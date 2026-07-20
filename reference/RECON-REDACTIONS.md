# Redactions applied to reference/recon/flo-manifest.yaml

This recon export (a `kubectl get ... -o yaml` capture from the pre-existing
FLO reference cluster) contained real secret material. It was redacted
before commit and scrubbed from git history with `git filter-repo`
(pattern-based rules, no secret bytes ever re-embedded anywhere in this
repo or its history).

Redacted fields:

- `Secret f5-utils/external-otelsvr-secret` — `data.ca.crt`, `data.tls.crt`,
  `data.tls.key` (a real TLS private key). This secret is self-managed by
  the FLO/CNE operator for its otel endpoint; telemetry is disabled in
  `manifests/05-cneinstance.yaml.tmpl` (`metricSubsystem.enabled: false`,
  `loggingSubsystem.enabled: false`) so it isn't on this seed's critical
  path. If a future revision enables telemetry, let cert-manager /
  the operator regenerate this secret fresh rather than restoring these
  values.
- `ConfigMap f5-utils/cpcl-config-cm` — `data.jwt`, a live F5 eval-license
  JWT. This field is itself marked deprecated in the same file's own CRD
  schema comment ("Deprecated: This configuration has been replaced by a
  dedicated License custom resource and is now unused") — superseded by
  `manifests/09-license.yaml.tmpl`, which is what this seed actually
  applies.

If you need the original values (e.g. to compare against a fresh capture),
they are not recoverable from this repository or its history — re-capture
from a live reference cluster you have access to.
