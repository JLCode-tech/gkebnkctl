# AI Inference Benchmarking & Proxy Shootout Guide

`gkebnkctl` provides an enterprise-grade AI performance benchmarking engine tailored for **F5 BIG-IP Next for Kubernetes (BNK)** and the **F5 BNK AI Service Plane** on Google Kubernetes Engine (GKE). It drives real-world and synthetic LLM inference workloads using NVIDIA's [`aiperf`](https://github.com/triton-inference-server/perf_analyzer) tool directly from an in-VPC GCE test jumphost VM, measures critical GenAI metrics (TTFT, ITL, token throughput, and prefix cache hit rates), and pushes telemetry into [BNK Forge](https://github.com/JLCode-tech/bnk-forge) for real-time visualization and multi-proxy shootout analysis. For complete product documentation, see the [F5 BIG-IP Next for Kubernetes CloudDocs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/overview.html).

---

## 1. Why Benchmark F5 BNK for AI?

Modern GenAI inference serving imposes unique demands on networking infrastructure:

- **Time to First Token (TTFT)**: High connection setup latency or proxy buffer bloat directly degrades conversational AI responsiveness.
- **Inter-Token Latency (ITL)**: Streaming Server-Sent Events (SSE) require zero packet jitter and high-efficiency TCP window management.
- **Prompt Prefix Caching**: Reusing common prompt prefixes (e.g., system instructions, few-shot examples) in vLLM / SGLang drastically cuts TTFT, provided the ingress layer supports intelligent request routing and sticky session affinity.
- **Line-Rate Throughput**: When serving multi-tenant LLM clusters across tens of concurrent users, traditional software proxies (Envoy, NGINX, HAProxy) often become CPU-bottlenecked by user-space context switches. F5 BNK TMM (Traffic Management Microkernel) uses dedicated GKE Datapath V2 multi-networking interfaces (`host-device` NADs and PCI passthrough) to deliver predictable, microsecond-level proxy latencies.

`gkebnkctl` makes benchmarking push-button: zero manual `aiperf` script writing, zero public exposure of private VIP endpoints, and automated multi-proxy comparative shootouts.

---

## 2. Benchmarking Architecture

Because F5 BNK's Gateway VIP lives on an isolated, non-routable external data-plane VPC (`10.30.0.0/24`), external workstations cannot hit the VIP directly. `gkebnkctl` solves this with a secure bidirectional architecture:

```mermaid
flowchart LR
    subgraph Operator["Operator Workstation"]
        direction TB
        UI["Forge Web UI (:3000)<br/>Dashboard & Sweeps"]
        Server["Forge Server (:8000)<br/>Dispatcher & Telemetry DB"]
        Daemon["gkebnkctl daemon<br/>Persistent Agent Listener"]
        UI --> Server
        Server <--> Daemon
    end

    subgraph VPC["GCP Private Data Plane"]
        direction TB
        IAP["IAP / SSH Tunnel<br/>(Jumphost Tunnel)"]
        JH["In-VPC GCE Jumphost<br/>(aiperf Runner: 10.30.0.200)"]
        IAP --> JH
    end

    subgraph Ingress["F5 BNK Data Plane (External VPC: 10.30.0.0/24)"]
        direction TB
        VIP["Gateway VIP: 10.30.0.100:80<br/>(Host: gkebnkctl-aiinference.local)"]
        TMM["TMM Microkernel (host-device)<br/>Prefix Routing & Rate Limiting"]
        VIP --> TMM
    end

    subgraph Serving["Inference Cluster (GKE)"]
        direction TB
        vLLM[("vLLM Model Serving<br/>Llama-3.1-8B GPU Pod")]
        EPP[("Prefill & Decode Endpoints<br/>Prometheus /metrics")]
    end

    Daemon -->|Secure Command Tunnel| IAP
    JH -->|L7 SSE Inference Stream| VIP
    TMM -->|Inference Forwarding| vLLM
    TMM -->|Disaggregated Routing| EPP
    Daemon -.->|Scrape Prometheus Metrics| EPP
```

### Architecture Highlights
1. **Workstation Daemon**: `gkebnkctl benchmark daemon` runs locally on the operator workstation and maintains a persistent WebSocket connection with 15-second heartbeats to Forge.
2. **In-VPC Execution**: Benchmark commands are dispatched over IAP / SSH to the GCE Jumphost VM (or via an in-cluster probe pod when `GKEBNKCTL_PROBE_IN_CLUSTER=1` is active), keeping the BNK VIP private inside the external VPC.
3. **In-Network Driver**: The dual-NIC GCE Jumphost VM attaches directly to the external data-plane VPC (`10.30.0.200`), generating high-concurrency traffic directly against the TMM Gateway VIP.
4. **Header Injection**: The runner automatically injects required Gateway API headers (`Host: gkebnkctl-aiinference.local`) to match Kubernetes `HTTPRoute` rules.
5. **Real-Time GenAI Scrapes**: Prometheus `/metrics` are scraped before and after each run to measure exact prefix-cache hit rates and KV-cache pool utilization.

---

## 3. Command Suite Reference

The `gkebnkctl benchmark` command family covers the full lifecycle of AI performance validation:

| Command | Primary Flags | Purpose |
|---|---|---|
| `benchmark setup` | `-f <cluster.yaml>`, `--preflight`, `--daemon` | Verifies `aiperf >= 0.10.0` on jumphost, registers SSH AccessMethod, BenchmarkAgent, and Target in Forge. |
| `benchmark daemon` | `-f <cluster.yaml>`, `--forge-agent-token`, `--heartbeat-interval` | Runs the persistent WebSocket listener to receive and execute dispatched benchmark tasks from Forge UI. |
| `benchmark run` | `-f <cluster.yaml>`, `--scenario`, `--scenarios`, `--proxies` | Executes a benchmark run directly via CLI (single run, smoke preset, native Forge scenario, or multi-proxy shootout). |
| `benchmark list` | `-o json` | Lists all available native Forge scenarios and built-in smoke presets. |
| `benchmark status` | `-f <cluster.yaml>`, `-w <workspace>` | Inspects jumphost readiness, `aiperf` version, Forge linkage, and discovered targets. |

---

## 4. Built-in Smoke Presets (`--scenarios`)

For rapid health validation and quick sanity checks, `gkebnkctl` includes 4 built-in smoke presets accessible via the `--scenarios` flag:

```bash
# Run a single preset
gkebnkctl benchmark run -f cluster.yaml --scenarios latency

# Run multiple presets sequentially
gkebnkctl benchmark run -f cluster.yaml --scenarios latency,throughput,streaming

# Run all 4 presets
gkebnkctl benchmark run -f cluster.yaml --scenarios all
```

| Preset Name | Concurrency | Num Requests | ISL (Input Tokens) | OSL (Output Tokens) | Streaming | Primary Focus |
|---|:---:|:---:|:---:|:---:|:---:|---|
| `latency` | 1 | 50 | 512 | 128 | Yes | Baseline TTFT and single-stream latency without queuing. |
| `throughput` | 32 | 500 | 512 | 128 | No | Maximum sustained request and token throughput under heavy saturation. |
| `long-context` | 4 | 50 | 4096 | 512 | Yes | Memory bandwidth, KV cache expansion, and long-context decode performance. |
| `streaming` | 8 | 200 | 512 | 256 | Yes | Mid-load conversational streaming with Server-Sent Events (SSE). |

---

## 5. Native Forge Scenarios (`--scenario`)

For exhaustive performance benchmarking and characterization, `gkebnkctl` implements the full native Forge benchmark engine (WS-C1/WS-C2). Each scenario automatically expands into an ordered sequence of child runs (e.g. concurrency sweeps, multi-turn phases, or multi-round burst probes).

```bash
# Run the baseline concurrency sweep
gkebnkctl benchmark run -f cluster.yaml --scenario baseline

# Run prefix-cache evaluation followed by real-world Mooncake trace replay
gkebnkctl benchmark run -f cluster.yaml --scenario prefix-cache,mooncake
```

### Scenario Catalog

| Key | Scenario Name | Concurrency Sweep | ISL / OSL | Workload Pattern | Key Metric Validated |
|---|---|---|---|---|---|
| `baseline` | Baseline | 50, 100, 150, 200 | 500 / 128 | Synthetic chat completions; $rc = \max(c \times 5, 20)$. | TTFT and ITL curve across increasing concurrency. |
| `high-concurrency` | High Concurrency | 150, 200, 250, 300 | 5000–10000 / 128 | Heavy prompt pairs: (150, 5k), (200, 7k), (250, 9k), (300, 10k). | Proxy connection management under massive payload sizes. |
| `mixed-workload` | Mixed Workload | 50, 100, 150, 200 | Multi-phase | 3 phases: Warmup ($c=50$), Short (ISL 500), Long (ISL 600 with prefix prompts). | Dynamic queue scheduling and adaptation. |
| `multi-turn` | Multi-Turn | 50, 100, 150, 200 | 500 / 128 | 4 turns: Turn 1 (no prefix), Turns 2–4 with growing prefix length (500, 1000, 1500). | Chat session persistence and incremental prefix caching. |
| `prefix-cache` | Prefix Cache | 150, 200, 250, 300 | Heavy pairs | 80% shared prefix (length 4k–8k tokens) across 20 prompt pools. | KV-cache hit rate and TTFT drop on cached prompts. |
| `bimodal` | Bimodal Distribution | 50, 100, 150, 200 | Bimodal distribution | 70% short prompts (ISL 300 / OSL 64), 30% long prompts (ISL 4000 / OSL 256). | Head-of-line blocking prevention in proxy queues. |
| `sustained-load` | Sustained Load | 50, 100, 150, 200, 250 | 1500±300 / 128 | Endurance test: $rc = c \times 10$ (up to 2,500 requests per step). | Long-term memory stability and steady-state throughput. |
| `burst-recovery` | Burst Recovery | Alternating 200 / 25 | Bimodal / 256 | 5 consecutive rounds: Burst phase ($c=200, 400$ reqs) followed by Probe ($c=25, 50$ reqs). | Speed of latency recovery after sudden traffic spikes. |
| `mooncake` | Mooncake Trace | Open-loop trace | Production trace | Replay of KVCache Mooncake tool-agent production trace with 0.80x time-dilation. | Real-world production traffic resilience. |

> [!TIP]
> **Cold Cache Resets (`--reset-cache`)**: When running consecutive scenarios like `baseline,prefix-cache`, pass `--reset-cache` to trigger an automated cold-redeploy of the inference backend between scenario keys. This guarantees that KV-cache state does not bleed between runs.

---

## 6. Multi-Proxy Shootout Mode (`--proxies`)

The **Multi-Proxy Shootout** evaluates comparative performance by running identical synthetic or trace workloads against F5 BNK and alternative ingress architectures deployed side-by-side in the same GKE cluster:

```mermaid
flowchart LR
    Client([In-VPC GCE Jumphost<br/>aiperf Runner])

    subgraph Proxies["Proxy Architectures Under Test"]
        BNK["F5 BNK (TMM)<br/>host-device / External VPC"]
        Envoy["Envoy Gateway<br/>Internal LoadBalancer"]
        HAProxy["HAProxy Ingress<br/>Internal LoadBalancer"]
        NGINX["NGINX Ingress<br/>Internal LoadBalancer"]
        NodePort["Direct Pod IP<br/>(Direct Baseline)"]
    end

    Backend[("vLLM Model Server<br/>Llama-3.1-8B GPU Pod")]

    Client -->|Benchmark Run 1| BNK --> Backend
    Client -->|Benchmark Run 2| Envoy --> Backend
    Client -->|Benchmark Run 3| HAProxy --> Backend
    Client -->|Benchmark Run 4| NGINX --> Backend
    Client -->|Benchmark Run 5| NodePort --> Backend
```

### Running a Shootout
```bash
gkebnkctl benchmark run -f cluster.yaml \
  --proxies f5-bnk,envoy,haproxy,nodeport \
  --scenario baseline \
  --model meta-llama/Llama-3.1-8B-Instruct
```

### How Shootout Works
1. When `--proxies` includes non-BNK proxies (e.g. `envoy`, `haproxy`), `gkebnkctl` tags the Forge `BenchmarkTarget` with `proxy_expose=internal-nlb` and points to the target Kubernetes Service.
2. The benchmark engine executes the scenario sweep against each proxy front-end in sequence.
3. With `--direct-pod-ip <IP>`, an un-proxied `nodeport` baseline run is included to isolate proxy overhead from raw inference execution.
4. Results are tagged by proxy type (`proxy=f5-bnk`, `proxy=envoy`, etc.) and rendered side-by-side in Forge's comparative analytics view.

---

## 7. GenAI Metrics & Prometheus Scraping

Beyond standard HTTP response codes and raw round-trip latency, `gkebnkctl` captures deep GenAI inference telemetry:

| Metric | Description | Source |
|---|---|---|
| `ttft_p50_ms` / `p90` / `p95` / `p99` | Time to First Token percentiles (ms). | `aiperf` streaming timestamps |
| `itl_p50_ms` / `p95` / `p99` | Inter-Token Latency percentiles (ms per token chunk). | `aiperf` SSE chunk delta |
| `output_tokens_per_sec` | Total output tokens delivered divided by elapsed duration. | `aiperf` output token count |
| `prefix_cache_hit_rate` | Ratio of prompt tokens serviced from GPU KV memory cache (0.0 to 1.0). | Scraped Prometheus delta |
| `prefill_worker_utilization` | Average GPU KV memory cache allocated on prefill workers. | Scraped Prometheus gauge |
| `decode_worker_utilization` | Average GPU KV memory cache allocated on decode workers. | Scraped Prometheus gauge |

### Configuring Prometheus Scrapes
```bash
# Scrape vLLM pods through Kubernetes API pod proxy
gkebnkctl benchmark run -f cluster.yaml --scenario prefix-cache \
  --metrics-pod-selector app=vllm \
  --metrics-namespace default \
  --metrics-port 8000 \
  --genai-out /tmp/genai-results.json

# Scrape disaggregated prefill/decode endpoints via Jumphost curl
gkebnkctl benchmark run -f cluster.yaml --scenario prefix-cache \
  --metrics-url prefill=http://10.50.0.11:8000/metrics \
  --metrics-url decode=http://10.50.0.12:8000/metrics
```

---

## 8. Synthetic Mode (`--synthetic`) for CI/CD

To test benchmark pipelines, Forge linkages, and dashboard reporting without provisioning expensive GPU instances:

```bash
gkebnkctl benchmark run -f cluster.yaml --synthetic --scenario baseline
```

In synthetic mode, `gkebnkctl` simulates a realistic vLLM `Llama-3-8B` inference endpoint (`llm-d-inference-sim`), generating complete TTFT, ITL, and token throughput telemetry while running entirely on standard CPU node pools.

---

## 9. Step-by-Step Operator Runbook

### Scenario A: Operator Web UI Driven Session
1. **Pre-stage Jumphost**:
   ```bash
   gkebnkctl benchmark setup -f cluster.yaml --forge-user admin --forge-pass changeme
   ```
2. **Start Daemon on Workstation**:
   ```bash
   gkebnkctl benchmark daemon -f cluster.yaml --forge-user admin --forge-pass changeme
   ```
3. Open `http://localhost:3000` (Forge Web UI), navigate to **Benchmarks** → **Run Benchmark**, select the Jumphost Agent, choose `prefix-cache`, and click **Start**.

### Scenario B: Fully Automated CLI Sweep for Comparative Benchmarks
```bash
# 1. Verify environment
gkebnkctl benchmark status -f cluster.yaml

# 2. Run head-to-head proxy shootout
gkebnkctl benchmark run -f cluster.yaml \
  --proxies f5-bnk,envoy,haproxy \
  --scenario baseline,prefix-cache \
  --run-label proxy-shootout \
  --genai-out /tmp/shootout.json
```
