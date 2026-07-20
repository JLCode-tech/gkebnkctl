#!/usr/bin/env bash
# bin/20-cluster.sh — GKE cluster (DPv2 + multi-networking + ip-alias), then
# tmm-pool / agent-pool with --additional-node-network + --additional-pod-network.
#
# Idempotent: `gcloud container clusters describe` / `node-pools describe`
# gate every create call.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"
# shellcheck source=lib/k8s.sh
source "$GKEBNKCTL_ROOT/lib/k8s.sh"

require_cmd gcloud kubectl python3
config::load "${1:-}"
common::confirm_gcloud_project "$CFG_METADATA_PROJECT"

cluster::exists() {
  common::gcloud_resource_exists container clusters describe "$CFG_METADATA_NAME" \
    --zone="$CFG_METADATA_ZONE" --project="$CFG_METADATA_PROJECT"
}

cluster::node_pool_exists() {
  common::gcloud_resource_exists container node-pools describe "$1" \
    --cluster="$CFG_METADATA_NAME" --zone="$CFG_METADATA_ZONE" --project="$CFG_METADATA_PROJECT"
}

if cluster::exists; then
  log::info "cluster $CFG_METADATA_NAME already exists — skipping create"
else
  log::step "creating GKE cluster $CFG_METADATA_NAME (DPv2 + multi-networking + ip-alias)"
  # NOTE: this seed assumes --enable-multi-networking alone is sufficient to
  # attach node-scoped secondary NICs via --additional-node-network on the
  # node pools below (proven live on the reference cluster, GKE 1.31/1.34/1.35
  # in the us-central1 zones used). It does NOT create Network / GKENetworkParamSet
  # CRDs — this cluster does not use GKE's own pod-multi-networking feature,
  # only node-level extra NICs consumed by host-device NADs. Flag if your GKE
  # version rejects --additional-node-network without those CRDs registered.
  gcloud container clusters create "$CFG_METADATA_NAME" \
    --project="$CFG_METADATA_PROJECT" \
    --zone="$CFG_METADATA_ZONE" \
    --cluster-version="$CFG_CLUSTER_KUBERNETESVERSION" \
    --network="$CFG_NETWORK_MAIN_VPC" \
    --subnetwork="$CFG_NETWORK_MAIN_SUBNET" \
    --cluster-secondary-range-name="$CFG_NETWORK_MAIN_PODSRANGENAME" \
    --services-secondary-range-name="$CFG_NETWORK_MAIN_SVCSRANGENAME" \
    --enable-ip-alias \
    --enable-dataplane-v2 \
    --enable-multi-networking \
    --machine-type="$CFG_CLUSTER_DEFAULTPOOL_MACHINETYPE" \
    --num-nodes="$CFG_CLUSTER_DEFAULTPOOL_NODECOUNT"
fi

k8s::fetch_credentials

if cluster::node_pool_exists "$CFG_CLUSTER_TMMPOOL_NAME"; then
  log::info "node pool $CFG_CLUSTER_TMMPOOL_NAME already exists — skipping create"
else
  log::step "creating node pool $CFG_CLUSTER_TMMPOOL_NAME ($CFG_CLUSTER_TMMPOOL_MACHINETYPE x $CFG_CLUSTER_TMMPOOL_NODECOUNT)"
  gcloud container node-pools create "$CFG_CLUSTER_TMMPOOL_NAME" \
    --cluster="$CFG_METADATA_NAME" \
    --zone="$CFG_METADATA_ZONE" \
    --project="$CFG_METADATA_PROJECT" \
    --machine-type="$CFG_CLUSTER_TMMPOOL_MACHINETYPE" \
    --num-nodes="$CFG_CLUSTER_TMMPOOL_NODECOUNT" \
    --node-labels="f5-dataplane=true" \
    --additional-node-network "network=$CFG_NETWORK_EXTERNAL_VPC,subnetwork=$CFG_NETWORK_EXTERNAL_SUBNET" \
    --additional-node-network "network=$CFG_NETWORK_INTERNAL_VPC,subnetwork=$CFG_NETWORK_INTERNAL_SUBNET" \
    --additional-pod-network "subnetwork=$CFG_NETWORK_EXTERNAL_SUBNET,pod-ipv4-range=$CFG_NETWORK_EXTERNAL_PODSRANGENAME,max-pods-per-node=$CFG_CLUSTER_TMMPOOL_MAXPODSPERNODE" \
    --additional-pod-network "subnetwork=$CFG_NETWORK_INTERNAL_SUBNET,pod-ipv4-range=$CFG_NETWORK_INTERNAL_PODSRANGENAME,max-pods-per-node=$CFG_CLUSTER_TMMPOOL_MAXPODSPERNODE"
fi

if cluster::node_pool_exists "$CFG_CLUSTER_AGENTPOOL_NAME"; then
  log::info "node pool $CFG_CLUSTER_AGENTPOOL_NAME already exists — skipping create"
else
  log::step "creating node pool $CFG_CLUSTER_AGENTPOOL_NAME ($CFG_CLUSTER_AGENTPOOL_MACHINETYPE x $CFG_CLUSTER_AGENTPOOL_NODECOUNT)"
  gcloud container node-pools create "$CFG_CLUSTER_AGENTPOOL_NAME" \
    --cluster="$CFG_METADATA_NAME" \
    --zone="$CFG_METADATA_ZONE" \
    --project="$CFG_METADATA_PROJECT" \
    --machine-type="$CFG_CLUSTER_AGENTPOOL_MACHINETYPE" \
    --num-nodes="$CFG_CLUSTER_AGENTPOOL_NODECOUNT" \
    --node-labels="f5-dataplane=true" \
    --additional-node-network "network=$CFG_NETWORK_EXTERNAL_VPC,subnetwork=$CFG_NETWORK_EXTERNAL_SUBNET" \
    --additional-pod-network "subnetwork=$CFG_NETWORK_EXTERNAL_SUBNET,pod-ipv4-range=$CFG_NETWORK_EXTERNAL_PODSRANGENAME,max-pods-per-node=$CFG_CLUSTER_AGENTPOOL_MAXPODSPERNODE"
fi

log::step "labeling $CFG_CLUSTER_TMMPOOL_NAME nodes app=f5-tmm (bnk-node-sysctls DS + CNEInstance scheduling depend on this)"
tmm_nodes="$(kubectl get nodes -l "cloud.google.com/gke-nodepool=$CFG_CLUSTER_TMMPOOL_NAME" -o jsonpath='{.items[*].metadata.name}')"
if [[ -z "$tmm_nodes" ]]; then
  log::error "no nodes found for pool $CFG_CLUSTER_TMMPOOL_NAME — node pool may still be provisioning"
  exit 1
fi
for n in $tmm_nodes; do
  kubectl label node "$n" app=f5-tmm --overwrite
done

log::step "20-cluster.sh done — nodes: $tmm_nodes"
