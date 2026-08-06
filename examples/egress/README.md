# BNK Transparent Egress & Firewall Demo

> [!NOTE]
> A live, operator-driven demo showing F5 BIG-IP Next for Kubernetes (BNK) **transparent egress**. Watch an application pod's outbound traffic get SNAT-translated to a single BNK-controlled identity with an egress firewall ACL applied, all toggled by applying a single `F5SPKEgress` CR without modifying the pod.

> [!WARNING]
> This requires an **external-only** or **sriov-external** pattern. It does NOT work on `dual-interface` host-device setups due to VXLAN limitations.
>
> **Why `external-only`?** Transparent egress via the pseudo-CNI VXLAN overlay works on the **external-only** and **sriov-external** patterns, where TMM does NOT consume the node's internal NIC (it reaches pods over the CNI). It does **NOT** work on `dual-interface`/host-device on AWS VPC CNI: in that layout the node-side VXLAN VTEP does not converge to a usable capture path and traffic redirection can interfere with ingress handling. Use `examples/external-only/cluster.yaml` (or `cluster.yaml` here) for this demo.

## What's Included
- `cluster.yaml`: External-only BNK cluster configuration.
- `egress-toggle.yaml`: The `F5SPKEgress` CR toggle (BNK ON / OFF).
- `firewall-policy.yaml`: The `F5BigCneAddresslist` and `F5BigFwPolicy` ACL.
- `probe.sh` / `watch.sh`: Scripts injecting an agent's-eye view.
- `workload.yaml`: The captured app pod and an uncaptured control pod.

## Quick Start

### 1. Provision Cluster
```bash
gkebnkctl up --config examples/egress/cluster.yaml
export KUBECONFIG=.gkebnkctl/bnk-egress/kubeconfig
```

### 2. Setup Base Resources
```bash
kubectl apply -f examples/egress/firewall-policy.yaml
kubectl apply -f examples/egress/workload.yaml
kubectl -n bnk-egress-demo rollout status deploy/agent --timeout=120s
```

### 3. Run the Demo
Open two terminals.
**Terminal 1 (Agent View):**
```bash
kubectl exec -it -n bnk-egress-demo deploy/agent -c nginx -- sh /demo/watch.sh
```
**Terminal 2 (Operator Toggle):**
```bash
# Toggle BNK ON
kubectl apply -f examples/egress/egress-toggle.yaml

# Toggle BNK OFF
kubectl delete -f examples/egress/egress-toggle.yaml
```
Watch the IP and access control flip dynamically in Terminal 1.

**The story:** same pod, zero pod changes — one CR flips its egress from a per-node/uncontrolled identity to a single BNK-controlled identity **with policy enforcement**. The `control` pod in the `bnk-egress-control` namespace is never captured, so it's the "unaffected workload" side-by-side beat.

### Optional Forge beats (best story — no terminal)
With this cluster registered in a localhost Forge:
- Toggle from the UI: **cluster `bnk-egress` → Networking → Egress →** delete / re-create `bnk-egress-demo` (Create Resource / Configuration Builder, paste `egress-toggle.yaml`). The pod's `watch.sh` flips ~10–15s later.
- Show **Insights** while ON: the **Traffic Flow** egress lane, the **Gateway Topology** egress node, and the **Policy Gateway Map** (`block-test-target → 1.1.1.1/32`).

---

## ⚠️ One gotcha to rehearse before a real demo

The **SNAT identity flip always works**. The **firewall-block beat** depends on the ACL blob being loaded into TMM — and on BNK 2.3 there is a `blobd` TLS bug where the blob only reaches TMM right **after a TMM pod restart**. If, while BNK is ON, `1.1.1.1` comes back reachable (e.g. HTTP 301) instead of BLOCKED, bounce TMM and wait ~2–3 minutes:

```bash
kubectl delete pod -n f5-cne-system -l app=f5-tmm
```

Then re-check the probe shows `1.1.1.1` BLOCKED. (The egress SNAT flip is unaffected by this.)

---

## Teardown
```bash
kubectl delete -f examples/egress/egress-toggle.yaml
kubectl delete -f examples/egress/workload.yaml
kubectl delete -f examples/egress/firewall-policy.yaml
gkebnkctl down --config examples/egress/cluster.yaml --yes
```

---

## How it works (one paragraph)

The `F5SPKEgress` CR (`snatType: SRC_TRANS_AUTOMAP` + `pseudoCNIConfig.vxlan`) tells the BNK controller to stand up a VXLAN tunnel endpoint on TMM and signal the `f5-spk-csrc` DaemonSet to program the worker node, so outbound traffic from pods in the captured namespace(s) is intercepted on `eth0` and routed through TMM over the overlay. TMM then AUTOMAP-SNATs it to its external self-IP identity (the BNK NAT EIP the internet sees) and applies the `firewallEnforcedPolicy` ACL. With `external-only`, TMM's only data-plane interface is the external ENI and it reaches pods over the CNI — so nothing consumes the node's internal NIC and the overlay works. See `docs/ARCHITECTURE.md` and, for the data-plane rationale, the pattern notes above in this README.
