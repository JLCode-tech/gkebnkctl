# gkebnkctl

[![CI](https://github.com/JLCode-tech/gkebnkctl/actions/workflows/ci.yml/badge.svg)](https://github.com/JLCode-tech/gkebnkctl/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/JLCode-tech/gkebnkctl?include_prereleases&sort=semver)](https://github.com/JLCode-tech/gkebnkctl/releases)
[![BNK Version](https://img.shields.io/badge/BNK-2.4.0-0a3a5c)](https://www.f5.com)
[![GKE Version](https://img.shields.io/badge/GKE-1.32%2B-326ce5?logo=googlecloud&logoColor=white)](https://cloud.google.com/kubernetes-engine)

**gkebnkctl** is a single Go binary that provisions **F5 BIG-IP Next for Kubernetes (BNK)** on Google Kubernetes Engine (GKE). It manages the entire lifecycle — VPCs, GKE multi-networking, node pools, host-device NetworkAttachmentDefinitions (NADs), CWC licensing, DSSM HA CoreDNS forwarders, F5 SPK CRDs, and transparent egress validation — using the GCP SDK directly and an in-process Kubernetes client.

No Terraform. No Helm. No host `kubectl`. **One binary, one intent file.**

---

## Prerequisites

| Need | Required for |
|---|---|
| **Go 1.25+** | Building from source (skip if using a release binary) |
| **GCP Project & Credentials** | Application Default Credentials (`gcloud auth application-default login` or `GOOGLE_APPLICATION_CREDENTIALS`) |
| **F5 Subscription JWT & FAR Secret** | BNK pull credentials and licensing (Phase 33 & 40) |

---

## Quick Start

```bash
# 1. Check host prerequisites, APIs, and GCP quotas
gkebnkctl doctor

# 2. Interactive setup — creates workspace, cluster.yaml, and keys/ directory
gkebnkctl init

# 3. Validate intent schema without making GCP API calls
gkebnkctl validate cluster.yaml

# 4. Provision infrastructure, GKE cluster, and install BNK host-device
gkebnkctl up -f cluster.yaml

# 5. Run built-in diagnostic latency percentiles and egress tests
gkebnkctl test -f cluster.yaml

# 6. Safe teardown (preserves shared dataplane VPCs)
gkebnkctl down -f cluster.yaml --yes
```

That's the 4-command lifecycle (`init` → `up` → `test` → `down`). See [docs/PHASES.md](docs/PHASES.md) for the phase-by-phase breakdown.

---

## Installation

| Option | How |
|---|---|
| **One-liner (Linux/macOS)** | `curl -fsSL https://raw.githubusercontent.com/JLCode-tech/gkebnkctl/main/install.sh \| sh` |
| **One-liner (Windows PowerShell)** | `irm https://raw.githubusercontent.com/JLCode-tech/gkebnkctl/main/install.ps1 \| iex` |
| **`go install`** | `go install github.com/JLCode-tech/gkebnkctl/cmd/gkebnkctl@latest` (Go 1.25+) |
| **Pre-built binary** | Download Linux/macOS/Windows archives + `checksums.txt` from [Releases](https://github.com/JLCode-tech/gkebnkctl/releases) |
| **From source** | `git clone https://github.com/JLCode-tech/gkebnkctl && cd gkebnkctl && make install` |
| **In-place self update** | `gkebnkctl self update` checks GitHub releases and updates the local binary in place |

---

## Command Reference

`gkebnkctl` provides 24 top-level commands (plus Cobra's built-in `help` and `completion`):

| Command | Subcommands & Key Flags | Description |
|---|---|---|
| **`gkebnkctl up`** | `-f/--config <cluster.yaml>`, `--dry-run` | Provisions VPCs, optional jumphost, GKE multi-networking cluster, Forge registration, and BNK host-device stack (21 phases: `00`–`51`) |
| **`gkebnkctl down`** | `-f/--config <cluster.yaml>`, `--yes`, `--dry-run`, `--keep-dataplane` | Unregisters from Forge, deletes jumphost, GKE cluster, main VPC, and cluster-owned dataplane subnets/routers/NATs/firewalls while preserving shared dataplane VPCs |
| **`gkebnkctl validate`** | `<cluster.yaml>` | Strict offline schema and semantic validation of `cluster.yaml` without making GCP API calls |
| **`gkebnkctl init`** | `[directory]`, `--project`, `--name`, `--region`, `--zone`, `--pattern`, `-f/--force` | Interactive or flag-driven wizard scaffolding `cluster.yaml`, `keys/` (`license.jwt`, `dockerconfig.json`), and `artifacts/` |
| **`gkebnkctl doctor`** | `--project`, `--region`, `--zone`, `--json` | Validates workstation prerequisites, GCP ADC credentials, enabled APIs, and regional compute/IP quotas |
| **`gkebnkctl status`** | `-f/--config <cluster.yaml>` | Summarizes workspace deploy state (`state.env`), cluster reachability, BNK 2.4/2.3 readiness, and Forge link |
| **`gkebnkctl logs`** | `<flo\|cis\|cert-manager\|cneinstance\|tmm>`, `-f/--follow`, `-n`, `-c`, `--governance`, `--mcp` | Streams component logs or filtered `BNKGOV` / MCP JSON-RPC governance decisions |
| **`gkebnkctl kubeconfig`** | `-f/--config <cluster.yaml>`, `--write-global`, `--merge`, `--restore-global` | Generates self-contained OAuth2 kubeconfig with automatic `.gkebnkctl-bak` backup and restore |
| **`gkebnkctl k`** | `<get\|describe\|logs\|exec\|apply\|delete\|top> [args...]` | In-process cluster-scoped `kubectl` replacement (no external `kubectl` binary required) |
| **`gkebnkctl bnk`** | `heal`, `resync`, `migrate-2.4`, `upgrade`, `mcp-session` | Day-2 BNK operations: 9-check self-healing (`multus-token-watch, tmm-log-stream, pod-manager, cwc, dssm-probe, controller-endpointslices, tmm-k8s-routes, epp-namespaces, test-namespace`), route resync, 2.3→2.4 migration, in-place upgrade, and MCP session affinity demo |
| **`gkebnkctl scenarios`** | `list`, `run [name] --all --dry-run --vip --synthetic -f`, `clean [name] --all -f` | Deploys, verifies, and cleans up end-to-end BNK traffic scenarios in topological dependency order |
| **`gkebnkctl test`** | `dns`, `connectivity`, `egress`, `traffic` (`--dry-run`, `-f/--file`, `--json`) | Built-in diagnostic suite: P50/P95/P99 latency percentiles, EDNS0/ECS DNS probes, TCP/HTTP connectivity, and Phase 50 egress verification |
| **`gkebnkctl forge`** | `register`, `status`, `unregister`, `cleanup`, `benchmark` (`--forge-mcp-url`, `-f/--config`) | Registers and manages the workspace with a BNK Forge instance over MCP (`http://localhost:8081/mcp/`) and REST |
| **`gkebnkctl benchmark`** | `setup`, `run`, `list`, `status`, `daemon` (`-f/--config`, `--scenario`, `--proxies`, `--synthetic`) | Runs `aiperf` GenAI inference benchmarks from the jumphost, multi-proxy shootouts, and the persistent WebSocket benchmark agent daemon |
| **`gkebnkctl topology`** | `-f/--config <cluster.yaml>`, `--format ascii\|mermaid\|dot\|json`, `--static` | Renders the 3-VPC, node-pool, TMM VLAN, PCI, VXLAN, and Gateway topology offline or enriched from live Kubernetes |
| **`gkebnkctl manifest`** | `probe [version]`, `--far`, `--all` | Probes and validates F5 BNK release manifests and image availability against F5 Artifact Registry (FAR) |
| **`gkebnkctl install`** | `--dir <path>` | Copies the running `gkebnkctl` binary into user `PATH` (`~/.local/bin`, `~/bin`, or `/usr/local/bin`) |
| **`gkebnkctl self`** | `update [--yes]`, `check` | Verifies SHA-256 checksums against `checksums.txt` and updates `gkebnkctl` in-place from GitHub Releases |
| **`gkebnkctl agent`** | `[all\|claude\|cursor\|copilot\|gemini\|codex]`, `-f/--file`, `--force` | Generates AI coding-assistant context files grounded in your `cluster.yaml` |
| **`gkebnkctl journal`** | `add <summary>`, `list` (`--file`, `--action`, `--actor`, `--tags`, `--json`) | Append-only operational audit log and multi-agent handover journal (`artifacts/journal.jsonl`) |
| **`gkebnkctl version`** | `--json` | Displays CLI release version, Git commit hash, build timestamp, Go toolchain, and pinned BNK/GKE versions |
| **`gkebnkctl completion`** | `bash`, `zsh`, `fish`, `powershell` | Generates shell autocompletion scripts |
| **`gkebnkctl help`** | `[command]` | Displays help for any command or subcommand |
| **`gkebnkctl bnkforge`** | `register`, `status`, `unregister` (`--url`, `-f`) | *Deprecated alias* — use `gkebnkctl forge` instead |

---

## Examples

All 5 committed example configurations under [`examples/`](examples/) are validated and dry-run tested in CI:

- [`examples/host-device/`](examples/host-device/README.md) — Baseline 3-VPC GKE host-device BNK 2.4 cluster with transparent egress and VXLAN return routing.
- [`examples/egress/`](examples/egress/README.md) — Live operator-driven transparent egress SNAT identity flip and `F5BigFwPolicy` egress firewall ACL demo.
- [`examples/ingress/`](examples/ingress/README.md) — North-South Gateway API L7 `HTTPRoute` ingress through BNK TMM on GKE.
- [`examples/multi-protocol-ingress/`](examples/multi-protocol-ingress/README.md) — Multi-protocol ingress (HTTP, HTTPS TLS termination, gRPC, and TCP L4) through a single BNK Gateway VIP.
- [`examples/vertex-agent-demo/`](examples/vertex-agent-demo/README.md) — AI agent governance demo pairing a Vertex AI (Gemini) tool-calling agent with BNK MCP governance, token counting, semantic cache, and egress controls.

---

## BNK Forge Integration

`gkebnkctl` integrates with **BNK Forge** via the `gkebnkctl forge` command group (talking to Forge's MCP server at `http://localhost:8081/mcp/` by default, configurable via `--forge-mcp-url` or `GKEBNKCTL_FORGE_MCP_URL`, with automatic REST fallback during `up`/`down` when `forge.enabled: true` is set in `cluster.yaml`):

```bash
# Register the cluster with BNK Forge over MCP (and run post-register scan + health check)
gkebnkctl forge register -f cluster.yaml --scan

# Inspect registration state and Forge reachability
gkebnkctl forge status -f cluster.yaml

# Run an aiperf benchmark from the jumphost and push telemetry to Forge
gkebnkctl forge benchmark -f cluster.yaml --scenario baseline

# Purge gkebnkctl-prefixed benchmark agents/configs for a workspace
gkebnkctl forge cleanup --workspace bnk-hostdev

# Unregister the cluster from Forge
gkebnkctl forge unregister -f cluster.yaml
```

> Note: `gkebnkctl bnkforge` is retained as a deprecated alias; use `gkebnkctl forge` for all new workflows. See [docs/FORGE_INTEGRATION.md](docs/FORGE_INTEGRATION.md) for full details.

---

## The GKE Host-Device Contract

`gkebnkctl` automates the host-device dataplane on GKE across 21 idempotent phases (`00` `preflight`, `10` `networks`, `15` `jumphost`, `20` `cluster`, `25` `discover`, `29` `forge-register`, `30` `namespaces`, `31` `cert-manager`, `32` `issuers`, `33` `far-secret`, `34` `multus`, `35` `nads`, `36` `flo`, `37` `node-sysctls`, `38` `cneinstance`, `39` `var-crash-patches`, `40` `license`, `41` `vlans-routes`, `42` `tmm-settle`, `50` `egress`, `51` `ingress-vxlan-routes`):

1. **Multi-Networking (GKE Datapath V2):** Node pools attach secondary (`tmm-ext`) and tertiary (`tmm-int`) NICs using additional node and pod networks. GKE auto-assigns per-node alias `/28` slices from secondary ranges.
2. **Host-Device NADs:** NetworkAttachmentDefinitions use `host-device` with static IPAM carrying discovered cloud self-IPs.
3. **TMM Environment & PCI Passthrough:** `CNEInstance` sets `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE` and binds discovered PCI slot addresses (`PCIDEVICE_INTEL_COM_EXT`/`_INT`).
4. **Node Sysctls:** `bnk-node-sysctls` DaemonSet sets `core_pattern` on TMM nodes, preventing postStart crash loops.
5. **Crash Storage:** Automatically injects `/var/crash` `emptyDir` volumes into `f5-cne-controller` and `f5-crdconversion`.
6. **Connected CWC Licensing:** `License` CR resolves F5 subscription JWTs against Cloud Web Console endpoints (`product-tst.apis.f5networks.net` / `product.apis.f5.com`) automatically.
7. **CoreDNS StubDomain Forwarding:** Phase 30 creates the 6 required namespaces (`cert-manager`, `f5-cne-core`, `f5-cne-system`, `f5-bnk-instance`, `f5-utils`, and `egress.testNamespace`), deploys `coredns-f5` in `kube-system`, and updates `kube-dns` `stubDomains` for `f5-cne-core.svc.cluster.local`, establishing reliable DSSM HA cluster discovery on GKE.
8. **Asymmetric Route Closure:** Phase 51 discovers `vxlan100` VTEP addresses on agent nodes and injects node-specific `F5SPKStaticRoute`s to maintain symmetric ingress/egress return paths.
9. **Safe Shared-VPC Teardown:** `gkebnkctl down` never deletes the shared dataplane VPCs (`network.external.vpc` / `network.internal.vpc`) themselves, while cleaning up per-cluster dataplane subnets and last-cluster-out shared routers/Cloud NATs/firewalls carrying the `gkebnkctl` ownership marker (unless `--keep-dataplane` is passed).

---

## Multi-Agent AI Workflow

`gkebnkctl` is built for pair programming with AI agents. Scaffold instructions for your preferred AI tool:

```bash
# Generate agent guidance for all supported tools (Claude, Cursor, Copilot, Gemini, Codex)
gkebnkctl agent all -f cluster.yaml

# Or target a single tool
gkebnkctl agent gemini -f cluster.yaml
gkebnkctl agent claude -f cluster.yaml
gkebnkctl agent cursor -f cluster.yaml
```

See [docs/ALIGNMENT.md](docs/ALIGNMENT.md) for multi-cloud CLI conventions.

---

## Configuration & Dry-Run Mode

The intent file (`cluster.yaml`) is the single source of truth:

```yaml
apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: bnk-hostdev
  project: my-gcp-project
  zone: us-central1-a
pattern: host-device
network:
  main:
    vpc: bnkhd-vpc
    subnet: bnkhd-subnet
    cidr: 10.60.0.0/24
    podsRangeName: bnkhd-pods
    podsCidr: 10.66.0.0/16
    svcsRangeName: bnkhd-svcs
    svcsCidr: 10.67.0.0/20
  external:
    vpc: bnkhd-ext
    subnet: bnkhd-ext
    cidr: 10.30.0.0/24
    mtu: 8896
    podsRangeName: tmm-ext-pods
    podsCidr: 10.31.0.0/24
    underlayCidr: 10.30.0.0/16
  internal:
    vpc: bnkhd-int
    subnet: bnkhd-int
    cidr: 10.50.0.0/24
    mtu: 8896
    podsRangeName: tmm-int-pods
    podsCidr: 10.51.0.0/24
  allowInternalCidr: 10.0.0.0/8
  natGateways: true
cluster:
  kubernetesVersion: "1.35"
  defaultPool:
    machineType: e2-standard-4
    nodeCount: 3
  tmmPool:
    name: tmm-pool
    machineType: e2-standard-8
    nodeCount: 1
    maxPodsPerNode: 8
  agentPool:
    name: agent-pool
    machineType: e2-standard-4
    nodeCount: 1
    maxPodsPerNode: 8
bnk:
  manifestVersion: "2.4.0-3.2882.0"
  tmmMtu: "1460"
  jwtPath: ./keys/license.jwt
  farSecret:
    source: dockerconfig
    dockerConfigFile: ./keys/dockerconfig.json
```

### Dry-Run Preview (No Cloud Mutations)

```bash
# Preview provisioning actions
gkebnkctl up -f cluster.yaml --dry-run

# Preview actions in CI without GCP credentials
GKEBNKCTL_SKIP_AUTH=1 gkebnkctl up -f cluster.yaml --dry-run
```

---

## Documentation Index

- [Architecture & Design](docs/ARCHITECTURE.md) — Subsystem breakdown, state management, and network models.
- [Multi-Cloud Parity](docs/ALIGNMENT.md) — Comparison across `gkebnkctl`, `awsbnkctl`, `roksbnkctl`, and `ocibnkctl`.
- [Provisioning Phases](docs/PHASES.md) — Detailed guide to Phases 00 through 51.
- [BNK Forge Integration](docs/FORGE_INTEGRATION.md) — Connecting with the multi-cloud test harness.
- [Release Process](docs/RELEASE.md) — Versioning, GoReleaser matrix, and artifact checksums.
- [Contributing Guide](CONTRIBUTING.md) — Local development workflow and testing guidelines.
- [Changelog](CHANGELOG.md) — Per-version release notes and feature history.

---

## License

Licensed under the [MIT License](LICENSE).
