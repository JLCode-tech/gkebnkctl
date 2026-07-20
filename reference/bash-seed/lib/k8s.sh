#!/usr/bin/env bash
# lib/k8s.sh — kubectl context helper + the "node debug pod" pattern used by
# 25-discover.sh (NIC/MAC/PCI discovery) and 40-egress.sh (route-setter DS
# templating shares the same nsenter idiom). Sourced by phase scripts.

if [[ -n "${GKEBNKCTL_K8S_SH_LOADED:-}" ]]; then
  return 0 2>/dev/null || exit 0
fi
GKEBNKCTL_K8S_SH_LOADED=1

# k8s::context_name — the kube context gcloud creates for this cluster.
k8s::context_name() {
  echo "gke_${CFG_METADATA_PROJECT}_${CFG_METADATA_ZONE}_${CFG_METADATA_NAME}"
}

# k8s::fetch_credentials — idempotent `gcloud ... get-credentials` + switch
# kubectl to this cluster's context.
k8s::fetch_credentials() {
  gcloud container clusters get-credentials "$CFG_METADATA_NAME" \
    --zone "$CFG_METADATA_ZONE" --project "$CFG_METADATA_PROJECT"
  kubectl config use-context "$(k8s::context_name)" >/dev/null
}

# k8s::exec_on_node <node-name> <shell-command> — runs <shell-command> in
# the host's namespaces on <node-name> via a short-lived privileged pod
# (nsenter -t 1 -m -u -n -i --), same pattern as manifests/07-node-sysctls.json.
# Prints the command's stdout, then deletes the pod. Used because kubectl
# debug node/<name> needs an interactive TTY that doesn't script cleanly.
k8s::exec_on_node() {
  local node="$1" cmd="$2"
  local pod="gkebnkctl-discover-$$-${RANDOM}"

  kubectl run "$pod" \
    --image=busybox:1.36 \
    --restart=Never \
    --overrides="$(k8s::_debug_pod_overrides "$node" "$cmd")" \
    >/dev/null

  local phase
  for _ in $(seq 1 30); do
    phase="$(kubectl get pod "$pod" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    [[ "$phase" == "Succeeded" || "$phase" == "Failed" ]] && break
    sleep 2
  done

  if [[ "$phase" != "Succeeded" ]]; then
    kubectl logs "$pod" >&2 || true
    kubectl delete pod "$pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    log::error "k8s::exec_on_node: command on $node did not succeed (phase=$phase)"
    return 1
  fi

  kubectl logs "$pod" 2>/dev/null
  kubectl delete pod "$pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}

k8s::_debug_pod_overrides() {
  local node="$1" cmd="$2"
  cat <<JSON
{
  "apiVersion": "v1",
  "spec": {
    "hostPID": true,
    "nodeName": "${node}",
    "restartPolicy": "Never",
    "tolerations": [{"operator": "Exists"}],
    "containers": [{
      "name": "discover",
      "image": "busybox:1.36",
      "command": ["nsenter", "-t", "1", "-m", "-u", "-n", "-i", "--", "sh", "-c", $(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$cmd")]
      ,"securityContext": {"privileged": true}
    }]
  }
}
JSON
}
