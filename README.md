# gkebnkctl

A single Go binary that provisions a GKE cluster and installs **F5 BIG-IP Next for Kubernetes (BNK)** with the **host-device** dataplane and working transparent egress.

> [!NOTE]
> This tool uses the pure GCP Go SDK and an in-process Kubernetes client. It requires **no** `gcloud`, `kubectl`, or `helm` shellouts, relying entirely on Application Default Credentials.

This binary is a Go port of a bash-first recipe proven live on cluster `bnk-hostdev` — the first-ever BNK host-device dataplane on GKE.

---

## Architecture & Design

- **Architecture:** For the phase-by-phase architecture (creation, teardown, state/idempotency strategy, and testing), see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

---

## The GKE host-device contract

Everything this binary does exists because of specific, live-discovered requirements.

Here are the 7 core requirements:

1. **Multi-networking:** Node pools use `--additional-node-network` and `--additional-pod-network` with multi-networking enabled at cluster-create time. GKE auto-assigns per-node alias `/28` slices from the secondary range (this is the cloud self-IP assignment).
2. **NADs:** NetworkAttachmentDefinitions are `host-device` with **static IPAM carrying the self-IP**. Interface names are discovered at runtime (e.g., GKE 1.35 uses `eth0/eth1/eth2`).
3. **TMM Environment:** `CNEInstance.spec.advanced.tmm.env` requires `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE` and `PCIDEVICE_INTEL_COM_EXT`/`_INT` set to the NIC's PCI address.
4. **Sysctls:** A `bnk-node-sysctls` DaemonSet forces `core_pattern` on TMM nodes to prevent postStart core-helper crash-loops.
5. **Crash Directory:** `f5-cne-controller` and `f5-crdconversion` need a `/var/crash` `emptyDir` patched in.
6. **Licensing:** A `License` CR (connected mode, F5 subscription JWT) is required before any VLAN programming occurs.
7. **Static Routes:** `F5SPKStaticRoute`s need the **internal gateway interface route** (`int-gw`) alongside the external one for egress to work.

---

## Quick Start

### Prerequisites

- Go 1.25+ (matches `go.mod`)
- Application Default Credentials for GCP (`gcloud auth application-default login`) or a service-account key via `GOOGLE_APPLICATION_CREDENTIALS`.

### Install

Install directly via `go install`:

```sh
go install github.com/JLCode-tech/gkebnkctl/cmd/gkebnkctl@latest
```

Or build from a clone:

```sh
git clone https://github.com/JLCode-tech/gkebnkctl.git
cd gkebnkctl
go build -o bin/gkebnkctl ./cmd/gkebnkctl
```

### Usage

> [!TIP]
> Every phase is idempotent and safely re-runnable after a partial failure. `gkebnkctl up` caches Phase25's discovered values into `.gkebnkctl/<cluster-name>/state.env`. Delete this directory and re-run `up` to force a fresh discovery pass.

```sh
# 1. Setup your cluster configuration
cp examples/host-device/cluster.yaml my-cluster.yaml

# 2. Parse & validate (no GCP calls)
gkebnkctl validate my-cluster.yaml

# 3. Provision & install (phases 00-51)
gkebnkctl up -f my-cluster.yaml

# 4. Tear down (cluster + main VPC only)
gkebnkctl down -f my-cluster.yaml
```

#### Dry-run Mode

You can run commands with `--dry-run` to print the plan without making any GCP/Kubernetes mutations:

```sh
gkebnkctl up -f my-cluster.yaml --dry-run
gkebnkctl down -f my-cluster.yaml --dry-run
```

To skip authentication entirely during a dry-run (e.g., for CI):

```sh
GKEBNKCTL_SKIP_AUTH=1 gkebnkctl up -f my-cluster.yaml --dry-run
```

### Egress verification (opt-in)

Phase50 can run a best-effort verification pass (mirroring the original bash recipe's `bin/40-egress.sh`).

> [!NOTE]
> A verification failure is logged but never fails `up`.

```sh
GKEBNKCTL_VERIFY_EGRESS=1 gkebnkctl up -f my-cluster.yaml
```

---

## Configuration

The `cluster.yaml` (`apiVersion: gkebnkctl/v1`, `kind: Cluster`) file is the source of truth for your cluster. 

> [!TIP]
> See [`examples/host-device/cluster.yaml`](examples/host-device/cluster.yaml) for a full annotated example.

| Section | Covers |
|---|---|
| `metadata` | GCP project, zone, cluster name. |
| `network.main` | This cluster's OWN VPC/subnet + pods/svcs secondary ranges. Destroyed with `down`. |
| `network.external` / `network.internal` | Dataplane VPCs (shared infrastructure). **Never destroyed** by `down`. |
| `cluster` | Kubernetes version, default/tmm/agent node pool machine types + counts. |
| `bnk` | manifestVersion, TMM MTU, JWT path, far-secret source. |
| `egress` | Test namespace, vxlan port/key/mtu. |

> [!WARNING]
> **Secrets are never embedded.** The F5 subscription JWT (`bnk.jwtPath`) and docker config file are referenced by path and rendered in-memory. They are never written to disk or committed.

---

## Testing & CI

To run the local gate set before pushing:

```sh
bash hack/ci-local.sh
```

You can also run tests manually:

```sh
go build ./...
go vet ./...
gofmt -l .
go tool staticcheck ./...
go test -race ./...
```

---

## Known Open Items

Carried over from the trial, unresolved by this binary:
- DNS-under-capture
- The TMM DS update strategy
- The `discover-pod.yaml.tmpl` deviation
- The FLO manifest size

See [`docs/ARCHITECTURE.md` "Known open items"](docs/ARCHITECTURE.md#known-open-items) for the full rationale.
