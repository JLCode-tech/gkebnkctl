# templates/

Go `text/template` `{{ .Field }}` sources converted from the bash seed's
`${VAR}` envsubst templates (`reference/bash-seed/manifests/*.yaml.tmpl`):
`nads-hostdevice.yaml.tmpl` (Task C, Phase35), `cneinstance.yaml.tmpl`
(Task C, Phase38), `vlans-routes.yaml.tmpl` (Task C, Phase41),
`license-cr.yaml.tmpl` (Task C, Phase40), `egress.yaml.tmpl` (Task D,
Phase50), `agent-route-setter.yaml.tmpl` (Task D, Phase50). Rendered via
`internal/k8s/render` against typed `Vars` structs, then applied via
dynamic SSA. See `docs/SPEC-go-tracer.md` §5 for the per-file `${VAR}` ->
`.Field` mapping table.

`ingress-vxlan-routes.yaml.tmpl` (Phase51, added after the tracer
milestone — no bash-seed equivalent) renders a variable-length list of
`F5SPKStaticRoute` objects from Phase51-discovered per-agent-node data,
unlike every template above which renders a fixed document set. See
`internal/gcp/phases/phase51_ingress_vxlan.go`'s doc comment for the
asymmetric-routing fix this closes.

Deviation from docs/SPEC-go-tracer.md §1/§5: the spec also lists a
`discover-pod.yaml.tmpl` for Phase25's ephemeral discovery pod. Task B
implemented that pod as a typed `corev1.Pod` Go struct
(`buildDiscoveryPod` in `internal/gcp/phases/phase25_discover.go`) instead
— there is no F5/CRD-specific unstructured content in that pod spec, so a
template added no value over the typed builder. No `discover-pod.yaml.tmpl`
exists or is needed.
