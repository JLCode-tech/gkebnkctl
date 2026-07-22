# Implementation Spec: Port `gkebnkctl` bash seed → single Go binary (`awsbnkctl`-modeled)

> Authored by Architect 2026-07-20 (session: go-tracer arc). Builders implement directly from
> this document. Decisions locked by the user: tracer-bullet milestone (validate/up/down +
> --dry-run only), pure GCP Go SDK + in-process k8s client (no gcloud/kubectl/helm shellouts,
> ADC auth), copy awsbnkctl patterns with no shared module, build-first (real e2e deferred).

**Decomposition verdict: VALID (with two refinements)** — the draft 4-task split is sound; task
(c) is split into two and explicit ordering guards added (see §8). The proven bash recipe is a
clean 1:1 source for phases; the only genuine architectural divergence from `awsbnkctl` is that
**GCP has no universal resource-tag/label discovery** the way AWS does, so the state/idempotency
model must be name-based rather than label-based (see §4).

Branch: **`feat/go-tracer`** only. Module path: **`github.com/JLCode-tech/gkebnkctl`**. Go 1.25+
(match `awsbnkctl` `go.mod`).

---

## 1. Repo layout

Mirror `awsbnkctl` naming, substituting `gcp` for `aws`. The bash seed moves wholesale under
`reference/` (it is already partly there) and stays as the untouched provenance record.

```
gkebnkctl/
├── cmd/gkebnkctl/main.go            # entrypoint → cli.Execute() (copy awsbnkctl main.go verbatim, rename)
├── internal/
│   ├── cli/
│   │   ├── root.go                  # cobra root, persistent flags, .env load, SIGINT ctx (copy awsbnkctl root.go shape)
│   │   ├── lifecycle.go             # up/down runners + phase sequencing + --dry-run + printDownPlan
│   │   └── validate.go              # `validate <path>` (no cloud calls)
│   ├── intent/
│   │   ├── cluster.go               # cluster.yaml schema (gkebnkctl/v1), Load, applyDefaults, validate
│   │   └── cluster_test.go
│   ├── gcp/
│   │   ├── clients.go               # Clients bundle + narrow SDK interfaces + NewClients (ADC) + AttachK8s
│   │   ├── auth.go                  # ADC token source, rest.Config builder from container API + ADC
│   │   ├── state/state.go           # state.env cache (copy awsbnkctl state.go ~verbatim)
│   │   ├── labels/labels.go         # label scheme (where GCP supports labels) + name helpers
│   │   └── phases/                  # Phase00..Phase50 + down counterparts (see §3)
│   │       ├── phase00_preflight.go
│   │       ├── phase10_networks.go
│   │       ├── phase20_cluster.go
│   │       ├── phase25_discover.go
│   │       ├── phase30_bnk_*.go     # split sub-phases (namespaces, certmgr, farsecret, multus, nads, flo, sysctls, cne, varcrash, license, vlans)
│   │       ├── phase50_egress.go
│   │       ├── down.go              # PhaseDownCluster, PhaseDownNetworks (asymmetric — see §3)
│   │       └── *_test.go
│   ├── netcalc/netcalc.go           # port lib/netcalc.py (nth, mac-for-ip, cidr-addr, cidr-prefixlen) → pure Go
│   ├── k8s/
│   │   ├── client.go                # BuildRESTConfig-equivalent (but GKE/ADC, see §5)
│   │   ├── apply.go                 # dynamic SSA apply (copy awsbnkctl apply.go ~verbatim)
│   │   ├── wait.go                  # WaitForCRDEstablished, WaitForCertificateReady, WaitForDeploymentReady, pod-log read
│   │   ├── render/render.go         # text/template renderers → typed Vars structs (copy awsbnkctl render.go shape)
│   │   └── manifests/embed.go       # go:embed of the manifest tree
│   └── version/version.go           # optional
├── examples/
│   └── host-device/cluster.yaml     # the proven bnk-hostdev config (from cluster.yaml.example)
├── reference/
│   ├── bash-seed/                   # ← MOVE bin/ lib/ manifests/ cluster.yaml.example here, UNTOUCHED
│   ├── install/                     # already present (RUNBOOK + captured yaml) — keep
│   ├── recon/                       # already present (flo-manifest.yaml etc.) — keep
│   └── TRIAL-NOTES.md               # already present — keep
├── docs/ARCHITECTURE.md             # phase model + intent schema + contract map (new)
├── .github/workflows/ci.yml         # §7
├── .gitignore                       # extend existing (already covers *.jwt, cluster.yaml, etc.)
└── go.mod / go.sum
```

**Bash-seed disposition:** `git mv bin/ lib/ manifests/ cluster.yaml.example reference/bash-seed/`.
Do **not** edit them — they remain the byte-for-byte provenance of the live-proven recipe.
`reference/recon/flo-manifest.yaml` stays where it is (embedded by the Go binary, see §5).

**Embedded manifests** (`internal/k8s/manifests/`, `go:embed all:...`): port the `manifests/` tree
here, converting `${VAR}` envsubst placeholders to Go `text/template` `{{ .Field }}` placeholders
(per-file mapping in §5). Static (non-templated) files embedded verbatim:

- `static/01-namespaces.yaml`, `static/04-issuers.yaml`, `static/07-node-sysctls.yaml` (convert JSON→YAML or keep JSON — embed handles both)
- `multus/02-multus-{rbac,crd,configmap,ds}.yaml`
- `cert-manager/cert-manager-v1.16.1.yaml` — **NEW**: vendor the static cert-manager install YAML
  (as `awsbnkctl` does) so there is **no helm shellout**. Pin version = the one FLO expects;
  document the pin.
- `flo/flo-manifest.yaml` — copy of `reference/recon/flo-manifest.yaml` (~3 MB, 23 CRDs). Embed and
  apply server-side. Slimming is a documented follow-up, not tracer scope.

Templated files (rendered → dynamic SSA): `nads-hostdevice.yaml.tmpl`, `cneinstance.yaml.tmpl`,
`vlans-routes.yaml.tmpl`, `license-cr.yaml.tmpl`, `egress.yaml.tmpl`,
`agent-route-setter.yaml.tmpl`, plus a new `discover-pod.yaml.tmpl` (§3, Phase25).

---

## 2. `cluster.yaml` intent schema

`apiVersion: gkebnkctl/v1`, `kind: Cluster`. Strict decode (`dec.KnownFields(true)` — unknown
fields are errors, copy `intent.decodeStrict`). Every field below maps to a `CFG_*`/`DERIVED_*`
variable the bash `lib/config.sh` + templates consume; the source-var comments are the bash
equivalent so Builders can cross-check the live-proven values.

```yaml
apiVersion: gkebnkctl/v1        # required, must == "gkebnkctl/v1"
kind: Cluster                   # required, must == "Cluster"

metadata:
  name: bnk-hostdev             # required. becomes GKE cluster name + state dir. regex ^[a-z][a-z0-9-]{0,38}[a-z0-9]$
  project: my-gcp-project       # required. GCP project ID
  zone: us-central1-a           # required. GKE zonal cluster zone; region derived = zone minus last "-a"
  labels: {purpose: bnk-hostdev-demo}   # optional. applied to label-capable resources (GKE cluster resourceLabels)

pattern: host-device            # required, must == "host-device" (only supported value; kept for awsbnkctl parity)

network:
  main:                         # cluster-OWNED VPC (created + destroyed with the cluster)
    vpc: bnkhd-vpc              # required   (CFG_NETWORK_MAIN_VPC)
    subnet: bnkhd-subnet        # required   (CFG_NETWORK_MAIN_SUBNET)
    cidr: 10.60.0.0/24          # required   (CFG_NETWORK_MAIN_CIDR)
    podsRangeName: bnkhd-pods   # required   (CFG_NETWORK_MAIN_PODSRANGENAME) — GKE cluster secondary range
    podsCidr: 10.66.0.0/16      # required   (CFG_NETWORK_MAIN_PODSCIDR)
    svcsRangeName: bnkhd-svcs   # required   (CFG_NETWORK_MAIN_SVCSRANGENAME)
    svcsCidr: 10.67.0.0/20      # required   (CFG_NETWORK_MAIN_SVCSCIDR)
  external:                     # SHARED dataplane VPC (created-if-missing, NEVER destroyed)
    vpc: bnkhd-ext              # required   (CFG_NETWORK_EXTERNAL_VPC)
    subnet: bnkhd-ext           # required   (CFG_NETWORK_EXTERNAL_SUBNET)
    cidr: 10.30.0.0/24          # required   (CFG_NETWORK_EXTERNAL_CIDR); DERIVED_EXT_GW_IP = nth(cidr,1)
    mtu: 8896                   # required   (CFG_NETWORK_EXTERNAL_MTU) — VPC wire MTU (NOT tmm MTU)
    podsRangeName: tmm-ext-pods # required   (CFG_NETWORK_EXTERNAL_PODSRANGENAME) — the /28-slice secondary range
    podsCidr: 10.31.0.0/24      # required   (CFG_NETWORK_EXTERNAL_PODSCIDR)
    underlayCidr: 10.30.0.0/16  # required   (CFG_NETWORK_EXTERNAL_UNDERLAYCIDR) — route-ext-underlay dest
  internal:                     # SHARED dataplane VPC (created-if-missing, NEVER destroyed)
    vpc: bnkhd-int              # required
    subnet: bnkhd-int           # required
    cidr: 10.50.0.0/24          # required   ; DERIVED_INT_GW_IP = nth(cidr,1) — default-egress gateway
    mtu: 8896                   # required
    podsRangeName: tmm-int-pods # required
    podsCidr: 10.51.0.0/24      # required
  allowInternalCidr: 10.0.0.0/8 # required   (CFG_NETWORK_ALLOWINTERNALCIDR) — allow-internal firewall source
  natGateways: true             # default true (CFG_NETWORK_NATGATEWAYS) — create Cloud Router+NAT on ext+int

cluster:
  kubernetesVersion: "1.35"     # default "1.35" (CFG_CLUSTER_KUBERNETESVERSION)
  defaultPool:
    machineType: e2-standard-4  # default e2-standard-4
    nodeCount: 3                # default 3
  tmmPool:
    name: tmm-pool              # default "tmm-pool"
    machineType: e2-standard-8  # default e2-standard-8
    nodeCount: 1                # default 1
    maxPodsPerNode: 8           # default 8   (--additional-pod-network max-pods-per-node)
  agentPool:
    name: agent-pool            # default "agent-pool"
    machineType: e2-standard-4  # default e2-standard-4
    nodeCount: 1                # default 1
    maxPodsPerNode: 8           # default 8

bnk:
  manifestVersion: "2.4.0-3.2882.0"  # required (CFG_BNK_MANIFESTVERSION)
  tmmMtu: "1460"                # default "1460" (CFG_BNK_TMMMTU) — TMM iface MTU; DISTINCT from network mtu 8896
  jwtPath: ./license.jwt        # required. path to F5 subscription JWT. RUNTIME INPUT, gitignored, never embedded
  farSecret:
    source: kubecontext         # required, enum {kubecontext, dockerconfig}
    sourceContext: ""           # required iff source==kubecontext — kube context to copy far-secret FROM
    sourceNamespace: f5-bnk     # default f5-bnk (source ns for the copy)
    dockerConfigFile: ""        # required iff source==dockerconfig. path to docker-config JSON. RUNTIME INPUT, gitignored
    namespaces: [f5-bnk, f5-utils]  # default [f5-bnk, f5-utils] — namespaces to install far-secret into

egress:
  testNamespace: egress-test    # default egress-test
  vxlanPort: 5789               # default 5789
  vxlanKey: 100                 # default 100
  vxlanMtu: 1410                # default 1410

tags: {env: lab, purpose: bnk-hostdev-demo}  # optional. merged into resource labels where GCP supports them
```

**Derived (computed in `applyDefaults`, marked derived, never operator-set):**
- `Region` = `strings.TrimSuffix(zone, "-"+lastSegment)` (bash: `${CFG_METADATA_ZONE%-*}`).
- `ExtGwIP` = `netcalc.Nth(external.cidr, 1)` (e.g. `10.30.0.1`).
- `IntGwIP` = `netcalc.Nth(internal.cidr, 1)` (e.g. `10.50.0.1`).
- Route CIDR splits for VLAN/route rendering: `ExtUnderlayDest/PrefixLen` from
  `external.underlayCidr`; `MainPodsDest/PrefixLen` from `main.podsCidr`; `MainSvcsDest/PrefixLen`
  from `main.svcsCidr`.
- KubeContext-name equivalent is not needed (we build rest.Config directly, §5).

**Validation rules (`validate`)** — mirror `intent.validate`: name regex; project/zone non-empty;
`pattern == "host-device"`; all `network.main/external/internal` CIDRs parse; `bnk.manifestVersion`
non-empty; `bnk.jwtPath` exists+readable at load (shape check only, content read deferred to
License phase); `farSecret.source` enum + conditional-required companion field; CIDR sanity (no
obvious overlaps between main/ext/int primary ranges — warn, don't hard-fail).
`StateDir()` = `.gkebnkctl/<name>`.

**Secrets constraint (verbatim for Builders):** *No secrets in the repo. The license JWT
(`bnk.jwtPath`) and the FAR pull dockerconfig (`bnk.farSecret.dockerConfigFile`) are runtime
inputs referenced by path in cluster.yaml and are gitignored. Read them at phase entry
(`os.ReadFile`), inline into the rendered CR/secret at apply time, and never write the rendered
secret-bearing manifest to disk. This copies `awsbnkctl` Phase23 (JWT read at entry, never
persisted) and the bash `30-bnk-install.sh` License step. In Go, render to an in-memory `[]byte`
and apply directly — never touch disk at all.*

---

## 3. Phase inventory

Signature for every phase (copy `awsbnkctl`):
`func PhaseNN(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error`.
Each: log a `[phase NN]` banner, check idempotency, on `dryRun` print "would…" + set placeholder
state + return without cloud mutation, else mutate + `st.Save()`.

GCP SDKs: `cloud.google.com/go/compute/apiv1` (networks/subnetworks/firewalls/routers/instances),
`cloud.google.com/go/container/apiv1` (GKE `ClusterManagerClient`),
`google.golang.org/api/serviceusage/v1` (API-enabled check), `client-go` (k8s). GCP compute
mutations return an LRO `*compute.Operation` — wait via `op.Wait(ctx)`. GKE mutations return
`*containerpb.Operation` — poll `GetOperation` until `DONE` (helper `waitForGKEOp`).

### Up phases (ordered)

| # | Phase | Creates | SDK call(s) | Idempotency check | Down counterpart |
|---|-------|---------|-------------|-------------------|------------------|
| 00 | `Phase00Preflight` | nothing | ADC: `google.FindDefaultCredentials(ctx, cloud-platform)`; API-enabled: `serviceusage GetService compute.googleapis.com` + `container.googleapis.com`; quota: `compute.RegionsClient.Get` → check CPUS/IN_USE_ADDRESSES headroom (best-effort, warn-only) | n/a (read-only, fail-fast) | none |
| 10 | `Phase10Networks` | main VPC+subnet(+2 secondary ranges)+allow-internal FW; ext VPC+subnet(+1 secondary)+FW; int VPC+subnet(+1 secondary)+FW; Cloud Router+NAT on ext & int | `NetworksClient.Insert/Get`, `SubnetworksClient.Insert/Get` (`SecondaryIpRanges`), `FirewallsClient.Insert/Get`, `RoutersClient.Insert/Get` (`Nats []*RouterNat` auto-allocate+all-subnet-ranges) | `Get(name)` per resource → skip if found (name-based). Router create: **tolerate 403 `compute.routers.create`** → warn "ask project owner", continue (bash `net::create_nat`) | `PhaseDownNetworks`: delete **main VPC/subnet/FW only**; ext/int VPCs **never touched** |
| 20 | `Phase20Cluster` | GKE cluster (DPv2 `ADVANCED_DATAPATH`, multi-networking, ip-alias w/ main pods+svcs ranges, default pool); tmm-pool (`f5-dataplane=true`, 2 additional-node-networks + 2 additional-pod-networks); agent-pool (1 additional-node-network + 1 additional-pod-network); node label `app=f5-tmm` on tmm-pool nodes | `ClusterManagerClient.CreateCluster/GetCluster`, `CreateNodePool/GetNodePool`. Node label via client-go `CoreV1().Nodes().Patch` (after AttachK8s) | `GetCluster` / `GetNodePool(name)` → skip if exists | `PhaseDownCluster`: `DeleteCluster` (node pools die with it) |
| 25 | `Phase25Discover` | nothing persistent (writes discovered.* into state.env). Ephemeral privileged pod per node (deleted after) | `InstancesClient.Get(node)` → `NetworkInterfaces[]` (match by `Network` self-link suffix `/networks/<vpc>`) → `networkIP` + `aliasIpRanges[].{subnetworkRangeName,ipCidrRange}`; derive MAC `42:01:<hex-ip>`; client-go: create privileged pod (hostPID, `nsenter -t1 -m -u -n -i -- sh -c '<dump /sys/class/net>'`), read logs via `CoreV1().Pods().GetLogs`, MAC-match iface/PCI; delete pod | read-only; re-runnable. state keys: `TMM_EXT_IFACE/PCI`, `TMM_INT_IFACE/PCI`, `TMM_EXT_ALIAS_CIDR/INT`, `TMM_EXT_SELFIP/INT` (= `nth(aliasCidr,2)`), `TMM_POD_CIDR/GW`, `AGENT_EXT_IFACE`, `TMM_NODE_NAME`, `AGENT_NODE_NAME` | none (nothing to destroy) |
| 30 | `Phase30Namespaces` | ns `f5-bnk`, `f5-utils`, `egress-test` | dynamic SSA of embedded `static/01-namespaces.yaml` | SSA is declarative-idempotent | (dies with cluster) |
| 31 | `Phase31CertManager` | cert-manager (CRDs + controllers) | dynamic SSA of embedded `cert-manager/cert-manager-v1.16.1.yaml`; `WaitForDeploymentReady(cert-manager, cert-manager-webhook)` | SSA idempotent; wait tolerant | — |
| 32 | `Phase32Issuers` | `selfsigned-cluster-issuer`, Certificate `ext-ca`, `ext-ca-cluster-issuer` | dynamic SSA `static/04-issuers.yaml`; `WaitForCertificateReady(cert-manager/ext-ca, 120s)` | SSA idempotent | — |
| 33 | `Phase33FarSecret` | `far-secret` in each `farSecret.namespaces` | source=kubecontext: build a **second** rest.Config for `sourceContext` (from local kubeconfig via `clientcmd`), `CoreV1().Secrets(srcNs).Get("far-secret")`, strip server-set metadata, `Create/Apply` into targets. source=dockerconfig: read `dockerConfigFile`, build `kubernetes.io/dockerconfigjson` Secret, SSA | `Secrets(ns).Get("far-secret")` → skip | — |
| 34 | `Phase34Multus` | Multus SA/ClusterRole/CRB/ConfigMap/NAD-CRD + thick DaemonSet | dynamic SSA of the 4 embedded `multus/*` files in order | SSA idempotent | — |
| 35 | `Phase35NADs` | `tmm-ext`, `tmm-int` host-device NADs in `f5-bnk` | render `nads-hostdevice.yaml.tmpl` (iface+selfip from state) → dynamic SSA | SSA idempotent | — |
| 36 | `Phase36FLO` | FLO operator + F5 CRDs | **server-side** dynamic SSA of embedded `flo/flo-manifest.yaml` (annotation size → SSA mandatory); `WaitForCRDEstablished` on `cneinstances.k8s.f5.com`, `f5spkvlans.k8s.f5net.com`, `f5spkstaticroutes.k8s.f5net.com`, `f5spkegresses.k8s.f5net.com`, `licenses.k8s.f5net.com` | SSA idempotent; wait tolerant | — |
| 37 | `Phase37NodeSysctls` | `bnk-node-sysctls` DS (core_pattern, nodeSelector `app=f5-tmm`) | dynamic SSA `static/07-node-sysctls.yaml` | SSA idempotent | — |
| 38 | `Phase38CNEInstance` | `CNEInstance/instance` in `f5-bnk` | render `cneinstance.yaml.tmpl` (tmmMtu, manifestVersion, `TMM_EXT_PCI`/`TMM_INT_PCI` from state) → dynamic SSA | SSA idempotent | — |
| 39 | `Phase39VarCrashPatches` | `/var/crash` emptyDir on `f5-cne-controller` + `f5-crdconversion` | `AppsV1().Deployments(f5-bnk).Get` → if no `var-crash` volume, strategic-merge `Patch` (look up container[0] name dynamically) | volume-presence check → skip. Deployment-absent → warn+skip (FLO still reconciling) | — |
| 40 | `Phase40License` | `License/bnk-license` in `f5-utils` (connected mode, raw JWT) | read `bnk.jwtPath`, trim whitespace, render `license-cr.yaml.tmpl` in-memory → dynamic SSA; never write to disk | SSA idempotent | — |
| 41 | `Phase41VlansRoutes` | `F5SPKVlan` external+internal; `F5SPKStaticRoute` ×6 (ext-gw, **int-gw**, ext-underlay, cluster-pods, cluster-svcs, default-egress) | render `vlans-routes.yaml.tmpl` (selfips, gw IPs, route CIDR splits from state+derived) → dynamic SSA | SSA idempotent | — |
| 42 | `Phase42TMMSettle` | nothing | client-go: detect old-`Terminating` + new-`Pending` `app=f5-tmm` pods on same node → `Pods.Delete(grace=0)` the terminating one; retry ≤6×10s (best-effort, never returns error) | reactive; idempotent | — |
| 50 | `Phase50Egress` | `F5SPKEgress/egress-agents` (pseudo-CNI vxlan); `bnk-agent-route-setter` DS; `egress-client` pod; **verification** | render `egress.yaml.tmpl` + `agent-route-setter.yaml.tmpl` (agent iface, ext gw, pods CIDR from state) → dynamic SSA; `WaitForPodReady(egress-client)`; verification via client-go **exec** (`tmctl` pkts_in delta + `curl` trace) — **best-effort, gated, skippable** | SSA idempotent; verification never blocks | — |

### Down phases (deliberately asymmetric)

Unlike `awsbnkctl`'s full reverse-order per-phase teardown, gkebnkctl teardown is intentionally
minimal (bash `90-teardown.sh`): the k8s objects die with the cluster, and the **shared dataplane
VPCs are never deleted**. `runPhasedDown` therefore runs only:

1. `PhaseDownCluster` — confirm gate (type `destroy`, or `--yes`), `DeleteCluster`, wait DONE.
2. `PhaseDownNetworks` — delete **main** VPC allow-internal FW, then main subnet, then main VPC
   (in that order — FW/subnet before VPC). `network.external`/`network.internal`
   VPCs/subnets/FWs/routers/NAT are **never referenced**. Clear `discovered.*` state keys.

`down --dry-run` prints this plan from state (copy `printDownPlan` shape) and makes zero calls.
Emit a loud one-liner: *"shared dataplane VPCs `<ext>`/`<int>` are NEVER deleted by teardown."*

### GKE host-device contract → coverage map (all 7 items must appear declaratively)

| Contract item (TRIAL-NOTES) | Where it lives in the Go port |
|---|---|
| 1. `--additional-node-network` + `--additional-pod-network` + multi-networking → GKE auto-assigns per-node alias /28 | Phase20 `NodePool.NetworkConfig.AdditionalNodeNetworkConfigs` + `AdditionalPodNetworkConfigs`; cluster `DatapathProvider=ADVANCED_DATAPATH` + multi-networking enabled + `IPAllocationPolicy` secondary ranges |
| 2. host-device NADs w/ static IPAM self-IP; iface names MAC-discovered | Phase25 (compute `aliasIpRanges` + MAC `42:01:<hex-ip>` match via ephemeral pod) → state; Phase35 `nads-hostdevice.yaml.tmpl` (`.ExtIface/.IntIface`, `.ExtSelfIP/.IntSelfIP`) |
| 3. CNEInstance `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE` + `PCIDEVICE_INTEL_COM_EXT/INT=<pci>` | Phase38 `cneinstance.yaml.tmpl` `spec.advanced.tmm.env` (literal `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE`; `.ExtPCI/.IntPCI` from Phase25 state) |
| 4. `bnk-node-sysctls` DS (core_pattern) | Phase37 embedded `static/07-node-sysctls.yaml` (nodeSelector `app=f5-tmm`) |
| 5. `/var/crash` emptyDir on `f5-crdconversion` **and** `f5-cne-controller` | Phase39 strategic-merge patch (both deployments) |
| 6. License CR (`k8s.f5net.com/v1`, ns `f5-utils`, `spec.jwt`, connected) | Phase40 `license-cr.yaml.tmpl` |
| 7. F5SPKStaticRoutes incl. **int-gw `10.50.0.1/32`**; agent-node route-setter DS | Phase41 `vlans-routes.yaml.tmpl` (`route-int-gw` present); Phase50 `agent-route-setter.yaml.tmpl` DS |
| (a) FLO CRDs need server-side apply (annotation size) | Phase36 `--server-side` dynamic SSA |
| (b) TMM DS `OnDelete` / NIC-handoff deadlock | Phase42 reactive cycle (matches bash). Note: setting DS `updateStrategy: OnDelete` is CNEInstance-owned; keep reactive for tracer, flag proactive as follow-up |

---

## 4. State strategy

**Key architecture flag:** GCP does **not** provide the universal resource-tagging + tag-filter
discovery that `awsbnkctl` leans on. VPC networks, subnetworks, and firewall rules are **not**
labelable in a way you can filter on the way AWS tags are. Therefore:

- **Idempotency + "source of truth" is deterministic naming**, not label-discovery. Every resource
  name is derived directly from `cluster.yaml`, so the idempotency check is `Get(<name>)` →
  skip-if-exists (exactly what the bash `*::*_exists` guards do). This is **more** robust than
  tags here, because the name is a pure function of the intent.
- **Labels are applied where GCP supports them** (GKE cluster `resourceLabels`, node pools,
  compute instances) from `metadata.labels` + `tags:` — for cost attribution / inventory only,
  **not** as the rebuild key. `internal/gcp/labels` provides `Required(name)` + `Merge(...)`
  returning `map[string]string`.
- **`state.env` local cache** (`.gkebnkctl/<name>/state.env`, copy `awsbnkctl/internal/aws/state`
  ~verbatim, `KEY=VALUE`, atomic write, `MarkReadOnly` for dry-run). It caches the **discovered**
  values — iface/MAC/PCI/alias-CIDR/self-IP/pod-GW — which are the only state **not** derivable
  from names.
- **Rebuildability:** because names are deterministic, a lost `state.env` is fully recoverable by
  re-running `up` (every phase re-`Get`s and skips existing resources) and re-running
  `Phase25Discover`. Document: "delete `state.env` and re-run `up`; it is a cache, not the record
  of truth — the GCP resources named in `cluster.yaml` are."

---

## 5. Kubernetes access design (in-process, no gcloud/kubectl)

**rest.Config construction (`internal/gcp/auth.go`), after the cluster is RUNNING:**
1. `ClusterManagerClient.GetCluster(name)` → `cluster.Endpoint` +
   `cluster.MasterAuth.ClusterCaCertificate` (base64 DER, decode to `CAData`).
2. ADC token source: `google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")`.
3. Build `*rest.Config{ Host: "https://"+endpoint, TLSClientConfig: rest.TLSClientConfig{CAData: caPEM}, WrapTransport: <oauth2 transport wrapping the ADC token source> }`.
   Using `WrapTransport` (not a static `BearerToken`) so tokens **auto-refresh** across the long
   CNEInstance/License waits. This replaces `k8s.sh k8s::fetch_credentials` entirely — no
   kubeconfig file, no exec plugin.

`Clients.AttachK8s(ctx, clients)` builds `kubernetes.Interface`, `dynamic.Interface`,
discovery-backed `RESTMapper` from that config (copy `awsbnkctl` `AttachK8s`). Call it after
Phase20 (cluster RUNNING) and before any k8s phase.

**Apply mechanism:** copy `awsbnkctl/internal/k8s/apply.go` verbatim — dynamic client +
deferred-discovery RESTMapper + SSA `Patch(ApplyPatchType)`, field-manager `gkebnkctl`, `--force`
toggle. F5 CRs and NADs are not in the typed scheme → dynamic/unstructured is mandatory anyway.

**Template vs typed-struct decision: keep everything as YAML templates rendered from typed `Vars`
structs** (Go `text/template`, copy `render.go` shape), applied via dynamic SSA. Rationale:
(a) the F5 CRDs are out-of-scheme so typed Go structs would mean hand-writing `unstructured` maps;
(b) the live-proven manifests are the ground truth and templates keep them auditable against
`reference/`; (c) golden-file tests over rendered output give strong coverage (§7). Static
manifests stay embedded verbatim, no templating.

**Per-file `${VAR}` → `{{ .Field }}` mapping (Builder reference):**
- `nads-hostdevice`: `${TMM_EXT_IFACE}`→`.ExtIface`, `${TMM_EXT_SELFIP}`→`.ExtSelfIP`, `${TMM_INT_IFACE}`→`.IntIface`, `${TMM_INT_SELFIP}`→`.IntSelfIP`.
- `cneinstance`: `${CFG_BNK_TMMMTU}`→`.TmmMtu`, `${CFG_BNK_MANIFESTVERSION}`→`.ManifestVersion`, `${TMM_EXT_PCI}`→`.ExtPCI`, `${TMM_INT_PCI}`→`.IntPCI`.
- `vlans-routes`: `.TmmMtu`, `.ExtSelfIP`, `.IntSelfIP`, `.ExtGwIP`, `.IntGwIP`, `.VxlanPort`, `.ExtUnderlayDest/.ExtUnderlayPrefixLen`, `.MainPodsDest/.MainPodsPrefixLen`, `.MainSvcsDest/.MainSvcsPrefixLen`, `.TmmPodGW`.
- `license-cr`: `${LICENSE_JWT}`→`.JWT` (in-memory only).
- `egress`: `.TestNamespace`, `.VxlanKey`, `.VxlanMtu`, `.AgentExtIface`, `.VxlanPort`, `.AgentPoolName`.
- `agent-route-setter`: `.AgentPoolName`, `.ExtPodsCidr`, `.ExtGwIP`, `.AgentExtIface`.

**Wait/retry conventions** (`internal/k8s/wait.go`, `wait.PollUntilContextTimeout`, 5s interval):
`WaitForCRDEstablished(name, 3m)`; `WaitForCertificateReady(ns/name, 120s)`;
`WaitForDeploymentReady(ns/name, 5m)`; `WaitForPodReady(ns/name, 120s)`. License `Active` and
CNEInstance `Ready` polls (up to ~20m) read `.status.conditions` via dynamic client — these are
**best-effort log steps** in the tracer milestone (build-first; not a hard e2e gate).

---

## 6. Dry-run design

`up --dry-run` (copy `awsbnkctl` mechanics exactly): `state.MarkReadOnly()` after Load; each
phase, when `dryRun`, prints its planned resource graph and returns without any cloud mutation.
The **only** cloud/auth touch permitted is Phase00's ADC + API-enabled read. Expected output shape:

```
→ dry-run: printing plan, no GCP mutations will be made
[phase 00] preflight: ADC ok (project my-gcp-project), compute+container APIs enabled
[phase 10] would create VPC bnkhd-vpc (10.60.0.0/24) + subnet bnkhd-subnet [pods bnkhd-pods=10.66.0.0/16, svcs bnkhd-svcs=10.67.0.0/20] + fw bnkhd-vpc-allow-internal
[phase 10] would create SHARED VPC bnkhd-ext (mtu 8896)…  (skip if exists)
[phase 10] would create Cloud Router+NAT bnkhd-ext-router / bnkhd-ext-nat
[phase 20] would create GKE cluster bnk-hostdev (DPv2+multinet, default e2-standard-4×3) + tmm-pool (e2-standard-8×1, +2 node-nets +2 pod-nets) + agent-pool
[phase 25] would discover tmm/agent NIC iface/MAC/PCI + alias /28 slices (needs live cluster)
[phase 30-41] would apply: namespaces, cert-manager, issuers, far-secret(×2 ns), multus, NADs(tmm-ext,tmm-int), FLO(server-side), node-sysctls, CNEInstance, /var/crash patches(×2), License, F5SPKVlan(×2), F5SPKStaticRoute(×6 incl route-int-gw)
[phase 50] would apply F5SPKEgress + agent-route-setter DS + egress-client
→ dry-run complete
```

Phases that depend on discovered state (25/35/38/41/50) print "needs live cluster" and set dry-run
placeholder state so downstream render calls don't nil-panic (e.g.
`st.Set("TMM_EXT_IFACE","dry-run-eth1")`).

`down --dry-run` → `printDownPlan` from state (cluster + main VPC only), zero calls.

---

## 7. Testing strategy

**Fake seams for GCP clients (the crux).** The GCP SDK exposes concrete `*compute.NetworksClient`
etc., not interfaces — so, exactly as `awsbnkctl` does with `EC2API`, define **narrow interfaces
in `internal/gcp/phases`** covering only the methods used, and have `Clients` hold
interface-typed fields. Example:

```go
type NetworksAPI interface {
    Insert(ctx, *computepb.InsertNetworkRequest, ...gax.CallOption) (*compute.Operation, error)
    Get(ctx, *computepb.GetNetworkRequest, ...gax.CallOption) (*computepb.Network, error)
    Delete(ctx, *computepb.DeleteNetworkRequest, ...gax.CallOption) (*compute.Operation, error)
}
// + SubnetworksAPI, FirewallsAPI, RoutersAPI, InstancesAPI, GKEAPI (ClusterManager subset)
```

Real clients satisfy these directly. Tests inject hand-rolled fakes (record calls, return canned
`*computepb.*` + fake ops). The LRO `*compute.Operation.Wait` is the one awkward spot — wrap
network mutations behind a tiny `insertAndWait(op)` helper so fakes can return an already-done op
(recommended; matches how `awsbnkctl` keeps waiters out of the mockable surface).

**k8s fakes:** `client-go` `dynamicfake` + `k8sfake` exactly as `awsbnkctl/phase20_nads_test.go`.
Known limitation: `dynamicfake` doesn't support SSA `ApplyPatchType`, so test **render
correctness** in `render` golden tests and test **dry-run + down** paths in phase tests (copy the
awsbnkctl split verbatim).

**Golden-file tests (`internal/k8s/render`):** for every template, render with a fixed `Vars`
struct and compare to `testdata/*.golden.yaml`. Assert the load-bearing contract fields:
`TMM_MAPRES_ADDL_VETHS_ON_DP: "TRUE"`, both `PCIDEVICE_INTEL_COM_*`, the `route-int-gw`
StaticRoute, `spec.jwt` in License, both self-IPs in the VLANs. Golden files double as a
regression guard on the live-proven recipe.

**Per-package unit tests:**
- `intent`: valid load, strict-unknown-field rejection, each validation error, defaults
  application, derived values (region/gw IPs), pattern must == host-device.
- `netcalc`: `nth`, `mac-for-ip` (`10.31.0.49`→`42:01:0a:1f:00:31`), `cidr-addr`,
  `cidr-prefixlen` — port the Python docstring examples as test cases.
- `gcp/phases`: idempotency (Get-returns-existing → skip, no Insert), dry-run (no mutation,
  placeholder state), 403-router tolerance, down asymmetry (PhaseDownNetworks never references
  ext/int VPC names — assert via fake call log).
- `gcp/labels`, `gcp/state`: copy awsbnkctl state tests.

**CI (`.github/workflows/ci.yml`, copy awsbnkctl minus docker/kind/testcontainers tiers):**
- `test` job (ubuntu+macos): `go vet ./...`, `gofmt -l` (fail on diff), `staticcheck ./...`,
  `go test -race ./...`, `go build ./...`.
- `windows-build`: `go build ./...`.
- `validate-dryrun` gate: build binary, run `gkebnkctl validate examples/host-device/cluster.yaml`
  (must exit 0) and `gkebnkctl up -f examples/host-device/cluster.yaml --dry-run` with a
  `GKEBNKCTL_SKIP_AUTH=1` test hook that stubs Phase00 so the full dry-run plan prints and exits 0.
- `security-audit`: gosec + govulncheck + gitleaks (gitleaks doubly important given the repo's
  history of scrubbed live secrets).

---

## 8. Builder task decomposition

Five tasks. **Ordering is strictly A → B → C → D → E** (C and D could partly parallelize but run
serial to keep `Clients`/state contracts and `embed.go` append-only).

**Task A — Scaffold: module, CLI, intent, state, netcalc, dry-run runner.**
Scope: `go.mod`; `cmd/gkebnkctl/main.go`; `internal/cli/{root,lifecycle,validate}.go` (up/down/
validate wired, phases stubbed to no-op that print `[phase NN] (stub)`); `internal/intent/
cluster.go`+tests; `internal/gcp/state` (copy awsbnkctl); `internal/gcp/labels`;
`internal/netcalc`+tests; `internal/gcp/clients.go` with the narrow interfaces + `NewClients`
(ADC) + `AttachK8s` stub; `internal/gcp/auth.go` (rest.Config builder); `internal/k8s/
{apply,wait,render,client}.go` + `manifests/embed.go` skeleton; move bash seed to
`reference/bash-seed/`; `examples/host-device/cluster.yaml`.
Boundaries: **no real GCP mutations**; phases are stubs; `validate` + `up --dry-run` +
`down --dry-run` must work end-to-end against stubs.
Acceptance: `go build ./...`, `go test ./...`, `go vet`, `gofmt` clean;
`gkebnkctl validate examples/host-device/cluster.yaml` passes; `up --dry-run` prints the full
stubbed plan and exits 0; strict-unknown-field rejection proven by test.

**Task B — GCP infra phases: networks, cluster, discover.**
Scope: `phase00_preflight.go`, `phase10_networks.go`, `phase20_cluster.go`, `phase25_discover.go`
+ `down.go` (`PhaseDownCluster`, `PhaseDownNetworks`) + tests with fakes. Wire into
`lifecycle.go` replacing the stubs for 00/10/20/25 and both down phases.
Boundaries: no k8s object apply beyond the node-label patch and the Phase25 discovery pod;
**PhaseDownNetworks must not reference ext/int VPC names** (assert in test).
Acceptance: fake-driven tests prove idempotent Get-skip, dry-run no-mutation, 403-router
tolerance, down asymmetry; `up --dry-run` still exits 0.

**Task C — BNK install phases (30–42).**
Scope: `phase30_*`…`phase42_*.go` + embedded manifests (static + templated) + `render` Vars
structs + golden tests. cert-manager & FLO embedded-YAML SSA (no helm), far-secret copy (both
sources), NADs/CNEInstance/License/VLAN templates, `/var/crash` patches, TMM settle. Wire into
`lifecycle.go`.
Boundaries: all secret material in-memory only (JWT, dockerconfig); server-side apply for FLO;
no live-cluster verification.
Acceptance: golden tests assert the 7-item contract fields render correctly; phase tests cover
dry-run + idempotency; `up --dry-run` exits 0 through phase 42.

**Task D — Egress phase (50).**
Scope: `phase50_egress.go` + `egress.yaml.tmpl` + `agent-route-setter.yaml.tmpl` + golden tests;
verification (tmctl/curl via client-go exec) implemented but **gated behind a flag,
default-skip for tracer** (build-first). Wire into `lifecycle.go`.
Boundaries: verification failure never fails `up` (best-effort, matches bash).
Acceptance: golden tests; dry-run exits 0; verify path compiles and is unit-tested with a fake
exec seam.

**Task E — Tests hardening + CI + docs.**
Scope: `.github/workflows/ci.yml`; `docs/ARCHITECTURE.md` (phase model + contract map + intent
schema); `.gitignore` extension; README rewrite (Go usage); fill test gaps;
`validate-dryrun` CI gate + `GKEBNKCTL_SKIP_AUTH` hook.
Acceptance: full CI green (vet/fmt/staticcheck/test/build/windows/security/validate-dryrun);
docs describe every phase + the down asymmetry + the "shared VPCs never deleted" rule.

**Ordering risk / merge notes:** A defines the `Clients` interface surface + `state` contract
that B/C/D all import — freeze it in A and avoid touching it after. C and D both add to `render`
and `manifests/embed.go` — run serial C→D, or pre-declare the full embed directive + render
struct stubs in A to make the seam append-only.

---

## 9. Open questions / risks (each with a recommended default so Builders never block)

1. **GCP SDK exact field names for GKE multi-networking** (`AdditionalNodeNetworkConfigs` /
   `AdditionalPodNetworkConfigs`, `DatapathProvider`, multi-networking enable flag). These live in
   `container/apiv1/containerpb` and have shifted across SDK versions. **Default:** pin the
   `cloud.google.com/go/container` version in `go.mod`, and in Task B verify the field names
   against that pinned version's types; map 1:1 from the gcloud flags in
   `reference/bash-seed/bin/20-cluster.sh`. If a field is absent in the pinned SDK, bump the SDK
   (don't shell out to gcloud).
2. **Multus bundle provenance** (`02-multus-{rbac,crd,configmap}` were AUTHORED, not captured live
   — only the DS was captured). **Default:** embed the four bundle files verbatim from
   `reference/bash-seed/manifests/`, keep the `AUTHORED, NOT CAPTURED LIVE` comment in the
   embedded copies, apply in order (rbac→crd→configmap→ds). They worked together live; do not
   re-derive.
3. **`readinessindicatorfile` unset** in the Multus config. **Default:** leave it unset (matches
   the live-proven config). Document in `docs/ARCHITECTURE.md`.
4. **DNS-under-capture open item** (svc-CIDR pod→`10.67.0.10` UDP/53 times out under TMM capture).
   **Explicitly OUT of tracer scope.** **Default:** do nothing; carry the note into
   `docs/ARCHITECTURE.md` "Known open items" (workaround: pod `dnsConfig` → `8.8.8.8`, proven).
   Do not add a phase.
5. **Cloud NAT `compute.routers.create` 403** (project-owner-gated in the trial). **Default:**
   Phase10 tolerates the 403 exactly like `net::create_nat` — warn with the copy-paste owner
   command, continue non-fatally. Unit-test the tolerance path.
6. **In-cluster iface/PCI discovery without SSH.** **Default:** `Phase25Discover` creates a
   short-lived privileged pod via client-go (`nsenter -t1` dump of `/sys/class/net`), reads logs
   via `CoreV1().Pods().GetLogs`, deletes it — this is the k8s API, not SSH. Compute API supplies
   the IP→MAC + alias-CIDR halves. (Pure compute-API-only discovery can't read on-node PCI/iface
   names, so the ephemeral pod is required — flag accepted.)
7. **cert-manager & FLO delivered as embedded static YAML** rather than helm. **Default:** vendor
   `cert-manager-v1.16.1.yaml` (pin to FLO's expected version) and reuse
   `reference/recon/flo-manifest.yaml`; apply both via dynamic SSA (FLO server-side). This is a
   genuine behavior change from the bash `helm upgrade --install` — validate the cert-manager
   version pin matches what FLO's webhooks expect.
8. **FLO manifest is ~3 MB / 23 CRDs embedded into the binary.** **Default:** embed as-is for
   tracer (correctness over binary size). Slimming is a documented follow-up.
9. **TMM DS `OnDelete` vs reactive cycle.** **Default:** keep the reactive `Phase42TMMSettle`
   (matches live). Setting `updateStrategy: OnDelete` fights CNEInstance's declarative ownership —
   follow-up requiring more certainty.

---

## Notes for Builder (non-obvious)

- The two MTUs are **different and must not be conflated**: `network.{external,internal}.mtu =
  8896` (VPC jumbo wire MTU) vs `bnk.tmmMtu = 1460` (TMM interface / F5SPKVlan MTU).
- `TMM_*_SELFIP = netcalc.Nth(aliasCidr, 2)` — the **2nd** address of the GKE-assigned /28, not
  `.1`. Gateway IPs are `Nth(cidr, 1)`.
- MAC derivation is `42:01:` + hex of each IP octet (`10.31.0.49` → `42:01:0a:1f:00:31`).
  Discovery matches this against on-node `/sys/class/net/*/address`.
- License CR namespace is `f5-utils` (not `f5-bnk`), `spec.jwt` is the **raw** trimmed token (not
  base64, not nested) — live-proven, don't "fix" it.
- `route-int-gw` (`10.50.0.1/32`, interface internal) is the item that made egress work where the
  reference cluster failed — golden-test its presence.

## Constraints (verbatim, all Builders)

- No secrets in the repo — license JWT and FAR pull creds are runtime inputs referenced by path
  in cluster.yaml, gitignored (copy how awsbnkctl handles `license.jwt`/`cne_pull_64.json`).
- No Co-Authored-By trailers on commits.
- Work only on branch `feat/go-tracer`.
- Never run gcloud/kubectl against live clusters.
