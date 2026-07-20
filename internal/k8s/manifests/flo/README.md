# flo/

Placeholder — Task C copies `reference/recon/flo-manifest.yaml` here
(~3 MB, 23 CRDs) and applies it via **server-side** dynamic SSA (annotation
size mandates SSA — see `docs/SPEC-go-tracer.md` §3 Phase36 and §9.8).
Slimming the manifest is a documented follow-up, not tracer scope.

This file exists only so `go:embed all:flo` has at least one file to embed
before Task C lands the real manifest.
