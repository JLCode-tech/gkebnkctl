# BNK GKE Host-Device Baseline Example

This directory contains the baseline 3-VPC GKE **host-device** configuration (`cluster.yaml`) for **F5 BIG-IP Next for Kubernetes (BNK)** on Google Kubernetes Engine.

## What's Included

- [`cluster.yaml`](cluster.yaml): Complete `gkebnkctl/v1` intent file for a 3-VPC (`main`, `external`, `internal`) GKE Datapath V2 multi-networking cluster with `default-pool`, `tmm-pool`, and `agent-pool`, plus Phase 50 transparent egress and Phase 51 VXLAN return-route configuration.
- [`license.jwt`](license.jwt): Committed dummy placeholder so `gkebnkctl validate` and `gkebnkctl up --dry-run` run offline in CI without real secret material. Replace with a valid F5 subscription JWT before running a live `gkebnkctl up`. See the [F5 BIG-IP Next for Kubernetes CloudDocs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/overview.html) for instructions on obtaining F5 licensing (`license.jwt`) and F5 Artifact Registry (`repo.f5.com`) credentials.

## Quick Start

### 1. Validate Schema Offline
```bash
gkebnkctl validate examples/host-device/cluster.yaml
```

### 2. Preview Phased Provisioning Plan (Dry-Run)
```bash
# Offline dry-run (no GCP credentials required)
GKEBNKCTL_SKIP_AUTH=1 gkebnkctl up -f examples/host-device/cluster.yaml --dry-run

# Offline teardown dry-run
GKEBNKCTL_SKIP_AUTH=1 gkebnkctl down -f examples/host-device/cluster.yaml --dry-run
```

### 3. Provision Live Cluster
Before provisioning against a live GCP project, edit `metadata.project`, `bnk.jwtPath`, and `bnk.farSecret` in `cluster.yaml` (or copy `examples/host-device/cluster.yaml` to `./cluster.yaml`):

```bash
gkebnkctl up -f examples/host-device/cluster.yaml
```

### 4. Run Diagnostics & Egress Verification
```bash
gkebnkctl status -f examples/host-device/cluster.yaml
gkebnkctl test egress -f examples/host-device/cluster.yaml
```

### 5. Teardown
```bash
gkebnkctl down -f examples/host-device/cluster.yaml --yes
```

## Architecture Notes

- **Three VPC Topology**:
  - `network.main` (`10.60.0.0/24`, pods `10.66.0.0/16`, services `10.67.0.0/20`): Cluster-owned VPC created and deleted with the cluster.
  - `network.external` (`10.30.0.0/24`, pods `10.31.0.0/24`, MTU `8896`): Shared external dataplane VPC.
  - `network.internal` (`10.50.0.0/24`, pods `10.51.0.0/24`, MTU `8896`): Shared internal dataplane VPC.
- **Node Pools**:
  - `defaultPool`: 3 nodes (`e2-standard-4`) for control/system components.
  - `tmmPool`: 1 node (`e2-standard-8`, `maxPodsPerNode: 8`) with secondary `tmm-ext` and tertiary `tmm-int` NICs handed to TMM via host-device NADs (`pciBusID`).
  - `agentPool`: 1 node (`e2-standard-4`, `maxPodsPerNode: 8`) hosting the egress test workload and VXLAN tunnel endpoint.
- See [`docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) and [`docs/PHASES.md`](../../docs/PHASES.md) for the complete 21-phase lifecycle details.
