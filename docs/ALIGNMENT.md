# Multi-Cloud *bnkctl Family Alignment

This document tracks feature parity and architectural conventions across the **`*bnkctl`** multi-cloud CLI family:

- **`awsbnkctl`**: AWS EKS implementation (secondary ENIs, SR-IOV, NLB, Gateway API).
- **`gkebnkctl`**: Google Cloud GKE implementation (host-device multi-networking, CoreDNS HA forwarding, transparent egress).
- **`roksbnkctl`**: IBM Cloud ROKS implementation (OpenShift ROKS, IBM Cloud Object Storage, GSLB-aware DNS probing).
- **`ocibnkctl`**: Local developer / edge implementation (k3s containerised nodes, OCI runtime virtio demo mode).

---

## Core Principles & Design Invariants

Across all four tools, the following architectural invariants are preserved:

1. **Single Binary, Zero External CLI Dependencies**:
   - Pure cloud SDKs (GCP, AWS, IBM Cloud, OCI/Docker).
   - In-process Kubernetes dynamic client (`client-go`).
   - No `terraform`, `helm`, or host `kubectl` shellouts required for core lifecycle operations.
2. **Intent File as Single Source of Truth**:
   - Strict-decoded YAML (`cluster.yaml` / `poc.yaml`).
   - Secrets are never embedded or written to disk; certificates, far-secrets, and JWTs are rendered in-memory or referenced via secure local paths.
3. **Deterministic 4-Command Lifecycle**:
   - `init` -> `up` -> `test` -> `down`.
4. **Idempotent and Resume-Safe**:
   - Every phase uses name-based `Get`-then-skip or Server-Side Apply.
   - Partial failures can be safely resumed by re-running `up`.

---

## Command Matrix & Parity

| Subcommand | `gkebnkctl` | `awsbnkctl` | `roksbnkctl` | `ocibnkctl` | Description |
|---|:---:|:---:|:---:|:---:|---|
| **`doctor`** | ✅ | ✅ | ✅ | ✅ | Validates workstation prerequisites, IAM/ADC, APIs, and cloud quotas |
| **`init`** | ✅ | ✅ | ✅ | ✅ | Scaffolds workspace, intent file (`cluster.yaml`), and `keys/` directories |
| **`validate`** | ✅ | ✅ | ✅ | ✅ | Performs deep static validation of intent file without making cloud API calls |
| **`up`** | ✅ | ✅ | ✅ | ✅ | Orchestrates multi-phase provisioning and BNK installation |
| **`down`** | ✅ | ✅ | ✅ | ✅ | Safely deprovisions cluster while protecting shared cloud infrastructure |
| **`test`** | ✅ | ✅ | ✅ | ✅ | Runs latency percentiles (P50/P95/P99), ECS DNS, L4/L7 probes, and egress tests |
| **`kubeconfig`**| ✅ | ✅ | ✅ | ✅ | Safely exports cluster credentials with automated backup and restore |
| **`manifest`** | ✅ | ✅ | 🔄 | 🔄 | Probes and validates remote F5 FAR manifests and release bundles |
| **`journal`** | ✅ | ✅ | 🔄 | 🔄 | Append-only operational journal and handoff notes manager |
| **`k`** | ✅ | ✅ | 🔄 | 🔄 | Cluster-scoped `kubectl` wrapper without mutating global context |
| **`agent`** | ✅ | ✅ | 🔄 | 🔄 | Scaffolds dual-path AI pair-programming instructions (`AGENTS.md`) |
| **`forge`** | ✅ | ✅ | 🔄 | ✅ | Registers and coordinates cluster with BNK Forge (`register \| status \| unregister \| cleanup \| benchmark`; `bnkforge` deprecated alias) |
| **`install`** | ✅ | ✅ | ✅ | 🔄 | Idempotent CLI installer to user PATH (`~/.local/bin`, `~/bin`, `/usr/local/bin`) |
| **`self`** | ✅ | ✅ | ✅ | 🔄 | Checks for updates (`self check`) and updates binary in-place (`self update`) |
| **`version`** | ✅ | ✅ | ✅ | ✅ | Displays version, Git commit hash, build timestamp, and Go toolchain |

*Legend: ✅ Complete & Verified | 🔄 Cloud-specific or in roadmap*

---

## Dataplane Architecture Comparison

| Feature | `gkebnkctl` (GCP) | `awsbnkctl` (AWS) | `roksbnkctl` (IBM Cloud) | `ocibnkctl` (Local/OCI) |
|---|---|---|---|---|
| **Dataplane Mode** | Host-Device (`pciBusID` passthrough) | Secondary ENIs / SR-IOV / DPDK | Calico / Multus SR-IOV | Virtio container netns (demo) |
| **Cluster Engine** | GKE Datapath V2 Multi-networking | EKS VPC CNI with secondary ENIs | OpenShift ROKS on VPC Gen2 | k3s on host Docker/Podman |
| **DSSM HA Clustering** | CoreDNS StubDomain forwarder | Route53 / CoreDNS stubDomain | OpenShift DNS operator | k3s CoreDNS rewrite |
| **Licensing Mode** | Connected JWT (Auto CWC routing) | Connected JWT | Connected JWT + COS pull secret | Connected JWT / FAR secret |
| **Egress Routing** | `F5SPKEgress` + vxlan100 + Cloud NAT | NAT Gateway + Secondary ENI | Cloud Gateway / NAT | Host NAT forwarding |
