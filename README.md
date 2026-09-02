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

| Subcommand | Description |
|---|---|
| **`gkebnkctl doctor`** | Validates workstation prerequisites, GCP ADC credentials, enabled APIs, and regional quotas |
| **`gkebnkctl init`** | Interactive wizard scaffolding `cluster.yaml`, `keys/`, `artifacts/`, and AI agent guides |
| **`gkebnkctl validate`** | Strict static validation of `cluster.yaml` without making cloud API calls |
| **`gkebnkctl up`** | Provisions VPCs, GKE multi-networking cluster, and installs BNK host-device (Phases 00–51) |
| **`gkebnkctl down`** | Tears down cluster and cluster-owned VPCs while preserving shared networks |
| **`gkebnkctl test`** | Built-in diagnostic suite: latency percentiles (P50/P95/P99), ECS DNS, and live Phase 50 egress probes |
| **`gkebnkctl kubeconfig`** | Exports cluster kubeconfig with automatic backup (`.gkebnkctl-bak`) and `--restore-global` |
| **`gkebnkctl manifest probe`**| Inspects and validates remote F5 FAR release manifests and CRD Bill-of-Materials |
| **`gkebnkctl journal`** | Append-only operational audit log and handover manager (`artifacts/journal.jsonl`) |
| **`gkebnkctl k`** | Cluster-scoped `kubectl` wrapper (`k get`, `k logs`, `k exec`, `k describe`, `k apply`) |
| **`gkebnkctl agent`** | Generates dual-path AI pair-programming instructions for Gemini, Claude, Cursor, and OpenCode |
| **`gkebnkctl bnkforge`** | Registers, unregisters, and queries cluster telemetry with central BNK Forge daemon |
| **`gkebnkctl install`** | Copies running binary into user PATH (`~/.local/bin`, `~/bin`, or `/usr/local/bin`) |
| **`gkebnkctl self`** | Release updater (`gkebnkctl self update`) and update checker (`gkebnkctl self check`) |
| **`gkebnkctl version`** | Displays CLI version, Git commit hash, build date, and Go toolchain |

---

## The GKE Host-Device Contract

`gkebnkctl` automates the host-device dataplane on GKE based on live-discovered invariants:

1. **Multi-Networking (GKE Datapath V2):** Node pools attach secondary (`tmm-ext`) and tertiary (`tmm-int`) NICs using `--additional-node-network` and `--additional-pod-network`. GKE auto-assigns per-node alias `/28` slices from secondary ranges.
2. **Host-Device NADs:** NetworkAttachmentDefinitions use `host-device` with static IPAM carrying discovered cloud self-IPs.
3. **TMM Environment & PCI Passthrough:** `CNEInstance` sets `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE` and binds discovered PCI slot addresses (`PCIDEVICE_INTEL_COM_EXT`/`_INT`).
4. **Node Sysctls:** `bnk-node-sysctls` DaemonSet sets `core_pattern` on TMM nodes, preventing postStart crash loops.
5. **Crash Storage:** Automatically injects `/var/crash` `emptyDir` volumes into `f5-cne-controller` and `f5-crdconversion`.
6. **Connected CWC Licensing:** `License` CR resolves F5 subscription JWTs against Cloud Web Console endpoints (`product-tst.apis.f5networks.net` / `product.apis.f5.com`) automatically.
7. **CoreDNS StubDomain Forwarding:** Deploys `coredns-f5` and updates `kube-dns` `stubDomains` for `f5-bnk.svc.cluster.local`, establishing reliable DSSM HA cluster discovery on GKE.
8. **Asymmetric Route Closure:** Discovers `vxlan100` VTEP addresses on agent nodes and injects node-specific `F5SPKStaticRoute`s to maintain symmetric ingress/egress return paths.

---

## Multi-Agent AI Workflow

`gkebnkctl` is built for pair programming with AI agents. Scaffold instructions for your preferred AI tool:

```bash
# Generate agent guidance for Gemini Code Assist / Antigravity
gkebnkctl agent --tool gemini

# Generate agent guidance for Claude Code
gkebnkctl agent --tool claude

# Generate agent guidance for Cursor
gkebnkctl agent --tool cursor

# Generate agent guidance for OpenCode
gkebnkctl agent --tool opencode
```

See [AGENTS.md](.agent/AGENTS.md) and [docs/ALIGNMENT.md](docs/ALIGNMENT.md) for agent roles and dual-path SDLC conventions.

---

## Configuration & Dry-Run Mode

The intent file (`cluster.yaml`) is the single source of truth:

```yaml
apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: gke-bnk-syd
  project: my-gcp-project
  region: australia-southeast1
  zone: australia-southeast1-a
network:
  main:
    cidr: 10.0.0.0/16
    podCidr: 10.1.0.0/16
    svcCidr: 10.2.0.0/20
  external:
    cidr: 10.10.0.0/16
    mtu: 8896
  internal:
    cidr: 10.20.0.0/16
    mtu: 8896
cluster:
  kubernetesVersion: "1.32"
  tmmPool:
    machineType: n2-standard-8
    nodeCount: 1
bnk:
  manifestVersion: "2.4.0"
  tmmMtu: 1460
  jwtPath: keys/f5-license.jwt
  farSecret:
    path: keys/far-credentials.json
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
