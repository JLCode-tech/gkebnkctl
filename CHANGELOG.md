# Changelog

All notable changes to `gkebnkctl` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.0.0] - 2026-09-02

### Features & CLI Alignment
- **`doctor`**: Host preflight validation for required binaries (`gcloud`, `kubectl`), GCP Application Default Credentials, required GCP APIs (`compute.googleapis.com`, `container.googleapis.com`, `iam.googleapis.com`), and regional quota inspection.
- **`init`**: Interactive workspace scaffolding generating `cluster.yaml` (strict schema validated), `keys/` credential storage (gitignored), `artifacts/`, and AI pair-programming instructions (`AGENTS.md`).
- **`install`**: Idempotent local CLI installer deploying the running binary directly into user PATH (`~/.local/bin`, `~/bin`, `/usr/local/bin`, or `--dir`).
- **`manifest probe`**: Remote preflight inspection of F5 BNK release manifests / Bill-of-Materials without requiring an active cluster.
- **`journal`**: Append-only operational audit trail and handoff note manager stored at `artifacts/journal.jsonl`.
- **`k`**: Cluster-scoped `kubectl` wrapper supporting subcommands (`k get`, `k logs`, `k exec`, `k describe`, `k apply`, `k delete`) without switching the global workstation context.
- **`agent`**: Multi-agent pair-programming recipe generator supporting Gemini (`.gemini/`), Claude (`.claude/`), Cursor (`.cursor/`), and OpenCode (`.opencode/`).
- **`bnkforge`**: Native REST client for registering, unregistering, and inspecting cluster state with the central BNK Forge daemon.
- **`self`**: In-place CLI updater (`self update`) and release checker (`self check`) querying GitHub releases.
- **`test`**: Comprehensive diagnostic test suite featuring DNS latency percentiles (Min, P50, P95, P99, Max) with EDNS client subnet (ECS) support, TCP/UDP reachability matrix, and Phase 50 live egress validation.
- **`kubeconfig`**: Automated credential export and `~/.kube/config` sync with safety backups (`.gkebnkctl-bak`) and instant rollback (`--restore-global`).
- **`install.sh` / `install.ps1`**: One-liner installer scripts for Linux/macOS and Windows PowerShell with automated SHA256 checksum verification.

### GKE & CNE Dataplane Hardening
- **CoreDNS StubDomain Forwarder**: Deployed `coredns-f5` and configured `kube-dns` `stubDomains` for `f5-bnk.svc.cluster.local`, resolving 3-node DSSM HA discovery on GKE SkyDNS.
- **CWC Licensing Auto-Routing**: Removed hardcoded production licensing endpoints from `license-cr.yaml.tmpl`, enabling automatic resolution of test/trial tokens (`product-tst.apis.f5networks.net`) and production tokens (`product.apis.f5.com`).
- **CrashAgent Support**: Automated discovery and `/var/crash` `emptyDir` volume patching across all `f5-bnk` and `f5-utils` controllers and pods.
- **Multi-NIC Persistence**: Discovered secondary and tertiary host-device NICs and persistent caching in `.gkebnkctl/<cluster-name>/state.env`.
- **Server-Side Apply Force Ownership**: Enabled `force=true` on Server-Side Apply for all DaemonSets and CoreDNS manifests to resolve client-side apply conflicts.
- **Egress Telemetry**: Enhanced Phase 50 live egress verification to scrape `debug` container tmstat tables (`/var/tmstat/blade/tmm0`).

### Network Ownership & Teardown Safeguards
- **GKE-05 / GKE-06 / GKE-07**: Resource ownership tagging via GCP descriptions with safe shared VPC teardown checks (destroying dataplane networks only when solely owned by the cluster).
- **Accurate Dry-Run Planning**: Complete execution preview for `up --dry-run` and `down --dry-run`.
