# gkebnkctl

A single Go binary that provisions a GKE cluster and installs **F5 BIG-IP
Next for Kubernetes (BNK)** with the **host-device** dataplane and working
transparent egress — pure GCP Go SDK + an in-process Kubernetes client, no
`gcloud`/`kubectl`/`helm` shellouts, Application Default Credentials only.
Sibling of [`awsbnkctl`](https://github.com/) (same phase-script /
`cluster.yaml` shape), scoped to GCP.

This binary is a Go port of a bash-first recipe **proven live 2026-07-20**
on cluster `bnk-hostdev` — the first-ever BNK host-device dataplane on GKE.
Full path: pod → CSRC vxlan (port 5789) → TMM egress virtual → SNAT automap
→ Cloud NAT → internet, verified via `tmctl clientside.pkts_in` climbing and
`curl https://1.1.1.1/cdn-cgi/trace` returning an external NAT IP. The
original bash recipe is preserved, untouched, under
[`reference/bash-seed/`](reference/bash-seed/) as the provenance record; see
[`reference/TRIAL-NOTES.md`](reference/TRIAL-NOTES.md) for the full
live-trial write-up.

For the phase-by-phase architecture (what each phase creates, the down
asymmetry, state/idempotency strategy, testing strategy), see
**[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)**. For the authoritative
build contract (schema, phase inventory, SDK call detail), see
**[`docs/SPEC-go-tracer.md`](docs/SPEC-go-tracer.md)**.

## The GKE host-device contract

Everything this binary does exists because of a specific, live-discovered
requirement. See [`reference/TRIAL-NOTES.md`](reference/TRIAL-NOTES.md) for
the full write-up; short version, the 7 items:

1. Node pools use `--additional-node-network` + `--additional-pod-network`
   with multi-networking enabled at cluster-create time — GKE then
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

## Install

```sh
go install github.com/JLCode-tech/gkebnkctl/cmd/gkebnkctl@latest
```

Or build from a clone:

```sh
git clone https://github.com/JLCode-tech/gkebnkctl.git
cd gkebnkctl
go build -o bin/gkebnkctl ./cmd/gkebnkctl
```

Requires Go 1.25+ (matches `go.mod`) and Application Default Credentials
for GCP (`gcloud auth application-default login`, or a service-account key
via `GOOGLE_APPLICATION_CREDENTIALS`) for any command other than `validate`
or `--dry-run`.

## Usage

```sh
cp examples/host-device/cluster.yaml my-cluster.yaml   # edit project/zone/name/CIDRs

gkebnkctl validate my-cluster.yaml       # parse + validate — no GCP calls
gkebnkctl up   -f my-cluster.yaml        # provision + install (phases 00-50)
gkebnkctl up   -f my-cluster.yaml --dry-run   # print the full plan, zero mutations

gkebnkctl down -f my-cluster.yaml        # tear down (cluster + main VPC only)
gkebnkctl down -f my-cluster.yaml --dry-run   # print the destroy plan, zero mutations
```

Every phase is idempotent and safely re-runnable after a partial failure —
`up` re-`Get`s each named resource and skips it if it already exists (see
[`docs/ARCHITECTURE.md` "State & idempotency"](docs/ARCHITECTURE.md#state--idempotency)).
`gkebnkctl up` caches Phase25's discovered iface/MAC/PCI/self-IP values into
`.gkebnkctl/<cluster-name>/state.env` — delete that directory and re-run
`up` if you ever need to force a fresh discovery pass; it is a cache, not
the record of truth.

### `--dry-run` and `GKEBNKCTL_SKIP_AUTH`

`--dry-run` makes **zero** GCP/Kubernetes mutations on either `up` or
`down` — every phase prints what it would do instead. The one exception is
Phase00's ADC + API-enabled read on `up --dry-run`, which is itself
skippable:

```sh
GKEBNKCTL_SKIP_AUTH=1 gkebnkctl up -f my-cluster.yaml --dry-run
```

`GKEBNKCTL_SKIP_AUTH=1` skips every GCP/Kubernetes credential check
entirely, so the full phased plan (through phase 50) prints without
Application Default Credentials, a GCP project, or network access — this is
the offline/CI dry-run path (see `.github/workflows/ci.yml`'s
`validate-dryrun` job). It is rejected outside `--dry-run`: a live run needs
real clients, or every phase would nil-pointer-deref on its first GCP call.

### Egress verification (opt-in)

Phase50 can run a best-effort verification pass — a `tmctl
clientside.pkts_in` delta plus a `curl https://1.1.1.1/cdn-cgi/trace`
through the `egress-client` test pod — mirroring the original bash
recipe's `bin/40-egress.sh`. It's off by default (build-first tracer
milestone); enable it with:

```sh
GKEBNKCTL_VERIFY_EGRESS=1 gkebnkctl up -f my-cluster.yaml
```

A verification failure is logged but never fails `up`.

## Config reference

`cluster.yaml` (`apiVersion: gkebnkctl/v1`, `kind: Cluster`) — see
[`examples/host-device/cluster.yaml`](examples/host-device/cluster.yaml)
for the full annotated, proven-live example, and
[`docs/SPEC-go-tracer.md` §2](docs/SPEC-go-tracer.md#2-clusteryaml-intent-schema)
for the complete field/default/validation reference:

| Section | Covers |
|---|---|
| `metadata` | GCP project, zone, cluster name (becomes both the GKE cluster name and the `.gkebnkctl/<name>` state-dir name) |
| `network.main` | this cluster's OWN VPC/subnet + pods/svcs secondary ranges — created **and destroyed** with the cluster |
| `network.external` / `network.internal` | dataplane VPCs, created-if-missing, **never destroyed** by `down` (see below) |
| `cluster` | Kubernetes version, default/tmm/agent node pool machine types + counts |
| `bnk` | manifestVersion, TMM MTU (1460 — distinct from the 8896 wire MTU), JWT path, far-secret source |
| `egress` | test namespace, vxlan port/key/mtu |

Unknown top-level or nested fields are a hard validation error (strict
decode) — `gkebnkctl validate` catches typos before any GCP call.

**Dataplane VPCs are shared infrastructure.** Phase10 creates
`network.external`/`network.internal` only if they don't already exist;
`down` never deletes them, even though `up` may have created them. This
mirrors how the live trial actually ran (the external/internal VPCs were
pre-existing, shared across several trial clusters) — see
[`reference/install/RUNBOOK.md`](reference/install/RUNBOOK.md)'s teardown
note. If you want per-cluster-owned dataplane VPCs instead, give them a
cluster-unique name and delete them by hand after `down`.

**Secrets are never embedded.** `bnk.jwtPath` (your F5 subscription JWT)
and `bnk.farSecret.dockerConfigFile` are runtime inputs referenced by
*path* — read at phase entry, rendered in-memory, applied directly, never
written to disk and never committed. `cluster.yaml`, `.gkebnkctl/`, and any
`*.jwt`/kubeconfig/dockerconfig files are git-ignored (the one exception is
`examples/host-device/license.jwt`, an obvious placeholder so `validate`
and `up --dry-run` have something to point at).

## Testing

```sh
go build ./...
go vet ./...
gofmt -l .              # must print nothing
go tool staticcheck ./...
go test -race ./...
```

### CI

This repository is private and GitHub's Actions budget hard-blocks all
hosted-runner jobs, making GitHub CI unusable. Before pushing, run the local
gate set instead:

```sh
bash hack/ci-local.sh
```

`hack/ci-local.sh` mirrors `.github/workflows/ci.yml` jobs locally — unit tests,
lint, format checks, staticcheck, and the offline `validate-dryrun` gates
(`validate` + `up`/`down --dry-run` with `GKEBNKCTL_SKIP_AUTH=1`). The GitHub
Actions workflow will be restored (with `pull_request`/`push` triggers) when
the Actions budget situation changes or the repo goes public.

## Known open items

Carried over from the trial, unresolved by this binary — see
[`docs/ARCHITECTURE.md` "Known open items"](docs/ARCHITECTURE.md#known-open-items)
for the full list and rationale (DNS-under-capture, the TMM DS update
strategy, the `discover-pod.yaml.tmpl` deviation, the SA1019 staticcheck
exception, the FLO manifest size).
