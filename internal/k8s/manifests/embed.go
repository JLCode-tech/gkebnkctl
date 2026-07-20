// Package manifests exposes the embedded k8s manifest directories as an
// fs.FS. Phases (Task C/D) use this FS to read manifest files without
// requiring them to be on disk at runtime — baked into the binary at
// compile time.
//
// Layout (see docs/SPEC-go-tracer.md §1 "Embedded manifests"):
//
//	static/         — 01-namespaces, 04-issuers, 07-node-sysctls (verbatim, Task C)
//	multus/         — Multus CNI v4.2.4 RBAC/CRD/ConfigMap/DaemonSet (verbatim, Task C)
//	cert-manager/   — cert-manager v1.16.1 static install YAML, vendored (Task C)
//	flo/            — FLO operator manifest (~3 MB, 23 CRDs), copied from
//	                  reference/recon/flo-manifest.yaml (Task C)
//	templates/      — text/template sources for dynamic SSA: nads-hostdevice,
//	                  cneinstance, vlans-routes, license-cr (Task C), egress,
//	                  agent-route-setter (Task D). No discover-pod.yaml.tmpl —
//	                  Phase25's ephemeral pod is a typed corev1.Pod struct
//	                  instead (see templates/README.md)
//
// Task A pre-declares the full go:embed directive (all five directories)
// with placeholder README.md content so Tasks C/D are append-only — they add
// real files under these directories without touching this file.
package manifests

import "embed"

// FS is the embedded manifest filesystem.
//
//go:embed all:static all:multus all:cert-manager all:flo all:templates
var FS embed.FS
