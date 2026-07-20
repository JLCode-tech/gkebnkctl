#!/usr/bin/env bash
# bin/40-egress.sh — F5SPKEgress (pseudo-CNI vxlan), agent-node persistent
# route, egress-client test pod, and a pkts_in-delta + curl verification loop.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFESTS="$GKEBNKCTL_ROOT/manifests"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"
# shellcheck source=lib/k8s.sh
source "$GKEBNKCTL_ROOT/lib/k8s.sh"

require_cmd kubectl envsubst python3
config::load "${1:-}"
k8s::fetch_credentials
config::load_discovered

RENDERED="$GKEBNKCTL_ROOT/.rendered"
mkdir -p "$RENDERED"

log::step "F5SPKEgress + egress-client test pod"
common::render_template "$MANIFESTS/08-egress.yaml.tmpl" "$RENDERED/08-egress.yaml"
kubectl apply -f "$RENDERED/08-egress.yaml"

log::step "agent-node persistent route ($CFG_NETWORK_EXTERNAL_PODSCIDR via $DERIVED_EXT_GW_IP)"
common::render_template "$MANIFESTS/07-agent-route-setter.yaml.tmpl" "$RENDERED/07-agent-route-setter.yaml"
kubectl apply -f "$RENDERED/07-agent-route-setter.yaml"

log::step "waiting for egress-client pod to be Ready"
kubectl wait --for=condition=Ready pod/egress-client -n "$CFG_EGRESS_TESTNAMESPACE" --timeout=120s

# ---------------------------------------------------------------- verification
# Best-effort: TMM container/pod naming can vary by manifestVersion, so
# failures here are reported clearly but don't necessarily mean the data
# path is broken — cross-check with `kubectl exec` manually if this fails.
log::step "verification: tmctl clientside.pkts_in delta"
egress::tmm_pod() {
  kubectl get pods -n f5-bnk -l app=f5-tmm -o jsonpath='{.items[0].metadata.name}'
}
egress::pkts_in() {
  local pod="$1"
  kubectl exec -n f5-bnk "$pod" -c tmm -- tmctl -a clientside.pkts_in 2>/dev/null \
    | awk '{sum+=$NF} END{print sum+0}'
}

tmm_pod="$(egress::tmm_pod || true)"
if [[ -z "$tmm_pod" ]]; then
  log::warn "no TMM pod found (app=f5-tmm) — skipping pkts_in check"
else
  before="$(egress::pkts_in "$tmm_pod" || echo 0)"
  log::info "clientside.pkts_in before: $before"

  log::step "verification: curl through egress-client -> https://1.1.1.1/cdn-cgi/trace"
  trace_out="$(kubectl exec -n "$CFG_EGRESS_TESTNAMESPACE" egress-client -- \
    curl -sk --max-time 10 https://1.1.1.1/cdn-cgi/trace || true)"
  echo "$trace_out" | sed 's/^/  /' >&2

  after="$(egress::pkts_in "$tmm_pod" || echo 0)"
  log::info "clientside.pkts_in after: $after"

  egress_ip="$(echo "$trace_out" | grep '^ip=' | cut -d= -f2 || true)"

  if [[ -n "$egress_ip" && "$after" -gt "$before" ]]; then
    log::step "PASS — egress verified: pkts_in $before -> $after, egress source IP $egress_ip"
  else
    log::error "FAIL — pkts_in delta=$((after - before)), trace ip='${egress_ip:-<none>}'"
    log::error "check: kubectl get f5spkegress,f5spkvlan -n f5-bnk; kubectl logs -n f5-bnk $tmm_pod -c tmm"
    exit 1
  fi
fi

log::step "40-egress.sh done"
