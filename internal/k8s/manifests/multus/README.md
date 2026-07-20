# multus/

Placeholder — Task C moves the four Multus bundle files here verbatim from
`reference/bash-seed/manifests/`: `02-multus-rbac.yaml`,
`02-multus-crd.yaml`, `02-multus-configmap.yaml`, `02-multus-ds.yaml`.
Applied via dynamic SSA in that order (rbac -> crd -> configmap -> ds). The
RBAC/CRD/ConfigMap files were AUTHORED, not captured live — only the
DaemonSet was captured; keep that provenance note in the moved copies (see
`docs/SPEC-go-tracer.md` §9.2).

This file exists only so `go:embed all:multus` has at least one file to embed
before Task C lands the real manifests.
