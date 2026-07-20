# cert-manager/

Placeholder — Task C vendors the static cert-manager v1.16.1 install YAML
here (`cert-manager-v1.16.1.yaml`), matching the version pinned by
`EmbeddedCertManagerVersion` in awsbnkctl and the version FLO's webhooks
expect (see `docs/SPEC-go-tracer.md` §9.7). Applied via dynamic SSA — no helm
shellout.

This file exists only so `go:embed all:cert-manager` has at least one file to
embed before Task C lands the real manifest.
