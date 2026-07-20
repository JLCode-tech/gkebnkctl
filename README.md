# gkebnkctl

A bash-first seed for provisioning a GKE cluster and installing **F5 BIG-IP
Next for Kubernetes (BNK)** with the **host-device** dataplane and working
transparent egress. Sibling of [`awsbnkctl`](https://github.com/) (same
phase-script / `cluster.yaml` shape), scoped to GCP.

No Go yet — this is the proven recipe turned into clean, idempotent,
re-runnable shell + a handful of small Python helpers (config parsing, IPv4
math). A future `gkebnkctl` binary can absorb these phases the way
`awsbnkctl` did for its AWS phases.

**Proven live 2026-07-20** on cluster `bnk-hostdev` (project
`f5-gcs-7056-ptg-spk-dev` initially, moved to a qwiklabs BNY project after a
`compute.routers.create` 403) — first-ever BNK host-device dataplane on GKE.
Full path: pod → CSRC vxlan (port 5789) → TMM egress virtual → SNAT automap
→ Cloud NAT → internet, verified via `tmctl clientside.pkts_in` climbing and
`curl https://1.1.1.1/cdn-cgi/trace` returning an external NAT IP.

## The GKE host-device contract

Everything below exists because of a specific, live-discovered requirement.
See [`reference/TRIAL-NOTES.md`](reference/TRIAL-NOTES.md) for the full
write-up; short version, the 7 items:

1. Node pools use `--additional-node-network` + `--additional-pod-network`
   with `--enable-multi-networking` at cluster-create time — GKE then
   auto-assigns per-node alias `/28` slices from the secondary range. This
   *is* the cloud self-IP assignment on GKE; no manual `gcloud` aliasing.
2. NADs are `host-device` with **static IPAM carrying the self-IP**.
   Interface names are **discovered at runtime**, never hardcoded — GKE 1.35
   nodes use `eth0/eth1/eth2`, 1.34 used `ens4/ens5/ens6`.
3. `CNEInstance.spec.advanced.tmm.env` needs `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE`
   **and** `PCIDEVICE_INTEL_COM_EXT`/`_INT` set to the NIC's PCI address —
   mapres silently skips interfaces without a `PCIDEVICE_*` reference.
4. A `bnk-node-sysctls` DaemonSet forces `core_pattern` on TMM nodes, or the
   TMM postStart core-helper crash-loops on GKE 1.35 COS.
5. `f5-cne-controller` and `f5-crdconversion` need a `/var/crash` `emptyDir`
   patched in — freshly-pulled image digests carry a crashagent that
   hard-dies without it.
6. A `License` CR (connected mode, your F5 subscription JWT) is required
   before any VLAN programming happens at all.
7. `F5SPKStaticRoute`s need the **internal gateway interface route**
   (`int-gw`) in addition to the external one — missing on the pre-existing
   reference cluster, which is why egress never worked there.

## Layout

```
cluster.yaml.example      copy to cluster.yaml and edit
lib/                       config parsing (yaml2env.py), IPv4 math (netcalc.py),
                            gcloud JSON parsing (gcejson.py), kubectl/gcloud
                            helpers (common.sh, k8s.sh)
bin/
  10-networks.sh           VPCs, subnets + secondary ranges, firewall, Cloud NAT
  20-cluster.sh             GKE cluster (DPv2 + multi-net + ip-alias) + node pools
  25-discover.sh             post-provision discovery -> discovered.env
  30-bnk-install.sh          namespaces -> cert-manager -> far-secret -> multus
                              -> FLO -> NADs -> CNEInstance -> license -> VLANs/routes
  40-egress.sh                F5SPKEgress + agent route + verification
  90-teardown.sh              cluster + OWN vpc/subnet/firewall only
manifests/                 templates (envsubst ${VAR}) + a few static copies
reference/                 the proven recipe's raw inputs (do not edit)
  TRIAL-NOTES.md              full contract + narrative
  install/RUNBOOK.md          exact install order used live
  install/*.yaml|json         exact manifests applied (source for templates)
  recon/                     raw exports from a separate, pre-existing GKE
                              reference cluster (flo-manifest.yaml is applied
                              directly by 30-bnk-install.sh — not copied)
```

## Usage

```sh
cp cluster.yaml.example cluster.yaml   # edit project/zone/name/CIDRs
gcloud auth login                      # this seed authors gcloud/kubectl
                                        # commands, it doesn't manage auth

bin/10-networks.sh
bin/20-cluster.sh
bin/25-discover.sh
bin/30-bnk-install.sh
bin/40-egress.sh

# later
bin/90-teardown.sh
```

Every phase script is idempotent — re-run any of them after a partial
failure. `bin/25-discover.sh` writes `discovered.env`
(git-ignored — regenerate it, don't hand-edit it, if node pools are recreated).

`cluster.yaml`, `discovered.env`, `.rendered/`, and any `*.jwt`/kubeconfig
files are git-ignored. Never commit the JWT or dockerconfig content — only
their local paths live in `cluster.yaml`.

## Config reference

`cluster.yaml` (see [`cluster.yaml.example`](cluster.yaml.example) for the
full annotated example):

| Section | Covers |
|---|---|
| `metadata` | project, zone, cluster name |
| `network.main` | this cluster's OWN VPC/subnet + pods/svcs secondary ranges |
| `network.external` / `network.internal` | dataplane VPCs (treated as shared — see below), MTU 8896, pods secondary range |
| `cluster` | Kubernetes version, default/tmm/agent node pool machine types + counts |
| `bnk` | manifestVersion, TMM MTU (1460 — distinct from the 8896 wire MTU), JWT path, far-secret source |
| `egress` | test namespace, vxlan port/key/mtu |

**Dataplane VPCs are shared infrastructure.** `10-networks.sh` creates
`network.external`/`network.internal` only if they don't already exist;
`90-teardown.sh` never deletes them, even though it created them. This
mirrors how the live trial actually ran (`tmm-ext`/`tmm-int` were pre-existing
VPCs shared across several trial clusters in the qwiklabs project) — see
`reference/install/RUNBOOK.md`'s teardown note ("NEVER the shared
tmm-ext/tmm-int VPCs"). If you want per-cluster-owned dataplane VPCs instead,
give them a cluster-unique name and delete them by hand after teardown.

## Known open items

Carried over from the trial, unresolved by this seed:

- **Cluster-service DNS under capture times out** — pod → `10.67.0.0/20`
  (svc CIDR) gets captured into TMM; a hairpin route was added but UDP/53
  still fails. Candidates: CSRC svc-CIDR exclusion, `egress.dnsEnabled: true`,
  or pod `dnsConfig` pointing at `8.8.8.8` (this workaround is proven —
  external DNS works today). Not addressed by any phase script here.
- **`dssm-sentinel-1` 1/2** — cosmetic; crashagent `core_pattern` noise,
  `DSSMAvailable=False`. Harmless for the egress demo.
- **TMM DS `OnDelete` update strategy** — `30-bnk-install.sh`'s
  `bnk::cycle_tmm_pod_if_stuck` detects and recovers from the NIC-handoff
  deadlock reactively (deletes the old pod once it sees old-Terminating +
  new-Pending on the same node). Setting the DS to `updateStrategy: OnDelete`
  declaratively would avoid the deadlock proactively but wasn't applied here
  since CNEInstance owns/reconciles that DS and fighting its declarative
  ownership needs more certainty than this seed has.

## Ambiguities flagged during authoring (see task report / commit messages)

A few pieces of the proven recipe were not fully captured in
`reference/install/` or `reference/TRIAL-NOTES.md` and were reconstructed
from general knowledge rather than a live capture. Each is marked inline
with an `AUTHORED, NOT CAPTURED LIVE` or `AMBIGUITY` comment:

- `manifests/02-multus-rbac.yaml`, `02-multus-crd.yaml`, `02-multus-configmap.yaml`
  — the Multus SA/ClusterRole/CRB/ConfigMap/NAD-CRD bundle; only the
  DaemonSet was captured live.

Resolved during verification (no longer ambiguous):

- `manifests/09-license.yaml.tmpl` — **live-proven** 2026-07-20 on
  bnk-hostdev (reached License status Active/eval in ~60s). The `License`
  CRD's `spec` carries the raw JWT string directly under `jwt` (not
  base64, not a nested `licenseToken` field), plus the three `teem*Url`
  fields:
  ```yaml
  spec:
    operationMode: connected
    jwt: ${LICENSE_JWT}
    teemCertUrl: https://product.apis.f5.com/ee/v1
    teemEntitlementUrl: https://product-s.apis.f5.com/ee/v1
    teemInitialConfigUrl: https://product-s.apis.f5.com/ee/v1
  ```
- `network.main.podsCidr`/`svcsCidr` defaults (`10.66.0.0/16` /
  `10.67.0.0/20`) — **confirmed correct**: this is what was actually
  applied and proven live. The earlier `10.64/16` / `10.65/20` mentioned in
  `TRIAL-NOTES.md`'s "Built so far" narrative section was an earlier plan,
  superseded mid-session.
