# gkebnkctl architecture

This is the operator-facing map of the Go binary: what it provisions, how
the phases fit together, and the decisions that aren't obvious from the
code alone. The authoritative build contract is
[`SPEC-go-tracer.md`](SPEC-go-tracer.md) — this document summarizes and
links into it rather than duplicating it; when the two disagree, the spec
wins.

## What this is

`gkebnkctl` provisions a GKE cluster and installs **F5 BIG-IP Next for
Kubernetes (BNK)** with the **host-device** dataplane pattern, in pure Go
against the GCP SDKs + an in-process Kubernetes client — no `gcloud`,
`kubectl`, or `helm` shellouts. It is a 1:1 port of the live-proven bash
recipe under [`reference/bash-seed/`](../reference/bash-seed/), which
remains in the tree, untouched, as the provenance record. See
[`reference/TRIAL-NOTES.md`](../reference/TRIAL-NOTES.md) for the narrative
of the original live trial this binary encodes.

## Repo layout

```
cmd/gkebnkctl/          entrypoint -> internal/cli.Execute()
internal/
  cli/                   cobra commands: up/down/validate, phase sequencing
  intent/                cluster.yaml (gkebnkctl/v1) schema + loader
  gcp/
    clients.go            Clients bundle + narrow SDK interfaces + ADC auth
    auth.go                rest.Config builder (container API + ADC, no kubeconfig)
    state/                 state.env local cache (discovered values only)
    labels/                label scheme for GCP resources that support labels
    phases/                Phase00..Phase50 + PhaseDown{Cluster,Networks}
  netcalc/                 pure-Go port of the bash seed's IPv4 math
  k8s/
    apply.go                dynamic server-side-apply
    client.go                rest.Config -> typed/dynamic client construction
    wait.go                  readiness polling helpers
    exec.go                  non-interactive pod-exec capture (Phase50 verification)
    render/                  Go text/template rendering against typed Vars structs
    manifests/               go:embed of the manifest tree (static + templates)
examples/host-device/     the proven bnk-hostdev cluster.yaml
reference/                 bash-seed (untouched) + the original install/recon captures
```

## `cluster.yaml` intent schema

`apiVersion: gkebnkctl/v1`, `kind: Cluster`, strict-decoded (unknown fields
are load errors). Full field-by-field reference, defaults, and validation
rules: [`SPEC-go-tracer.md` §2](SPEC-go-tracer.md#2-clusteryaml-intent-schema).
Working example: [`examples/host-device/cluster.yaml`](../examples/host-device/cluster.yaml).

Two MTUs are configured independently and must not be conflated:
`network.{external,internal}.mtu` (8896 — the VPC jumbo wire MTU) vs
`bnk.tmmMtu` (1460 — the TMM interface / F5SPKVlan MTU).

## Phase model

`up` runs every phase 00→50 in order; each phase is idempotent and
re-runnable (name-based `Get`-then-skip — see "State & idempotency"
below). Full per-phase SDK-call/idempotency-check detail:
[`SPEC-go-tracer.md` §3](SPEC-go-tracer.md#3-phase-inventory).

| # | Phase | What it creates |
|---|-------|------------------|
| 00 | preflight | Nothing — ADC check, compute/container API-enabled check, best-effort quota warn |
| 10 | networks | main VPC (cluster-owned) + shared external/internal dataplane VPCs (created-if-missing) + Cloud Router/NAT |
| 20 | cluster | GKE cluster (DPv2 multi-networking) + default/tmm/agent node pools |
| 25 | discover | tmm/agent node iface/MAC/PCI + alias-CIDR/self-IP discovery, cached into state.env |
| 30 | namespaces | `f5-bnk`, `f5-utils`, `egress-test` |
| 31 | cert-manager | vendored static install YAML (no helm) |
| 32 | issuers | self-signed cluster issuer + `ext-ca` Certificate/ClusterIssuer |
| 33 | far-secret | pull-credential Secret in every `bnk.farSecret.namespaces` entry |
| 34 | multus | Multus CNI SA/ClusterRole/CRB/ConfigMap/NAD-CRD + DaemonSet |
| 35 | nads | `tmm-ext`/`tmm-int` host-device NetworkAttachmentDefinitions |
| 36 | flo | FLO operator + F5 CRDs (server-side apply — the manifest is too large for client-side) |
| 37 | node-sysctls | `bnk-node-sysctls` DaemonSet (forces `core_pattern` on TMM nodes) |
| 38 | cneinstance | `CNEInstance/instance` — the host-device PCI-passthrough contract |
| 39 | var-crash-patches | `/var/crash` emptyDir patched onto `f5-cne-controller` + `f5-crdconversion` |
| 40 | license | `License/bnk-license` (connected mode, raw JWT, never written to disk) |
| 41 | vlans-routes | `F5SPKVlan` ×2 + `F5SPKStaticRoute` ×6 (incl. `route-int-gw`) |
| 42 | tmm-settle | Reactive NIC-handoff-deadlock recovery (best-effort, never errors) |
| 50 | egress | `F5SPKEgress` + `bnk-agent-route-setter` DS + `egress-client` pod + gated verification |

### The GKE host-device contract

Seven items, each a specific live-discovered requirement, are threaded
through the phases above (Phase20's multi-networking, Phase25's discovery,
Phase38's `PCIDEVICE_*` env, Phase37's sysctls DS, Phase39's `/var/crash`
patch, Phase40's License CR, Phase41's `route-int-gw`). Full contract →
phase coverage map: [`SPEC-go-tracer.md` "GKE host-device contract →
coverage map"](SPEC-go-tracer.md#gke-host-device-contract--coverage-map-all-7-items-must-appear-declaratively);
narrative origin: [`reference/TRIAL-NOTES.md`](../reference/TRIAL-NOTES.md).

### Down phases — deliberately asymmetric

`down` does **not** mirror `up` in reverse. It runs exactly two steps:

1. **`PhaseDownCluster`** — deletes the GKE cluster (node pools die with it).
2. **`PhaseDownNetworks`** — deletes the **main VPC only**: firewall, then
   subnet, then VPC, in that order.

`network.external`/`network.internal` — the shared dataplane VPCs — are
**never referenced by `PhaseDownNetworks` at all**, not even as a
"skip these" list; the function's signature has no path to their names.
This is enforced by test (`down_test.go` asserts the fake VPC call log
never contains the ext/int names) and reprinted at runtime:

```
→ shared dataplane VPCs <ext>/<int> are NEVER deleted by teardown.
```

`down --dry-run` prints this same plan from `cluster.yaml` naming alone —
zero GCP calls, zero state reads required.

## State & idempotency

GCP has no universal resource-tag/label discovery the way AWS does, so
idempotency here is **deterministic naming**, not label-discovery: every
resource name is a pure function of `cluster.yaml`, so every phase's
mutation is guarded by a `Get(<name>)` → skip-if-exists check. `state.env`
(`.gkebnkctl/<cluster>/state.env`) caches only the values that are **not**
derivable from names — the Phase25-discovered iface/MAC/PCI/alias-CIDR/
self-IP/pod-GW set. Losing `state.env` is fully recoverable: delete it and
re-run `up`; it is a cache, not the record of truth. Full rationale:
[`SPEC-go-tracer.md` §4](SPEC-go-tracer.md#4-state-strategy).

## Kubernetes access

No kubeconfig file, no exec-credential plugin. `internal/gcp/auth.go`
builds a `rest.Config` directly from `ClusterManagerClient.GetCluster`'s
endpoint + CA cert, with `WrapTransport` wiring an OAuth2 transport over
Application Default Credentials so tokens auto-refresh across long waits
(the CNEInstance/License polls can run 20+ minutes). Full design:
[`SPEC-go-tracer.md` §5](SPEC-go-tracer.md#5-kubernetes-access-design-in-process-no-gcloudkubectl).

F5 CRs and NADs are outside the typed client-go scheme, so every apply
goes through `internal/k8s/apply.go`'s dynamic server-side-apply
(`FieldManager = "gkebnkctl"`) rather than typed structs.

## Dry-run

`up --dry-run` and `down --dry-run` make **zero** GCP/Kubernetes mutations.
The only permitted cloud/auth touch on `up --dry-run` is Phase00's ADC +
API-enabled read — and that too is skippable via `GKEBNKCTL_SKIP_AUTH=1`
(see "CI + local dev hooks" below). Full expected-output shape:
[`SPEC-go-tracer.md` §6](SPEC-go-tracer.md#6-dry-run-design).

## CI + local dev hooks

- **`GKEBNKCTL_SKIP_AUTH=1`** — skips Phase00's ADC + API-enabled checks
  entirely and passes a `nil *gcp.Clients` through every phase. Valid
  **only** together with `--dry-run` (rejected otherwise — a live run with
  nil clients would nil-pointer-deref on the first real GCP call). This is
  the offline/CI dry-run path: `.github/workflows/ci.yml`'s
  `validate-dryrun` job uses it to smoke-test `up --dry-run` and
  `down --dry-run` without any GCP credentials.
- **`GKEBNKCTL_VERIFY_EGRESS=1`** — enables Phase50's best-effort
  `tmctl clientside.pkts_in` delta + `curl` trace verification pass
  (ported from `reference/bash-seed/bin/40-egress.sh`). Default: skipped
  (build-first tracer milestone). A verification failure is logged but
  never fails `up`.

## Testing strategy

The GCP SDK exposes concrete clients, not interfaces, so every phase's
GCP-side dependency is a narrow interface defined in `internal/gcp/clients.go`
(`NetworksAPI`, `GKEAPI`, ...), with hand-rolled fakes in
`internal/gcp/phases/fakes_test.go`. On the Kubernetes side:

- `client-go`'s `dynamicfake` (the vendored client-go v0.35.1) does **not**
  support server-side-apply's `ApplyPatchType` — live-apply paths are
  covered by dry-run tests + `internal/k8s/render`'s golden-file tests
  instead; typed-API paths (Nodes, Pods, Deployments, ...) get full
  `k8sfake` coverage.
- SPDY pod-exec (`internal/k8s/exec.go`'s `ExecCapture`, used by Phase50's
  verification) needs a real upgradeable HTTP connection and cannot be
  faked directly — Phase50 depends on a function-typed seam
  (`egressExecFunc`) instead, so the PASS/FAIL/delta decision logic is
  fully unit-tested without one.
- Golden-file tests (`internal/k8s/render/testdata/*.golden.yaml`) assert
  the load-bearing host-device contract fields render correctly and double
  as a regression guard on the live-proven manifests.

Full strategy: [`SPEC-go-tracer.md` §7](SPEC-go-tracer.md#7-testing-strategy).

## Known open items

Carried over from the trial, intentionally out of scope for this binary:

- **Cluster-service DNS under capture times out** — pod → svc-CIDR
  (`10.67.0.0/20`) UDP/53 fails under TMM capture. Proven workaround: pod
  `dnsConfig` pointing at `8.8.8.8`. Not addressed by any phase.
- **TMM DaemonSet `OnDelete` update strategy** — Phase42 recovers from the
  NIC-handoff deadlock reactively (matches the bash ground truth exactly,
  including its no-same-node-matching quirk — see the deviation note in
  `internal/gcp/phases/phase42_tmmsettle.go`). Setting the DS to
  `updateStrategy: OnDelete` declaratively would avoid the deadlock
  proactively, but CNEInstance owns/reconciles that DS — fighting its
  declarative ownership needs more certainty than this tracer has.
- **`discover-pod.yaml.tmpl`** — the spec's original plan for Phase25's
  ephemeral discovery pod. Implemented instead as a typed `corev1.Pod` Go
  struct (`buildDiscoveryPod` in `internal/gcp/phases/phase25_discover.go`)
  since there's no F5/CRD-specific unstructured content in that pod spec.
- **SA1019 (deprecated GKE SDK ID-based request fields)** — the
  `container/apiv1` SDK's `ProjectId`/`Zone`/`ClusterId`/`NodePoolId`/
  `OperationId` fields are marked deprecated in favor of a single
  fully-qualified `Name` field. This codebase intentionally kept the
  ID-based constructors (matches the bash seed's discrete
  `--project`/`--zone`/`--cluster` flags); staticcheck's SA1019 is
  disabled repo-wide in `staticcheck.conf` rather than migrating every
  `GKEAPI` call site as a side effect of turning staticcheck on. Flagged as
  a follow-up.
- **FLO manifest is ~3 MB / 23 CRDs embedded into the binary.** Correctness
  over binary size for the tracer; slimming is a documented follow-up.

## Further reading

- [`SPEC-go-tracer.md`](SPEC-go-tracer.md) — the authoritative build
  contract (schema, phase inventory, state strategy, testing strategy,
  Builder task decomposition).
- [`reference/TRIAL-NOTES.md`](../reference/TRIAL-NOTES.md) — the original
  live-trial narrative this binary is a Go port of.
- [`reference/install/RUNBOOK.md`](../reference/install/RUNBOOK.md) — the
  exact install order used live.
- [`../README.md`](../README.md) — install/usage quick-start.
