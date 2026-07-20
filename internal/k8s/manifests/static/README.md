# static/

Placeholder — Task C moves `01-namespaces.yaml`, `04-issuers.yaml`, and
`07-node-sysctls.json` here verbatim from `reference/bash-seed/manifests/`
(see `docs/SPEC-go-tracer.md` §1). These are applied via dynamic SSA with no
templating (declarative-idempotent).

This file exists only so `go:embed all:static` has at least one file to
embed before Task C lands the real manifests.
