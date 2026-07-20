# templates/

Placeholder — Task C/D convert the bash seed's `${VAR}` envsubst templates
(`reference/bash-seed/manifests/*.yaml.tmpl`) into Go `text/template`
`{{ .Field }}` sources here: `nads-hostdevice.yaml.tmpl`,
`cneinstance.yaml.tmpl`, `vlans-routes.yaml.tmpl`, `license-cr.yaml.tmpl`,
`egress.yaml.tmpl`, `agent-route-setter.yaml.tmpl`, plus a new
`discover-pod.yaml.tmpl` (Phase25 ephemeral discovery pod). Rendered via
`internal/k8s/render` against typed `Vars` structs, then applied via dynamic
SSA. See `docs/SPEC-go-tracer.md` §5 for the per-file `${VAR}` -> `.Field`
mapping table.

This file exists only so `go:embed all:templates` has at least one file to
embed before Task C/D land the real templates.
