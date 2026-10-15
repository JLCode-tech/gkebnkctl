# gkebnkctl architecture

This is the operator-facing map of the Go binary: what it provisions, how
the phases fit together, and the decisions that aren't obvious from the
code alone.

## What this is

`gkebnkctl` provisions a GKE cluster and installs **F5 BIG-IP Next for
Kubernetes (BNK)** with the **host-device** dataplane pattern, in pure Go
against the GCP SDKs + an in-process Kubernetes client — no `gcloud`,
`kubectl`, or `helm` shellouts.

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
    phases/                Phase00..Phase51 + PhaseDown{Cluster,Networks}
  netcalc/                 pure-Go IPv4 math
  k8s/
    apply.go                dynamic server-side-apply
    client.go                rest.Config -> typed/dynamic client construction
    wait.go                  readiness polling helpers
    exec.go                  non-interactive pod-exec capture (Phase50 verification)
    render/                  Go text/template rendering against typed Vars structs
    manifests/               go:embed of the manifest tree (static + templates)
examples/host-device/     example cluster.yaml intent file
```

## `cluster.yaml` intent schema

`apiVersion: gkebnkctl/v1`, `kind: Cluster`, strict-decoded (unknown fields
are load errors).
Working example: [`examples/host-device/cluster.yaml`](../examples/host-device/cluster.yaml).

Two MTUs are configured independently and must not be conflated:
`network.{external,internal}.mtu` (8896 — the VPC jumbo wire MTU) vs
`bnk.tmmMtu` (1460 — the TMM interface / F5SPKVlan MTU).

## Phase model

`up` runs all 21 phases `00`→`51` in order; each phase is idempotent and
re-runnable (name-based `Get`-then-skip — see "State & idempotency"
below).

| # | Phase | What it creates |
|---|-------|------------------|
| 00 | preflight | Nothing — ADC check, compute/container API-enabled check, best-effort quota warn |
| 10 | networks | main VPC (cluster-owned) + shared external/internal dataplane VPCs (created-if-missing) + per-cluster subnets + Cloud Router/NAT |
| 15 | jumphost | Optional GCE jumphost VM (`<cluster>-jumphost`) + IAP SSH firewall rule (`<cluster>-allow-iap-ssh`) on external VPC when `testing.jumphost.enabled: true` |
| 20 | cluster | GKE cluster (DPv2 multi-networking) + default/tmm/agent node pools |
| 25 | discover | tmm/agent node iface/MAC/PCI + alias-CIDR/self-IP discovery, cached into `state.env` |
| 29 | forge-register | Registers cluster with BNK Forge via MCP/REST when `forge.enabled: true` and writes `.gkebnkctl/<cluster>/forge_link.json` |
| 30 | namespaces | `cert-manager`, `f5-cne-core`, `f5-cne-system`, `f5-bnk-instance`, `f5-utils`, and `egress.testNamespace` (default `egress-test`), plus `coredns-f5` in `kube-system` and `kube-dns` `stubDomains` patch for `f5-cne-core.svc.cluster.local` |
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
| 51 | ingress-vxlan-routes | One `F5SPKStaticRoute` per agent-pool node with a discovered `vxlan100` VTEP — fixes ingress-to-egress-pod asymmetric routing |

### Bidirectional dataplane: why ingress needs its own vxlan route

Phase50's pseudo-CNI egress vxlan makes an egress-namespace pod's *outbound*
traffic symmetric (pod → vxlan100 → TMM → SNAT → internet) but leaves
*inbound* traffic to that same pod asymmetric: the pod's default route for
vxlan-steered traffic is `vxlan100 -> TMM`, so its replies leave over the
vxlan, but TMM *originates* ingress connections to the pod over the
standard Cilium route (`route-cluster-pods`, Phase41's cluster-wide pod
CIDR) — a path mismatch TMM's stateful connection tracking drops (the
return never arrives on the vxlan TMM expects it on). Phase51 closes this
by adding one `F5SPKStaticRoute` per agent-pool node — that node's pod CIDR
routed back over the SAME vxlan egress uses — more specific than
`route-cluster-pods` so it only affects TMM-originated ingress to that
node's pods, not the rest of the cluster.

The route's `gateway` is the node's `vxlan100` VTEP address, which
Phase51 **discovers**, not derives: neither `cluster.yaml` nor the
`F5SPKEgress` CR (`templates/egress.yaml.tmpl`) carry any vxlan *subnet*
configuration — only `vxlanKey`/`vxlanMtu`/`vxlanPort` — so the pseudo-CNI
controller is the sole owner of `vxlan100`'s IPAM and there is no
reproducible formula for it. Phase51 discovers each agent-pool node's live
`vxlan100` address the same way Phase25 discovers NIC iface/PCI info: an
ephemeral privileged pod, `nsenter -t1`'d into the node's host netns,
reading the interface directly — after Phase50 has applied `F5SPKEgress`
and waited for `egress-client` Ready, so the vxlan has had a chance to come
up. A node with no egress-namespace pod scheduled on it has no `vxlan100`
at all; Phase51 skips that node (no route emitted) rather than guessing,
since a wrong or absent gateway on a route this specific would black-hole
*all* ingress to that node's pods — worse than the bug it fixes.

### The GKE host-device contract

Seven items, each a specific live-discovered requirement, are threaded
through the phases above (Phase20's multi-networking, Phase25's discovery,
Phase38's `PCIDEVICE_*` env, Phase37's sysctls DS, Phase39's `/var/crash`
patch, Phase40's License CR, Phase41's `route-int-gw`).

### Down phases — deliberately asymmetric

`down` does **not** mirror `up` phase-for-phase. It runs four teardown steps:

1. **`Phase29ForgeRegisterDown`** — cleans up cluster-scoped benchmark targets in BNK Forge and unregisters the cluster via MCP/REST when registered.
2. **`PhaseDownJumphost`** — deletes `<cluster>-jumphost` and `<cluster>-allow-iap-ssh` when `testing.jumphost.enabled: true`.
3. **`PhaseDownCluster`** — best-effort strips F5 BNK CR finalizers, then deletes the GKE cluster (node pools die with it).
4. **`PhaseDownNetworks`** — deletes the **main VPC** (`firewall` → `subnet` → `VPC`). Unless `--keep-dataplane` is set, it also calls `teardownDataplaneResources` on `network.external.vpc` and `network.internal.vpc` to delete per-cluster dataplane subnets carrying this cluster's `gkebnkctl` ownership marker, and — if no other cluster's marked subnets remain in that shared VPC — deletes the last-cluster-out marked firewall rules and Cloud Routers/NATs. Unmarked resources are never touched.

The shared dataplane VPCs themselves (`network.external.vpc` / `network.internal.vpc`) are **NEVER deleted by teardown** (enforced by `down_test.go` and printed at runtime):

```
→ shared dataplane VPCs <ext>/<int> are NEVER deleted by teardown.
```

`down --dry-run` prints this same plan from `cluster.yaml` naming alone —
zero GCP calls, zero state reads required.

## State & idempotency

GCP has no universal resource-tag/label discovery the way AWS does, so
idempotency here is **deterministic naming for wholly-owned resources; a parsed `Description` ownership marker for resources inside the shared dataplane VPCs (GCP networking resources cannot carry labels)**. Every
resource name is a pure function of `cluster.yaml`, so every phase's
mutation is guarded by a `Get(<name>)` → skip-if-exists check. `state.env`
(`.gkebnkctl/<cluster>/state.env`) caches only the values that are **not**
derivable from names — the Phase25-discovered iface/MAC/PCI/alias-CIDR/
self-IP/pod-GW set. Losing `state.env` is fully recoverable: delete it and
re-run `up`; it is a cache, not the record of truth.

## Kubernetes access

No kubeconfig file, no exec-credential plugin. `internal/gcp/auth.go`
builds a `rest.Config` directly from `ClusterManagerClient.GetCluster`'s
endpoint + CA cert, with `WrapTransport` wiring an OAuth2 transport over
Application Default Credentials so tokens auto-refresh across long waits
(the CNEInstance/License polls can run 20+ minutes).

F5 CRs and NADs are outside the typed client-go scheme, so every apply
goes through `internal/k8s/apply.go`'s dynamic server-side-apply
(`FieldManager = "gkebnkctl"`) rather than typed structs.

## Dry-run

`up --dry-run` and `down --dry-run` make **zero** GCP/Kubernetes mutations.
The only permitted cloud/auth touch on `up --dry-run` is Phase00's ADC +
API-enabled read — and that too is skippable via `GKEBNKCTL_SKIP_AUTH=1`.

## CI + local dev hooks

- **`GKEBNKCTL_SKIP_AUTH=1`** — skips Phase00's ADC + API-enabled checks
  entirely and passes a `nil *gcp.Clients` through every phase. Valid
  **only** together with `--dry-run` (rejected otherwise).
- **`GKEBNKCTL_VERIFY_EGRESS=1`** — enables Phase50's best-effort
  `tmctl clientside.pkts_in` delta + `curl` trace verification pass.
  Default: skipped. A verification failure is logged but never fails `up`.

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

## Known open items

These items are intentionally out of scope for this binary:

- **Cluster-service DNS under capture times out** — pod → svc-CIDR
  (`10.67.0.0/20`) UDP/53 fails under TMM capture. Proven workaround: pod
  `dnsConfig` pointing at `8.8.8.8`. Not addressed by any phase.
- **TMM DaemonSet `OnDelete` update strategy** — Phase42 recovers from the
  NIC-handoff deadlock reactively (including its no-same-node-matching quirk — see the deviation note in
  `internal/gcp/phases/phase42_tmmsettle.go`). Setting the DS to
  `updateStrategy: OnDelete` declaratively would avoid the deadlock
  proactively, but CNEInstance owns/reconciles that DS — fighting its
  declarative ownership needs more certainty.
- `discover-pod.yaml.tmpl` — implemented instead as a typed `corev1.Pod` Go
  struct (`buildDiscoveryPod` in `internal/gcp/phases/phase25_discover.go`)
  since there's no F5/CRD-specific unstructured content in that pod spec.
- **FLO manifest is ~3 MB / 23 CRDs embedded into the binary.** Correctness
  over binary size; slimming is a documented follow-up.

## Further reading

- [`../README.md`](../README.md) — install/usage quick-start.
