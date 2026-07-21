# D-033 — Bidirectional Host-Device TMM Dataplane on GKE (ingress-vxlan-routes)

- **Status:** Accepted
- **Date:** 2026-07-21
- **Scope:** `internal/gcp/phases/phase51_ingress_vxlan.go` (Phase51), `k8s/manifests/templates/ingress-vxlan-routes.yaml.tmpl`
- **Gold standard:** cluster `bnk-hostdev` (live GKE trial, 2026-07-21) — see `reference/TRIAL-NOTES.md` and `docs/ARCHITECTURE.md` "Bidirectional dataplane"
- **Related:** sibling repo `bnk-forge-v2` tracks the same `D-033` identifier for the **EKS** host-device TMM dataplane (`docs/adr/D-033-host-device-tmm-dataplane.md` there, AWS-specific: ENI/PCI/CNEInstance env). The identifier is deliberately reused across the two repos for cross-repo traceability of the shared "host-device TMM dataplane" architecture theme; this ADR is the **gkebnkctl-side** instance and covers a distinct, GKE-only problem (pseudo-CNI vxlan asymmetric routing) that has no EKS equivalent.

## Context

`gkebnkctl` installs BNK on GKE using the **host-device** dataplane pattern: TMM owns the node's NIC directly (PCI passthrough via `CNEInstance`/`F5SPKVlan`, not an ipvlan/macvlan NAD). On top of host-device, **transparent egress** (Phase50) uses GKE Dataplane V2 (Cilium)'s pseudo-CNI: an `F5SPKEgress` CR causes the `bnk-agent-route-setter` DaemonSet to install a per-pod `ip rule ... lookup 276 -> default via <tmm vxlan100 VTEP> dev vxlan100` on every pod in the egress-designated namespace, steering that pod's default route to TMM over a vxlan tunnel (`vxlan100`) instead of the normal Cilium pod network. This makes egress traffic symmetric and source-NAT-able by TMM: pod → vxlan100 → TMM → SNAT → internet, proven live.

This same route rewrite breaks **ingress** to the *same* pods. The pod's default route now sends **all** outbound packets — including replies to inbound connections — over `vxlan100`. But TMM does not *originate* ingress connections to backend pods over that vxlan; it originates them over the cluster's standard Cilium route, `route-cluster-pods` (`10.66.0.0/16 via <cilium-gw>`, installed generically by Phase41 for the whole pod CIDR, not vxlan-aware). The result is a **path mismatch**: TMM's SYN goes out via `route-cluster-pods`/Cilium, the pod's SYN-ACK comes back via `vxlan100`, and TMM's stateful connection tracking drops the reply because it never arrives on the interface/path TMM expects for that flow. Symptom: virtual server traffic to an egress-steered backend shows `serverside.bytes_in=0` — TMM never sees a response, even though the pod is healthy and can reach the internet fine outbound.

In short: **egress works because it is flow/source-learning-based** (the pod initiates, TMM only needs to see and NAT the outbound packet); **ingress fails because it is route-based** (TMM originates via a route that no longer matches where the target pod will actually send its reply).

## Decision

Add **Phase51 (`ingress-vxlan-routes`)**, which runs after Phase50 (egress) and emits one `F5SPKStaticRoute` per agent-pool node:

```
<node's Spec.PodCIDR, e.g. 10.66.4.0/24> via <that node's live vxlan100 VTEP, e.g. 10.0.64.1>
```

as a `gateway`-type route in the `f5-bnk` namespace. Because this route's destination is a single node's pod CIDR — strictly more specific than Phase41's cluster-wide `route-cluster-pods: 10.66.0.0/16` — it takes routing precedence for that node's pods only, without altering routing to the rest of the cluster. With it in place, TMM's ingress-originated connections to that node's egress-steered pods now go out over the same `vxlan100` path the pod's replies use — a symmetric path — closing the gap.

**Proven live** on cluster `bnk-hostdev`, 2026-07-21: route `10.66.4.0/24 via 10.0.64.1` applied; a VIP ingress request to a pod on that node returned `HTTP 200`, and the same pod's outbound egress still SNAT'd correctly to `34.61.234.229` — ingress and egress both working through the same pod, simultaneously.

### VTEP is discovered, not derived

Phase51 discovers each agent-pool node's live `vxlan100` address at runtime rather than computing it from any formula. This is deliberate:

- Neither `cluster.yaml` (`intent.EgressSpec`) nor the `F5SPKEgress` CR template (`templates/egress.yaml.tmpl`) carry any vxlan *subnet*/IPAM configuration — only `vxlanKey`, `vxlanMtu`, `vxlanPort`. The pseudo-CNI controller is the sole owner of `vxlan100`'s address assignment; there is no reproducible scheme in this codebase (or in `cluster.yaml`) to predict it offline.
- Phase51 instead discovers it live: an ephemeral privileged pod is scheduled on each agent-pool node, `nsenter -t1`'d into the node's host network namespace (the same pattern Phase25 uses to discover NIC iface/MAC/PCI info), reading `vxlan100`'s address directly with `ip addr show`. This must happen *after* Phase50 has applied `F5SPKEgress` and waited for the `egress-client` pod to go Ready, so the vxlan interface has actually come up on that node.
- A node with no egress-namespace pod scheduled on it has no `vxlan100` interface at all. Phase51 treats that as "skip this node" (no route emitted), not an error — a wrong or absent gateway on a route this specific would **black-hole all ingress** to that node's pods outright, which is strictly worse than the asymmetric-routing bug this phase exists to fix.

### Declared routes are authoritative (drift reconciliation)

BNK's `f5validate.f5net.com` admission webhook enforces a unique
`destination`+`prefixLen` across **all** `F5SPKStaticRoute`s, *regardless of CR
name* — confirmed live on `bnk-hostdev` 2026-07-21:

```
CR route-ingress-vxlan-10-66-4-0 cannot be created or updated with an already
existing staticroute destination address 10.66.4.0 with prefixLen 24
```

So a hand-applied route occupying a destination this tool declares does not
merely coexist — it hard-fails the apply and aborts the whole `up` run. Since
`gkebnkctl` registers its clusters into Forge, which manages drift, the tool
must be the source of truth for what it declares rather than yielding to
whatever is already on the cluster.

Phase41 and Phase51 therefore apply their manifests through
`reconcileStaticRouteDrift` (`internal/gcp/phases/staticroute_drift.go`), which
deletes any `F5SPKStaticRoute` occupying a declared destination under a
*different* name before applying. Three cases, one code path:

- **greenfield** — no conflict; nothing deleted; plain apply.
- **idempotent re-run** — the occupying route *is* ours (same name); it is left
  alone and server-side apply updates it in place. Verified live: the webhook
  accepts a same-name re-apply and only rejects a different CR claiming a taken
  destination.
- **drift** — an operator hand-applied the route, or an older release used a
  different name; it is replaced by the declared one.

Scope is deliberately narrow: a route is deleted only on an **exact**
`destination`+`prefixLen` match with one being created under a different name.
Phase41's cluster-wide `route-cluster-pods` (`10.66.0.0/16`) is therefore never
a conflict for Phase51's per-node `10.66.4.0/24` — same supernet, different
prefix. Deletion also waits for the CR to actually disappear rather than merely
be marked for deletion: these routes carry the `handletmmconfig_inconsistency`
finalizer, so removal is asynchronous and a premature re-apply would still trip
the webhook.

Replacing a live route is briefly traffic-affecting — between delete and
re-apply TMM has no route for that destination. The webhook forbids
create-before-delete, so the window is unavoidable; it is logged loudly at
`[drift]` rather than hidden.

## Consequences

- Ingress and egress now both work correctly, simultaneously, on the same egress-steered pod — the primary goal.
- The new routes only affect TMM-*originated* traffic to the specific node CIDRs they target; the cluster-wide `route-cluster-pods` route and all other traffic are untouched.
- Phase51 has a hard ordering dependency: it must run after Phase50 (needs `vxlan100` to exist) and after Phase41 (its routes are deliberately more-specific overrides of `route-cluster-pods`, not a replacement for it).
- Phase51 cannot be fully rendered offline / in `--dry-run`: the VTEP is only knowable by querying a live cluster, so `up --dry-run` only announces intent for this phase (see `hack/ci-local.sh`'s `[phase 51]` assertion, which guards that the phase stays wired into the `up` sequence even though its real effect can't be dry-run-verified).
- Adds one more privileged ephemeral discovery pod per agent-pool node during `up`, mirroring Phase25's existing discovery-pod pattern (no new privilege model introduced).

## Alternatives considered

1. **Hardcode a VTEP-derivation formula** (e.g. a fixed TMM-side-`.1`/node-offset scheme) instead of discovering it live. Rejected: nothing in `cluster.yaml`, the `F5SPKEgress` CR, or the pseudo-CNI's own configuration surface pins such an offset — it is entirely owned by the pseudo-CNI controller's internal IPAM. Hardcoding it would be an unverified guess, and a wrong gateway on a route this specific silently black-holes ingress rather than failing loudly.
2. **Move backend pods off the agent-pool node** (avoid transparent-egress steering entirely for ingress-serving pods). Rejected: this loses egress for those pods, which defeats the purpose of enabling transparent egress in the first place — the goal is both directions working on the same pod.
3. **Modify Phase41 (`vlans-routes`) or Phase50 (`egress`) directly** to also emit the ingress-fix routes, instead of adding a new phase. Rejected: both phases' function signatures and responsibilities are treated as frozen/stable contracts elsewhere in the codebase (Phase41 is the general route/vlan setup; Phase50 is strictly the egress CR + verification). Adding Phase51 as a distinct, clearly-scoped phase keeps each phase's blast radius and testability narrow, consistent with the existing Phase00–Phase42/50 numbering convention.

## Open questions for F5 product engineering

- Should the CNE controller / pseudo-CNI program pod-over-vxlan reachability for *ingress* automatically as part of enabling transparent egress? Existing F5 documentation states that virtual-server traffic reaches pod IPs "through the VXLAN/GENEVE tunnels" in general, but on GKE host-device specifically, `route-cluster-pods` points at the Cilium gateway, not at the vxlan — suggesting the ingress side of this contract is not automatically vxlan-aware today. If the controller could own this instead, Phase51 (and its live-discovery dependency) could potentially be removed.
- Is a node's `vxlan100` VTEP address deterministically derivable from any input already available to `gkebnkctl` (cluster/node/pod-CIDR identity), or is live discovery the only correct approach given the current pseudo-CNI IPAM implementation? Confirming this either validates Phase51's discovery-based design or opens a path to a simpler, fully-offline-renderable route.
