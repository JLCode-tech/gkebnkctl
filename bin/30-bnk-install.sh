#!/usr/bin/env bash
# bin/30-bnk-install.sh — namespaces, cert-manager, far-secret, multus, FLO,
# host-device NADs, bnk-node-sysctls, CNEInstance (with the mandatory tmm
# env), /var/crash patches, License CR, F5SPKVlans + static routes, TMM pod
# cycle handling. Order matches reference/install/RUNBOOK.md.
#
# Idempotent: every kubectl apply is server-side-declarative already; the
# non-declarative steps (helm install, far-secret copy, deployment patches)
# each have their own existence check.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFESTS="$GKEBNKCTL_ROOT/manifests"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"
# shellcheck source=lib/k8s.sh
source "$GKEBNKCTL_ROOT/lib/k8s.sh"

require_cmd kubectl helm envsubst python3
config::load "${1:-}"
k8s::fetch_credentials
config::load_discovered

RENDERED="$GKEBNKCTL_ROOT/.rendered"
mkdir -p "$RENDERED"

# ---------------------------------------------------------------- namespaces
log::step "namespaces"
kubectl apply -f "$MANIFESTS/01-namespaces.yaml"

# --------------------------------------------------------------- cert-manager
log::step "cert-manager"
helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
helm repo update jetstack >/dev/null
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --set crds.enabled=true \
  --wait --timeout 5m

log::step "issuers"
kubectl apply -f "$MANIFESTS/04-issuers.yaml"
kubectl wait --for=condition=Ready certificate/ext-ca -n cert-manager --timeout=120s

# ----------------------------------------------------------------- far-secret
log::step "far-secret (source=$CFG_BNK_FARSECRET_SOURCE)"
IFS=',' read -r -a far_namespaces <<<"$CFG_BNK_FARSECRET_NAMESPACES"

bnk::copy_far_secret_from_context() {
  local ns="$1"
  kubectl --context "$CFG_BNK_FARSECRET_SOURCECONTEXT" \
    get secret far-secret -n "$CFG_BNK_FARSECRET_SOURCENAMESPACE" -o json \
    | python3 -c '
import sys, json
d = json.load(sys.stdin)
m = d["metadata"]
for k in ("uid", "resourceVersion", "creationTimestamp", "namespace", "annotations", "managedFields"):
    m.pop(k, None)
print(json.dumps(d))
' \
    | kubectl apply -n "$ns" -f -
}

bnk::copy_far_secret_from_dockerconfig() {
  local ns="$1"
  kubectl create secret docker-registry far-secret \
    --from-file=.dockerconfigjson="$CFG_BNK_FARSECRET_DOCKERCONFIGFILE" \
    -n "$ns" --dry-run=client -o yaml | kubectl apply -f -
}

for ns in "${far_namespaces[@]}"; do
  if kubectl get secret far-secret -n "$ns" >/dev/null 2>&1; then
    log::info "far-secret already present in $ns — skipping"
    continue
  fi
  case "$CFG_BNK_FARSECRET_SOURCE" in
    kubecontext)  bnk::copy_far_secret_from_context "$ns" ;;
    dockerconfig) bnk::copy_far_secret_from_dockerconfig "$ns" ;;
    *) log::error "unknown bnk.farSecret.source: $CFG_BNK_FARSECRET_SOURCE (want kubecontext|dockerconfig)"; exit 1 ;;
  esac
done

# --------------------------------------------------------------------- multus
log::step "multus (SA+ClusterRole+CRB+ConfigMap+NAD CRD, then DS — DS alone fails)"
kubectl apply -f "$MANIFESTS/02-multus-rbac.yaml"
kubectl apply -f "$MANIFESTS/02-multus-crd.yaml"
kubectl apply -f "$MANIFESTS/02-multus-configmap.yaml"
kubectl apply -f "$MANIFESTS/02-multus-ds.yaml"

# ----------------------------------------------------------- host-device NADs
log::step "host-device NADs"
common::render_template "$MANIFESTS/03-nads-hostdevice.yaml.tmpl" "$RENDERED/03-nads-hostdevice.yaml"
kubectl apply -f "$RENDERED/03-nads-hostdevice.yaml"

# ------------------------------------------------------------------------ FLO
log::step "FLO operator (server-side apply — client-side exceeds CRD annotation limit)"
kubectl apply --server-side -f "$GKEBNKCTL_ROOT/reference/recon/flo-manifest.yaml"

bnk::wait_for_crd() {
  local crd="$1" timeout="${2:-180}"
  log::info "waiting for CRD $crd to establish (up to ${timeout}s)"
  if ! kubectl wait --for=condition=Established "crd/$crd" --timeout="${timeout}s" 2>/dev/null; then
    log::error "CRD $crd did not establish in time — check FLO operator pods: kubectl get pods -n f5-bnk"
    exit 1
  fi
}
bnk::wait_for_crd cneinstances.k8s.f5.com
bnk::wait_for_crd f5spkvlans.k8s.f5net.com
bnk::wait_for_crd f5spkstaticroutes.k8s.f5net.com
bnk::wait_for_crd f5spkegresses.k8s.f5net.com

# --------------------------------------------------------------- bnk-node-sysctls
log::step "bnk-node-sysctls DS"
kubectl apply -f "$MANIFESTS/07-node-sysctls.json"

# ------------------------------------------------------------------ CNEInstance
log::step "CNEInstance"
common::render_template "$MANIFESTS/05-cneinstance.yaml.tmpl" "$RENDERED/05-cneinstance.yaml"
kubectl apply -f "$RENDERED/05-cneinstance.yaml"

# ------------------------------------------------------ /var/crash emptyDir patches
# freshly-pulled f5-cne-controller / f5-crdconversion image digests carry a
# crashagent that hard-dies without /var/crash and s6-tears-down the main
# service — symptom is CrashLoop / empty VLAN status / absent license logs
# (reference/TRIAL-NOTES.md item 5). Container name is looked up dynamically
# rather than assumed, since it isn't guaranteed to match the Deployment name.
bnk::patch_var_crash() {
  local deploy="$1" ns="f5-bnk"
  if ! kubectl get deployment "$deploy" -n "$ns" >/dev/null 2>&1; then
    log::warn "$deploy not found in $ns yet — skipping /var/crash patch (FLO may still be reconciling); re-run 30-bnk-install.sh once it exists"
    return
  fi
  if kubectl get deployment "$deploy" -n "$ns" -o jsonpath='{.spec.template.spec.volumes[*].name}' | grep -qw var-crash; then
    log::info "$deploy already has /var/crash volume — skipping"
    return
  fi
  local container
  container="$(kubectl get deployment "$deploy" -n "$ns" -o jsonpath='{.spec.template.spec.containers[0].name}')"
  log::step "patching $deploy (container=$container) with /var/crash emptyDir"
  kubectl patch deployment "$deploy" -n "$ns" --type=strategic -p "$(cat <<JSON
{"spec":{"template":{"spec":{
  "volumes":[{"name":"var-crash","emptyDir":{}}],
  "containers":[{"name":"${container}","volumeMounts":[{"name":"var-crash","mountPath":"/var/crash"}]}]
}}}}
JSON
)"
}
bnk::patch_var_crash f5-cne-controller
bnk::patch_var_crash f5-crdconversion

# ------------------------------------------------------------------- License CR
log::step "License CR"
if [[ ! -f "$CFG_BNK_JWTPATH" ]]; then
  log::error "JWT not found at $CFG_BNK_JWTPATH (cluster.yaml bnk.jwtPath)"
  exit 1
fi
export BNK_JWT_CONTENT
BNK_JWT_CONTENT="$(cat "$CFG_BNK_JWTPATH")"
common::render_template "$MANIFESTS/09-license.yaml.tmpl" "$RENDERED/09-license.yaml"
kubectl apply -f "$RENDERED/09-license.yaml"
unset BNK_JWT_CONTENT
# strip the rendered copy immediately — it's the only place the JWT content
# ever touches disk outside the user's own $CFG_BNK_JWTPATH.
rm -f "$RENDERED/09-license.yaml"

# ------------------------------------------------------- F5SPKVlans + routes
log::step "F5SPKVlans + static routes"
export EXT_UNDERLAY_DEST EXT_UNDERLAY_PREFIXLEN MAIN_PODS_DEST MAIN_PODS_PREFIXLEN MAIN_SVCS_DEST MAIN_SVCS_PREFIXLEN
EXT_UNDERLAY_DEST="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-addr "$CFG_NETWORK_EXTERNAL_UNDERLAYCIDR")"
EXT_UNDERLAY_PREFIXLEN="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-prefixlen "$CFG_NETWORK_EXTERNAL_UNDERLAYCIDR")"
MAIN_PODS_DEST="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-addr "$CFG_NETWORK_MAIN_PODSCIDR")"
MAIN_PODS_PREFIXLEN="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-prefixlen "$CFG_NETWORK_MAIN_PODSCIDR")"
MAIN_SVCS_DEST="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-addr "$CFG_NETWORK_MAIN_SVCSCIDR")"
MAIN_SVCS_PREFIXLEN="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" cidr-prefixlen "$CFG_NETWORK_MAIN_SVCSCIDR")"
common::render_template "$MANIFESTS/06-vlans-routes.yaml.tmpl" "$RENDERED/06-vlans-routes.yaml"
kubectl apply -f "$RENDERED/06-vlans-routes.yaml"

# ---------------------------------------------------------- TMM pod cycle handling
# host-device DS rolling updates deadlock: a new TMM pod can't bind the NICs
# while the old pod still holds them, so the rollout hangs forever unless the
# old pod is deleted. Best-effort: give the rollout a window to settle
# naturally (initial install has no "old" pod), then intervene only if we see
# the classic old-Terminating + new-Pending pair on the same node.
log::step "checking for TMM pod NIC-handoff deadlock"
bnk::cycle_tmm_pod_if_stuck() {
  local ns="f5-bnk"
  for _ in $(seq 1 6); do
    local pods
    pods="$(kubectl get pods -n "$ns" -l app=f5-tmm -o json 2>/dev/null || echo '{"items":[]}')"
    local terminating pending
    terminating="$(echo "$pods" | python3 -c 'import json,sys; d=json.load(sys.stdin); print("\n".join(p["metadata"]["name"] for p in d["items"] if p["metadata"].get("deletionTimestamp")))')"
    pending="$(echo "$pods" | python3 -c 'import json,sys; d=json.load(sys.stdin); print("\n".join(p["metadata"]["name"] for p in d["items"] if p["status"].get("phase")=="Pending" and not p["metadata"].get("deletionTimestamp")))')"
    if [[ -n "$terminating" && -n "$pending" ]]; then
      log::warn "TMM pod NIC-handoff deadlock detected — deleting old pod(s): $terminating"
      for p in $terminating; do
        kubectl delete pod "$p" -n "$ns" --grace-period=0 --force >/dev/null 2>&1 || true
      done
      sleep 10
      continue
    fi
    return 0
  done
  log::warn "TMM pod state still unsettled after retries — check manually: kubectl get pods -n f5-bnk -l app=f5-tmm"
}
bnk::cycle_tmm_pod_if_stuck

log::step "30-bnk-install.sh done — check: kubectl get cneinstance,f5spkvlan,pods -n f5-bnk"
