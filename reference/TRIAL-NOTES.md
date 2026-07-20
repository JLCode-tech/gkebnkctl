---
name: project_gkebnkctl_hostdev_trial
description: gkebnkctl seed — fresh GKE cluster bnk-hostdev in f5-gcs-7056-ptg-spk-dev validating BNK host-device dataplane (egress fix)
metadata: 
  node_type: memory
  type: project
  originSessionId: 5f6f3721-f416-4358-bc7a-c4c96a4802a6
---

Goal (2026-07-20): validate BNK **host-device** dataplane on GKE (installer-side alias self-IP
assignment, no cne-controller cloud contract) on a fresh cluster → seed for **gkebnkctl** (sibling
of awsbnkctl). Follows the ipvlan-egress disproof — see [[project_bnk_egress_demo_cluster]].

## Project / naming correction (user)
`f5-gcs-7056-ptg-spk-dev` clusters `spk` + `spk2-ipvlan` (us-west1) are **BNK clusters, NOT SPK**
— SPK is legacy naming. Both use ipvlan NADs (whereabouts, ens5/ens6); team actively iterating
(tmm-ext-ui NAD added ~2026-07-17). Nobody has tried host-device on GKE before this trial.
Do NOT touch their clusters.

## Built so far (us-central1-a, project f5-gcs-7056-ptg-spk-dev)
- VPCs: bnkhd-vpc (10.60.0.0/24 + pods 10.64/16 + svcs 10.65/20), bnkhd-ext (10.30.0.0/24 +
  secondary "vips" 10.31.0.0/24, MTU 8896), bnkhd-int (10.50.0.0/24 + "vips" 10.51.0.0/24, MTU
  8896); allow-internal 10.0.0.0/8 firewall each. **Cloud NAT BLOCKED**: account lacks
  compute.routers.create — project owner must create bnkhd-{ext,int}-router + NAT. Until then
  egress validated against in-VPC targets.
- Cluster `bnk-hostdev`: DPv2 + multi-networking, default-pool 3×e2-standard-4; tmm-pool
  1×e2-standard-8 (NICs: bnkhd-ext + bnkhd-int, f5-dataplane=true); agent-pool 1×e2-standard-4
  (NIC: bnkhd-ext).

## Install plan (from reference cluster bnk-bny-us-central1 capture, in scratchpad gkebnkctl-recon/)
- FLO v2.25.4 (helm release; replicate via CRD+RBAC+Deployment export — no chart registry needed).
- cert-manager (jetstack helm) + selfsigned-cluster-issuer → Certificate ext-ca (CA, ns
  cert-manager) → ClusterIssuer ext-ca-cluster-issuer.
- NADs tmm-ext/tmm-int as **host-device** (device ens5/ens6) — NOT ipvlan; NO whereabouts needed.
- Multus thick (ghcr snapshot-thick, nodeSelector f5-dataplane=true) — verbatim from reference.
- CNEInstance 2.4.0-3.2882.0, networkAttachments [tmm-ext,tmm-int], demoMode, generic socket
  driver, NO CLOUD_* env (bare-metal-style static path, proven working on reference).
- F5SPKVlans external(1.1, selfip 10.31.0.18/16)/internal(1.2, 10.51.0.18/16) + static routes
  (ext-gw 10.30.0.1/32 interface; 10.30.0.0/24 via 10.30.0.1 for VTEP returns; cluster-pods
  10.64/16; default via 10.50.0.1).
- Installer alias step (THE experiment): alias 10.31.0.16/28 (ext self-IP+VIP room) on tmm node
  nic1, 10.51.0.16/28 on nic2 — gcloud instances network-interfaces update. awsbnkctl template
  comment confirms: controller only cloud-assigns Gateway VIPs, never SelfIPs — installer must.
- far-secret (repo.f5.com pull) — needs user creds or authorized cluster-to-cluster copy
  (classifier blocks credential materialization to disk).

## ✅ RESULT 2026-07-20: BNK HOST-DEVICE EGRESS **WORKS ON GKE** — first ever
Cluster `bnk-hostdev` (qwiklabs BNY project, us-central1-a — venue moved there after spk-dev
403'd cluster-create; own bnkhd-vpc, dataplane NICs on shared tmm-ext/tmm-int subnets).
**Proven: pod → CSRC vxlan100 (port 5789) → TMM egress virtual (pkts_in climbing) → SNAT
automap (internal self-IP) → Cloud NAT → internet. HTTP 301 from 1.1.1.1 in 34ms; egress
source IP = 34.61.234.229 (tmm-int NAT, deterministic); external DNS via 8.8.8.8 through TMM
64ms.** The full ipvlan-impossible path, working.

## THE GKE HOST-DEVICE CONTRACT (all 7 items discovered live — gkebnkctl core recipe)
1. Node pools: `--additional-node-network` (tmm-ext + tmm-int) AND `--additional-pod-network
   subnetwork=X,pod-ipv4-range=X-pods,max-pods-per-node=8` → GKE auto-assigns per-node alias
   /28s from the secondary range (this IS the cloud self-IP assignment — no manual gcloud
   aliasing on GKE-managed ranges). Our slices: tmm 10.31.0.48/28 + 10.51.0.48/28.
2. NADs: `host-device` + **static IPAM carrying the self-IP** (10.31.0.50/28 on eth1,
   10.51.0.50/28 on eth2). **Interface names MUST be discovered** — GKE 1.35 nodes use
   eth0/eth1/eth2 (1.34 reference used ens4/5/6); map by MAC (42:01:<hex-ip>).
3. CNEInstance advanced.tmm.env: `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE` (colleague hand-patched
   their DS — put it declaratively) AND **`PCIDEVICE_INTEL_COM_EXT/INT=<pci>`** (0000:00:05.0 /
   0000:00:06.0 for GCP virtio nic1/nic2) — mapres SKIPS interfaces without a PCIDEVICE_* env
   ref ("Environment variable reference is required"); TMM_MAPRES_IFC_CLAIM_LIST alone does NOT
   work. Success log: `#tcl# interface 1.1 net1` + tmctl interface_stat shows 1.1/1.2.
4. `bnk-node-sysctls` DS (copied from team's BNK cluster in spk-dev): forces
   `core_pattern=/shared/core/core.%e.%p.%t` on app=f5-tmm nodes — else TMM postStart
   (core_helper_init.sh) crash-loops on GKE 1.35 COS.
5. **`/var/crash` emptyDir patch on f5-crdconversion AND f5-cne-controller** — freshly-pulled
   image digests (same tags as 37d-old reference!) have a crashagent that hard-dies without
   /var/crash and s6-tears-down the main service. F5 re-pushed tags — flag to F5. Symptom:
   CrashLoop/empty VLAN status/"license" logs absent.
6. License CR (k8s.f5net.com/v1, ns f5-utils, operationMode connected, jwt = user's own
   subscription JWT from awsbnkctl/license.jwt) → Active/eval in ~60s. Without it:
   "Configs are not allowed since license is not activated" and NO VLAN programming.
7. F5SPKStaticRoutes: ext-gw 10.30.0.1/32 iface external; **int-gw 10.50.0.1/32 iface internal
   (REQUIRED for default-egress gw resolution — missing on reference, egress never worked
   there so nobody noticed)**; ext-underlay 10.30.0.0/16 via 10.30.0.1; cluster-pods
   10.66.0.0/16 via 10.66.3.1; default 0.0.0.0/0 via 10.50.0.1; cluster-svcs 10.67.0.0/20 via
   10.66.3.1. Node side: agent nodes need `10.31.0.0/24 via 10.30.0.1 dev eth1`
   (tmm-route-setter equivalent — added manually, NOT persisted; gkebnkctl must own).
Egress CR (mirrors ref shape): pseudoCNIConfig vxlan create/key 100/mtu 1410/port 5789/
`nodeInterfaceName: eth1`/tmmInterfaceName external; UNIQUE_SNAT_ADDRESS; ns [egress-test].
CSRC node rules: per-pod `from <podIP> lookup 276` + dataplane-range exclusions.

## Open items
- Cluster-service DNS under capture times out (pod→10.67.0.10 captured into TMM; hairpin route
  added but UDP/53 still fails) — candidates: CSRC svc-CIDR exclusion, egress dnsEnabled:true,
  or pod dnsConfig 8.8.8.8 (WORKS today). Demo unaffected (use external DNS).
- dssm-sentinel-1 1/2 (crashagent core_pattern noise) — cosmetic; DSSMAvailable=False.
- TMM DS rolling update deadlocks on host-device (new pod can't take NICs while old holds them)
  — delete old pod first; gkebnkctl should set DS updateStrategy OnDelete or handle.
- agent-node 10.31.0.0/24 route not persisted (needs tmm-route-setter DS clone).
- Multus install needs SA+CRB+CM `multus-daemon-config`+NAD CRD too (DS alone fails: "sa multus
  not found"); FLO CRDs need `--server-side` apply (annotation size).
- **Cluster left RUNNING** (~$0.9/hr: 3×e2-s4 + e2-s8 + e2-s4) in colleague's qwiklabs project.
  Teardown: delete cluster bnk-hostdev + bnkhd-vpc/bnkhd-subnet/bnkhd-vpc-allow-internal
  (shared tmm-ext/tmm-int untouched — only NIC attachments, die with the nodes).
- gkebnkctl distillation (task #8) pending — manifests in scratchpad gkebnkctl-install/ +
  gkebnkctl-recon/ (RUNBOOK.md has the order); author in an isolated worktree/new repo per user.

## Key evidence backing this design
Reference GKE build programs SelfIPs with ZERO cloud env (F5SPKVlan static → "CR config sent to
all grpc endpoints"); fabric probe proved VXLAN→alias-IP + anti-spoof + Cloud NAT all work.
Diagnosis doc: `~/Downloads/gke-bnk-egress-diagnosis.md` (2 addendums).
