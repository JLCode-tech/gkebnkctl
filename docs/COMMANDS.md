# Complete CLI Command Reference (`gkebnkctl`)

This document is the authoritative reference for all **24 top-level commands** and subcommands in `gkebnkctl`.

---

## Table of Contents

1. [Global Flags](#1-global-flags)
2. [Lifecycle Commands (`up`, `down`, `validate`, `init`)](#2-lifecycle-commands)
3. [Diagnostics & Inspection (`doctor`, `status`, `logs`, `topology`)](#3-diagnostics--inspection)
4. [Kubernetes Access (`kubeconfig`, `k`)](#4-kubernetes-access)
5. [Day-2 BNK 2.4 Operations (`bnk`)](#5-day-2-bnk-24-operations-bnk)
6. [Scenarios & Traffic Validation (`scenarios`, `test`)](#6-scenarios--traffic-validation)
7. [Performance Benchmarking (`benchmark`)](#7-performance-benchmarking-benchmark)
8. [BNK Forge Control Plane (`forge`, `bnkforge`)](#8-bnk-forge-control-plane-forge)
9. [Release Manifest Inspection (`manifest`)](#9-release-manifest-inspection-manifest)
10. [AI Agent & Workspace Audit (`agent`, `journal`)](#10-ai-agent--workspace-audit)
11. [Binary Management & Shell Completion (`install`, `self`, `version`, `completion`, `help`)](#11-binary-management--shell-completion)

---

## 1. Global Flags

Every `gkebnkctl` command inherits the following persistent flags from the root command:

| Flag | Short | Type | Default | Description |
| :--- | :---: | :--- | :--- | :--- |
| `--verbose` | `-v` | `bool` | `false` | Enable debug-level logging |
| `--quiet` | `-q` | `bool` | `false` | Suppress non-essential informational output |
| `--no-color` | | `bool` | `false` | Disable ANSI color output (also enabled when `NO_COLOR` is set) |
| `--workspace` | `-w` | `string` | `.` | Path to the cluster workspace directory |
| `--output` | `-o` | `string` | `text` | Output format (`text` or `json`) |

---

## 2. Lifecycle Commands

### `gkebnkctl up`

Provision the complete 3-VPC Compute Engine network, GCE jumphost, 3-pool GKE cluster, and F5 BNK 2.4 stack (Phases `00` through `51`). State is checkpointed to `.gkebnkctl/<cluster-name>/state.env`.

```bash
gkebnkctl up -f cluster.yaml
gkebnkctl up -f cluster.yaml --dry-run
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--dry-run` | | `false` | Print the 21-phase execution plan without creating cloud resources |

### `gkebnkctl down`

Tear down the GKE cluster, GCE jumphost, and cluster-owned `main` VPC. By default, shared `external` and `internal` dataplane VPCs are preserved so co-tenant clusters are never disrupted.

```bash
gkebnkctl down -f cluster.yaml
gkebnkctl down -f cluster.yaml --yes
gkebnkctl down -f cluster.yaml --dry-run
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--yes` | | `false` | Skip interactive confirmation prompt |
| `--dry-run` | | `false` | Print the teardown plan without deleting resources |
| `--keep-dataplane` | | `true` | Preserve shared `external` and `internal` dataplane VPCs |

### `gkebnkctl validate`

Validate `cluster.yaml` syntax, CIDR blocks, GKE node pool machine types, and credential file references offline.

```bash
gkebnkctl validate cluster.yaml
gkebnkctl validate examples/host-device/cluster.yaml
```

### `gkebnkctl init`

Scaffold a new deployment workspace (`cluster.yaml`, `keys/`, `artifacts/`, `AGENTS.md`, `.env`).

```bash
gkebnkctl init poc-gke \
  --project my-gcp-project \
  --name bnk-hostdev-01 \
  --region us-central1 \
  --zone us-central1-a
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--project` | | `""` | Target GCP project ID |
| `--name` | | *(dir name)* | Cluster name |
| `--region` | | `us-central1` | GCP region |
| `--zone` | | `us-central1-a` | GCP compute zone |
| `--main-cidr` | | `10.60.0.0/24` | Main VPC subnet CIDR |
| `--ext-cidr` | | `10.30.0.0/24` | External dataplane VPC CIDR |
| `--int-cidr` | | `10.50.0.0/24` | Internal dataplane VPC CIDR |
| `--force` | `-f` | `false` | Overwrite existing `cluster.yaml` if present |

---

## 3. Diagnostics & Inspection

### `gkebnkctl doctor`

Verify local CLI tools (`gcloud`, `kubectl`, `helm`, `jq`), active GCP authentication, required GCP APIs, and optional live Kubernetes cluster health.

```bash
gkebnkctl doctor --project my-gcp-project --region us-central1
gkebnkctl doctor --project my-gcp-project --strict
gkebnkctl doctor --backend k8s
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--project` | `""` | GCP project ID to verify API enablement and IAM access against |
| `--region` | `us-central1` | GCP region to check quotas/connectivity against |
| `--strict` | `false` | Treat warnings (such as missing optional tools `yq`/`openssl`) as errors |
| `--backend` | `local` | Diagnostic scope (`local` for workstation/GCP checks, `k8s` to include live cluster checks) |

### `gkebnkctl status`

Inspect live GKE cluster state, node pools, BNK 2.4 pods across `f5-cne-core` and `f5-cne-system`, `CNEInstance`, `F5SPKVlan`, and Gateway VIP readiness.

```bash
gkebnkctl status -f cluster.yaml
gkebnkctl status -f cluster.yaml -o json
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |

### `gkebnkctl logs`

Stream or tail logs from BNK 2.4 components (`flo`, `cis`, `cwc`, `cert-manager`, `cneinstance`, `fluentd`, `tmm`) with built-in shortcuts for AI governance and MCP filter logs.

```bash
gkebnkctl logs tmm -f
gkebnkctl logs flo --tail 200
gkebnkctl logs cwc --since 10m
gkebnkctl logs tmm --governance
gkebnkctl logs tmm --mcp
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--follow` | `-f` | `false` | Stream logs in real time |
| `--namespace` | `-n` | *(auto)* | Override component Kubernetes namespace |
| `--container` | `-c` | *(auto)* | Specific container name within the pod |
| `--previous` | `-p` | `false` | Print logs from the previous terminated container instance |
| `--since` | | `""` | Only return logs newer than a relative duration (e.g. `5m`, `1h`) |
| `--tail` | | `100` | Number of recent log lines to show |
| `--governance` | | `false` | Filter TMM logs for AI Gateway governance / token events |
| `--mcp` | | `false` | Filter TMM logs for iControl LX MCP session events |

### `gkebnkctl topology`

Render the GKE cluster, VPC networks, Multus NADs, TMM interfaces, and Gateway API topology as ASCII art, Mermaid diagrams, Graphviz DOT, or JSON.

```bash
gkebnkctl topology -f cluster.yaml
gkebnkctl topology -f cluster.yaml --format mermaid
gkebnkctl topology -f cluster.yaml --format dot -o topology.dot
gkebnkctl topology -f cluster.yaml --static
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--format` | | `ascii` | Output format: `ascii`, `mermaid`, `dot`, or `json` |
| `--output` | `-o` | `""` | Write output to file path instead of stdout |
| `--static` | | `false` | Render from `cluster.yaml` and `state.env` only without querying live K8s API |
| `--kubeconfig` | | `""` | Explicit kubeconfig path for live K8s discovery |

---

## 4. Kubernetes Access

### `gkebnkctl kubeconfig`

Fetch GKE cluster credentials into `artifacts/kubeconfig` or safely sync them into `~/.kube/config` (backing up your existing config to `~/.kube/config.gkebnkctl-bak`).

```bash
gkebnkctl kubeconfig -f cluster.yaml
gkebnkctl kubeconfig -f cluster.yaml --export
gkebnkctl kubeconfig -f cluster.yaml --write-global
gkebnkctl kubeconfig --restore-global
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--file` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--export` | | `false` | Print kubeconfig YAML to stdout |
| `--write-global` | | `false` | Safely sync to `~/.kube/config` (creates backup at `~/.kube/config.gkebnkctl-bak`) |
| `--restore-global` | | `false` | Restore `~/.kube/config` from `~/.kube/config.gkebnkctl-bak` |
| `--out` | | `artifacts/kubeconfig` | Custom output file path |

### `gkebnkctl k`

Run `kubectl` commands directly against the workspace cluster (`artifacts/kubeconfig`) without modifying your global `~/.kube/config`. All arguments and flags are passed straight through to `kubectl`.

```bash
gkebnkctl k get pods -A
gkebnkctl k get cneinstances,f5spkvlans,f5spkstaticroutes -n f5-cne-system
gkebnkctl k describe gateway -A
```

---

## 5. Day-2 BNK 2.4 Operations (`bnk`)

### `gkebnkctl bnk heal`

Detect and repair 9 common BNK 2.4 runtime failure modes on GKE (`multus-token-watch`, `tmm-log-stream`, `pod-manager`, `cwc`, `dssm-probe`, `controller-endpointslices`, `tmm-k8s-routes`, `epp-namespaces`, `test-namespace`).

```bash
gkebnkctl bnk heal -f cluster.yaml --dry-run
gkebnkctl bnk heal -f cluster.yaml
gkebnkctl bnk heal -f cluster.yaml --only cwc,tmm-k8s-routes
gkebnkctl bnk heal -f cluster.yaml -o json
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--kubeconfig` | | `""` | Explicit kubeconfig file path |
| `--dry-run` | | `false` | Inspect and report needed repairs without mutating the cluster |
| `--only` | | `""` | Comma-separated list of repair names to run |

### `gkebnkctl bnk resync`

Bump `HTTPRoute` metadata annotations to force the BNK 2.4 controller to re-evaluate routes affected by the Gateway startup race (`bnk.f5.com/hostname-error: "true"`).

```bash
gkebnkctl bnk resync my-route -n default
gkebnkctl bnk resync --all-in-ns -n default
gkebnkctl bnk resync --watch --debounce 15s
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--namespace` | `-n` | `default` | Target Kubernetes namespace |
| `--all-in-ns` | | `false` | Resync all `HTTPRoute` resources in the namespace |
| `--gateway-class` | | `f5-gateway-class` | Filter routes by parent GatewayClass |
| `--dry-run` | | `false` | Preview routes that would be bumped without patching |
| `--watch` | | `false` | Continuously watch and auto-resync routes with `hostname-error` |
| `--debounce` | | `15s` | Poll/debounce interval when `--watch` is enabled |
| `--kubeconfig` | | `""` | Explicit kubeconfig file path |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |

### `gkebnkctl bnk migrate-2.4`

Translate BNK 2.3 configuration into BNK 2.4 `CNEInstance` and `F5BnkGateway` custom resources.

```bash
gkebnkctl bnk migrate-2.4 -f cluster.yaml --dry-run
gkebnkctl bnk migrate-2.4 -f cluster.yaml --apply --wait
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--dry-run` | | `true` | Print generated 2.4 YAML manifests without applying |
| `--apply` | | `false` | Apply generated manifests to the cluster |
| `--kubeconfig` | | `""` | Explicit kubeconfig file path |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--namespace` | `-n` | `f5-cne-system` | Target namespace for `CNEInstance` and `F5BnkGateway` |
| `--infra-name` | | `bnk-gateway-config` | Name of the generated `F5BnkGateway` resource |
| `--gateway-class` | | `f5-gateway-class` | Name of the `GatewayClass` resource |
| `--single-self-ip` | | `true` | Emit single `/27` prefix self-IP per `F5SPKVlan` |
| `--wait` | | `false` | Wait for `CNEInstance` readiness after applying |

### `gkebnkctl bnk upgrade`

Perform an in-place BNK 2.4 release-manifest upgrade (FLO Helm upgrade + `CNEInstance.spec.manifestVersion` patch + rollout verification).

```bash
gkebnkctl bnk upgrade -f cluster.yaml --manifest-version 2.4.0-3.2882.0 --dry-run
gkebnkctl bnk upgrade -f cluster.yaml --manifest-version 2.4.0-3.2882.0 --timeout 15m
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--kubeconfig` | | `""` | Explicit kubeconfig file path |
| `--manifest-version` | | `2.4.0-3.2882.0` | Target F5 BNK release-manifest version |
| `--flo-version` | | *(from BOM)* | Explicit F5 Lifecycle Operator chart version override |
| `--namespace` | `-n` | `f5-cne-system` | Namespace of the `CNEInstance` resource |
| `--instance` | | `bnk-instance` | Name of the `CNEInstance` CR |
| `--service-cidr` | | `""` | Override Kubernetes service CIDR if needed during upgrade |
| `--timeout` | | `15m` | Rollout readiness timeout |
| `--dry-run` | | `false` | Preview upgrade steps without modifying the cluster |

### `gkebnkctl bnk mcp-session`

Generate and optionally apply iControl LX MCP session resources (`F5SPKiRule`, passphrase `Secret`, and `Gateway` listener patches).

```bash
gkebnkctl bnk mcp-session --name mcp-prod --gateway bnk-gateway --listener http
gkebnkctl bnk mcp-session --name mcp-prod --gateway bnk-gateway --apply --wait
```

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--name` | | `mcp-session` | Base name for generated MCP resources |
| `--namespace` | `-n` | `default` | Target Kubernetes namespace |
| `--type` | | `http` | Session transport type (`http` or `https`) |
| `--gateway` | | `bnk-gateway` | Target `Gateway` name |
| `--listener` | | `http` | Target listener name on the `Gateway` |
| `--irule` | | `mcp-session-irule` | Name of the `F5SPKiRule` CR |
| `--passphrase-env` | | `MCP_PASSPHRASE` | Environment variable holding the MCP passphrase |
| `--secret` | | `mcp-passphrase-secret` | Name of the Kubernetes `Secret` storing the passphrase |
| `--net-policy-name` | | `""` | Optional `NetworkPolicy` name to restrict MCP access |
| `--apply` | | `false` | Apply generated manifests directly to the cluster |
| `--wait` | | `false` | Wait for resource reconciliation after `--apply` |

---

## 6. Scenarios & Traffic Validation

### `gkebnkctl scenarios`

List, execute, or clean up the 15 built-in BNK validation scenarios. See [docs/SCENARIOS.md](SCENARIOS.md) for full details.

```bash
gkebnkctl scenarios list
gkebnkctl scenarios run http-routing-e2e -f cluster.yaml
gkebnkctl scenarios run --all -f cluster.yaml
gkebnkctl scenarios clean --all -f cluster.yaml
```

| Subcommand | Flags | Description |
| :--- | :--- | :--- |
| `scenarios list` | *(none)* | List all 15 scenarios, categories, and default VIP offsets |
| `scenarios run [name]` | `-f/--config`, `--all`, `--dry-run`, `--vip`, `--synthetic` | Deploy and verify a single scenario or `--all` |
| `scenarios clean [name]` | `-f/--config`, `--all` | Delete Kubernetes resources created by a scenario or `--all` |

### `gkebnkctl test`

Run targeted data-plane and cluster connectivity checks.

```bash
# Drive HTTP probes from the GCE jumphost (.200) through the Gateway VIP (.100)
gkebnkctl test traffic -f cluster.yaml --iterations 10 --json

# Test in-cluster CoreDNS resolution
gkebnkctl test dns --target f5.com

# Test pod-to-service connectivity
gkebnkctl test connectivity -n default

# Test outbound VXLAN F5SPKEgress SNAT routing
gkebnkctl test egress -f cluster.yaml
```

#### `gkebnkctl test traffic` Flags

| Flag | Short | Default | Description |
| :--- | :---: | :--- | :--- |
| `--config` | `-f` | `cluster.yaml` | Path to `cluster.yaml` |
| `--vip` | | *(derived `.100`)* | Gateway VIP to probe |
| `--iterations` | | `5` | Number of curl probe iterations |
| `--timeout` | | `10s` | Per-probe timeout |
| `--host` | | `""` | Optional HTTP `Host:` header |
| `--instance` | | *(from state)* | Override GCE jumphost instance name |
| `--dry-run` | | `false` | Print resolved jumphost, VIP, and `curl` command without executing |
| `--json` | | `false` | Output probe results and RTT statistics in JSON format |

---

## 7. Performance Benchmarking (`benchmark`)

Provision load generators on the GCE jumphost over IAP SSH and run dataplane or GenAI benchmarks. See [docs/BENCHMARKS.md](BENCHMARKS.md) for full workflows.

```bash
gkebnkctl benchmark setup -f cluster.yaml
gkebnkctl benchmark run -f cluster.yaml --protocol http --vip 10.30.0.100 --duration 30s --concurrency 64
gkebnkctl benchmark ingest -f cluster.yaml --input artifacts/genai.json
gkebnkctl benchmark list -f cluster.yaml
gkebnkctl benchmark status <run-id> -f cluster.yaml
gkebnkctl benchmark daemon start -f cluster.yaml
```

| Subcommand | Key Flags | Description |
| :--- | :--- | :--- |
| `benchmark setup` | `-f/--config`, `--instance`, `--dry-run` | Install `wrk`, `hey`, `ab`, `ghz`, and `iperf3` on the GCE jumphost |
| `benchmark run` | `-f/--config`, `--protocol` (`http\|https\|grpc\|tcp\|udp`), `--tool` (`wrk\|hey\|ab\|ghz\|iperf3`), `--vip`, `--port`, `--path`, `--host`, `--duration`, `--concurrency`, `--threads`, `--rate`, `--payload-size`, `--proto-service`, `--push`, `--forge-url`, `--cluster-id`, `--suite`, `--dry-run`, `-o/--output` | Execute a live dataplane benchmark from the GCE jumphost |
| `benchmark ingest` | `-f/--config`, `--input`, `--model`, `--backend`, `--concurrency`, `--prompt-tokens`, `--output-tokens`, `--ttft-p50/p95/p99`, `--tpot-p50/p95/p99`, `--tokens-per-sec`, `--requests-per-sec`, `--cache-hit-rate`, `--forge-url`, `--cluster-id`, `--suite`, `--dry-run` | Normalize and upload GenAI / LLM gateway benchmark metrics to BNK Forge |
| `benchmark list` | `-f/--config`, `--forge-url`, `--cluster-id`, `--limit`, `-o/--output` | List historical benchmark runs |
| `benchmark status [id]` | `-f/--config`, `--forge-url`, `--cluster-id`, `-o/--output` | Inspect detailed metrics for a benchmark run |
| `benchmark daemon` | `start`, `stop`, `status`, `logs` (`-f/--config`, `-n/--namespace`, `--interval`, `--forge-url`, `--cluster-id`, `--follow`, `--tail`, `--dry-run`) | Manage the in-cluster continuous benchmark daemon |

---

## 8. BNK Forge Control Plane (`forge`)

Register GKE BNK clusters with **BNK Forge** (`bnk-forge`), inspect heartbeat status, trigger remote benchmark suites, or unregister. (`gkebnkctl bnkforge` is supported as a backward-compatible alias.)

```bash
gkebnkctl forge register -f cluster.yaml --forge-url https://bnk-forge.example.com
gkebnkctl forge status -f cluster.yaml
gkebnkctl forge benchmark -f cluster.yaml --suite standard --wait
gkebnkctl forge cleanup -f cluster.yaml
gkebnkctl forge unregister -f cluster.yaml
```

| Subcommand | Key Flags | Description |
| :--- | :--- | :--- |
| `forge register` | `-f/--config`, `--forge-url`, `--cluster-id`, `--token`, `--dry-run` | Register cluster metadata and topology with BNK Forge |
| `forge status` | `-f/--config`, `--forge-url`, `--cluster-id`, `-o/--output` | Query cluster registration and heartbeat status |
| `forge benchmark` | `-f/--config`, `--forge-url`, `--cluster-id`, `--suite`, `--protocol`, `--duration`, `--concurrency`, `--wait`, `--timeout`, `--dry-run` | Trigger a remote benchmark job via BNK Forge |
| `forge cleanup` | `-f/--config`, `--kubeconfig`, `--dry-run` | Clean up stale benchmark jobs/pods in the cluster |
| `forge unregister` | `-f/--config`, `--forge-url`, `--cluster-id`, `--dry-run` | Remove cluster registration from BNK Forge |

---

## 9. Release Manifest Inspection (`manifest`)

### `gkebnkctl manifest probe`

Inspect the F5 BNK release-manifest Bill of Materials (chart versions and container image tags) without needing a live cluster.

```bash
gkebnkctl manifest probe
gkebnkctl manifest probe 2.4.0-3.2882.0 --all
gkebnkctl manifest probe 2.4.0-3.2882.0 --far keys/f5-far-auth-key.tgz
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--far` | `""` | Path to local FAR auth tarball or manifest directory |
| `--all` | `false` | Print every Helm chart and container image in the BOM |

---

## 10. AI Agent & Workspace Audit

### `gkebnkctl agent`

Generate workspace instruction files tailored to your `cluster.yaml` for AI coding assistants.

```bash
gkebnkctl agent all --force
gkebnkctl agent claude -f cluster.yaml
gkebnkctl agent gemini -f cluster.yaml
```

| Target Argument | Generated File |
| :--- | :--- |
| `claude` | `CLAUDE.md` |
| `gemini` | `GEMINI.md` |
| `opencode` | `AGENTS.md` |
| `cursor` | `.cursor/rules/gkebnkctl.mdc` |
| `all` | Generates all four harness instruction files |

### `gkebnkctl journal`

Maintain an append-only JSONL operational audit log in `artifacts/journal.jsonl`.

```bash
gkebnkctl journal add "Completed Phase 51 egress verification" --action VERIFY --tag gke
gkebnkctl journal list
gkebnkctl journal list --json
```

| Subcommand | Flags | Description |
| :--- | :--- | :--- |
| `journal add <msg>` | `--file`, `--actor`, `--action` (`NOTE`), `--tag` | Append a timestamped entry to the workspace journal |
| `journal list` | `--file`, `--json` | Display all recorded journal entries |

---

## 11. Binary Management & Shell Completion

### `gkebnkctl install`

Copy the currently running `gkebnkctl` binary onto your `$PATH` (`~/.local/bin` by default).

```bash
gkebnkctl install
gkebnkctl install --dir ~/.local/bin --force
sudo gkebnkctl install --dir /usr/local/bin
```

### `gkebnkctl self`

Check GitHub Releases for newer versions of `gkebnkctl` or self-update the binary in place.

```bash
gkebnkctl self check
gkebnkctl self update
```

### `gkebnkctl version`

Display binary version, git commit, build timestamp, Go version, and pinned component versions (`2.4.0-3.2882.0`, `cert-manager v1.16.1`, GKE `1.35`).

```bash
gkebnkctl version
gkebnkctl version --json
```

### `gkebnkctl completion`

Generate shell completion scripts for `bash`, `zsh`, `fish`, or `powershell`:

```bash
# Zsh (macOS default)
source <(gkebnkctl completion zsh)

# Bash
source <(gkebnkctl completion bash)
```
