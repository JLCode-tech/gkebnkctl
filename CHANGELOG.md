# Changelog

All notable changes to `gkebnkctl` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.1.0](https://github.com/JLCode-tech/gkebnkctl/compare/v1.0.1...v1.1.0) (2026-10-23)


### Features

* align F5 BNK 2.4 namespaces and port status, logs, and topology CLI commands ([#65](https://github.com/JLCode-tech/gkebnkctl/issues/65)) ([3b0763d](https://github.com/JLCode-tech/gkebnkctl/commit/3b0763d4c6198145d80e564986bee9c2dba3d1b9))
* backport P0/P1 cross-cloud parity fixes ([e018d6a](https://github.com/JLCode-tech/gkebnkctl/commit/e018d6ae02da1716cb2fda5cbd2f358e4f9dbfad))
* backport P0/P1 cross-cloud parity fixes (BNK 2.4 CRDs, OCI FLO Helm SDK, in-process k8s, migrate/upgrade, aiperf, bnkscan, forge) ([f02ca24](https://github.com/JLCode-tech/gkebnkctl/commit/f02ca2481465ec04acf6615a1577937a9f821fb5))
* **benchmark:** benchmark engine, forge agent auth, and genai metrics parity ([#60](https://github.com/JLCode-tech/gkebnkctl/issues/60)) ([60ceabc](https://github.com/JLCode-tech/gkebnkctl/commit/60ceabc1a5b5c2ca6f455dc7fb1b9e2fec5d0c37))
* BNK 2.4 Gateway/GIE CRDs, JWKS rotation, VXLAN scenario namespaces & example parity ([2d4e789](https://github.com/JLCode-tech/gkebnkctl/commit/2d4e78985a528fbda1faaf2382a2c54e9024e96c))
* BNK 2.4 Gateway/GIE CRDs, JWKS rotation, VXLAN scenario namespaces & example parity ([c4cdf0b](https://github.com/JLCode-tech/gkebnkctl/commit/c4cdf0b23b3f09cc21cfbfb456f8520eeb36bc7b))
* **cli,ci,docs:** add self update checksums, scenarios --all, and align CI/release/docs parity ([#68](https://github.com/JLCode-tech/gkebnkctl/issues/68)) ([9b1323b](https://github.com/JLCode-tech/gkebnkctl/commit/9b1323b8ee3ac66b3ba540731cb1fc5828ebbc09))
* **cli:** add bnk resync, test traffic, and vertex demo hardening ([#62](https://github.com/JLCode-tech/gkebnkctl/issues/62)) ([5d72615](https://github.com/JLCode-tech/gkebnkctl/commit/5d72615c3bb1fb004326779ef5dc297c2ed2c35a))
* dynamic ingress vxlan routing, 15 Gateway API scenarios, and BNK CLI parity ([58677fe](https://github.com/JLCode-tech/gkebnkctl/commit/58677fe964804d7b6f89fcb3662d1a299cb6121b))
* **gcp:** sync unified bnkctl 2.4.1 AI Gateway, Forge governance, and public-ready docs ([f95e2ce](https://github.com/JLCode-tech/gkebnkctl/commit/f95e2ced3f2e26e3758c7965a796a26b4b256049))
* **gcp:** sync unified bnkctl 2.4.1 AI Gateway, Forge governance, and public-ready docs ([4105402](https://github.com/JLCode-tech/gkebnkctl/commit/410540280b16fb5aa309ec50b08f96c98e468450))


### Bug Fixes

* apply P0/P1 runtime parity and scenario verification fixes ([145d0e3](https://github.com/JLCode-tech/gkebnkctl/commit/145d0e3585f8aee36cd679cf8a41d35f38e4a374))
* **down:** match full Compute API network URLs in DiscoverDataplaneResources ([#67](https://github.com/JLCode-tech/gkebnkctl/issues/67)) ([278f2cd](https://github.com/JLCode-tech/gkebnkctl/commit/278f2cd6c4ebd66ce5faeb0fdbf6ee6121ddecb6))
* **gke,forge,license,scenarios:** wire Phase29 Forge lifecycle, fix CPCL license sync, and align BNK 2.4 CRDs/scenarios ([5f754bd](https://github.com/JLCode-tech/gkebnkctl/commit/5f754bd96eab1c250ff84e7ea575242c5ffae7cd))
* **lint:** resolve staticcheck unused symbols and ST1005 error capitalization ([46da6c2](https://github.com/JLCode-tech/gkebnkctl/commit/46da6c271d9ffcbd7cfa8614d6f27d094c94d71c))
* **parity:** align FLO GatewayClass/CRDs/issuer, aiperf agentx-mvp, and self-update with awsbnkctl/azrbnkctl ([4505072](https://github.com/JLCode-tech/gkebnkctl/commit/4505072a0cd7c096eed81323c752e31fb8cfa69e))
* **phases:** add JWT preflight validation and License status visibility in TMMSettle ([b1a2e28](https://github.com/JLCode-tech/gkebnkctl/commit/b1a2e288a6b633d6197d358bf7bd4b6c5bc78ec6))

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
