#!/usr/bin/env bash
# bin/25-discover.sh — post-provision discovery. Writes discovered.env,
# consumed by 30-bnk-install.sh and 40-egress.sh.
#
# GKE 1.35 nodes name secondary NICs eth1/eth2; 1.34 used ens5/ens6 on the
# reference cluster — this script NEVER hardcodes an interface name. It
# matches each NIC by MAC address instead: GCP's virtio driver derives a
# NIC's MAC from its IP as 42:01:<hex-ip> (see reference/TRIAL-NOTES.md
# contract item 2), so we can compute the expected MAC from the IP gcloud
# reports and match it against what's actually present on the node.
#
# Read-only against the cluster/project — this script only ever describes
# and gets, never creates/patches/deletes. The one exception is the
# short-lived discover pod (lib/k8s.sh k8s::exec_on_node), deleted immediately
# after use.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"
# shellcheck source=lib/k8s.sh
source "$GKEBNKCTL_ROOT/lib/k8s.sh"

require_cmd gcloud kubectl python3
config::load "${1:-}"
k8s::fetch_credentials

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

IFACE_DUMP_SCRIPT='
for i in /sys/class/net/*; do
  n=$(basename "$i")
  [ "$n" = "lo" ] && continue
  mac=$(cat "$i/address" 2>/dev/null || echo "")
  pci=""
  if [ -e "$i/device" ]; then
    pci=$(basename "$(readlink -f "$i/device")")
  fi
  echo "IFACE=$n MAC=$mac PCI=$pci"
done
'

# discover::iface_for_mac <dump> <mac> — extract IFACE= for a MAC= line.
discover::field_for_mac() {
  local dump="$1" mac="$2" field="$3"
  echo "$dump" | grep -i "MAC=${mac}" | head -1 | grep -o "${field}=[^ ]*" | cut -d= -f2
}

discover::node_json() {
  local node="$1" out="$2"
  gcloud compute instances describe "$node" \
    --zone="$CFG_METADATA_ZONE" --project="$CFG_METADATA_PROJECT" \
    --format=json >"$out"
}

# --- node names ---
TMM_NODE_NAME="$(kubectl get nodes -l "cloud.google.com/gke-nodepool=$CFG_CLUSTER_TMMPOOL_NAME" \
  -o jsonpath='{.items[0].metadata.name}')"
AGENT_NODE_NAME="$(kubectl get nodes -l "cloud.google.com/gke-nodepool=$CFG_CLUSTER_AGENTPOOL_NAME" \
  -o jsonpath='{.items[0].metadata.name}')"

if [[ -z "$TMM_NODE_NAME" || -z "$AGENT_NODE_NAME" ]]; then
  log::error "could not find nodes for tmm-pool/agent-pool — run bin/20-cluster.sh first"
  exit 1
fi
log::info "tmm node: $TMM_NODE_NAME, agent node: $AGENT_NODE_NAME"

# --- tmm node: ext + int NIC discovery ---
discover::node_json "$TMM_NODE_NAME" "$WORKDIR/tmm-node.json"

TMM_EXT_IP="$(python3 "$GKEBNKCTL_ROOT/lib/gcejson.py" nic-ip "$WORKDIR/tmm-node.json" "$CFG_NETWORK_EXTERNAL_VPC")"
TMM_INT_IP="$(python3 "$GKEBNKCTL_ROOT/lib/gcejson.py" nic-ip "$WORKDIR/tmm-node.json" "$CFG_NETWORK_INTERNAL_VPC")"
TMM_EXT_MAC="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" mac-for-ip "$TMM_EXT_IP")"
TMM_INT_MAC="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" mac-for-ip "$TMM_INT_IP")"

log::step "discovering NIC names / PCI addresses on $TMM_NODE_NAME (node debug pod pattern)"
TMM_IFACE_DUMP="$(k8s::exec_on_node "$TMM_NODE_NAME" "$IFACE_DUMP_SCRIPT")"

TMM_EXT_IFACE="$(discover::field_for_mac "$TMM_IFACE_DUMP" "$TMM_EXT_MAC" IFACE)"
TMM_EXT_PCI="$(discover::field_for_mac "$TMM_IFACE_DUMP" "$TMM_EXT_MAC" PCI)"
TMM_INT_IFACE="$(discover::field_for_mac "$TMM_IFACE_DUMP" "$TMM_INT_MAC" IFACE)"
TMM_INT_PCI="$(discover::field_for_mac "$TMM_IFACE_DUMP" "$TMM_INT_MAC" PCI)"

if [[ -z "$TMM_EXT_IFACE" || -z "$TMM_INT_IFACE" ]]; then
  log::error "MAC match failed — dumping raw interface list for debugging:"
  echo "$TMM_IFACE_DUMP" >&2
  log::error "expected ext MAC=$TMM_EXT_MAC (ip=$TMM_EXT_IP), int MAC=$TMM_INT_MAC (ip=$TMM_INT_IP)"
  exit 1
fi
log::info "tmm ext: iface=$TMM_EXT_IFACE pci=$TMM_EXT_PCI  |  tmm int: iface=$TMM_INT_IFACE pci=$TMM_INT_PCI"

# --- tmm node: GKE-allocated alias /28 slices + chosen self-IPs (base+2) ---
TMM_EXT_ALIAS_CIDR="$(python3 "$GKEBNKCTL_ROOT/lib/gcejson.py" alias-cidr "$WORKDIR/tmm-node.json" "$CFG_NETWORK_EXTERNAL_PODSRANGENAME")"
TMM_INT_ALIAS_CIDR="$(python3 "$GKEBNKCTL_ROOT/lib/gcejson.py" alias-cidr "$WORKDIR/tmm-node.json" "$CFG_NETWORK_INTERNAL_PODSRANGENAME")"
TMM_EXT_SELFIP="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" nth "$TMM_EXT_ALIAS_CIDR" 2)"
TMM_INT_SELFIP="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" nth "$TMM_INT_ALIAS_CIDR" 2)"
log::info "tmm ext alias slice: $TMM_EXT_ALIAS_CIDR -> self-IP $TMM_EXT_SELFIP"
log::info "tmm int alias slice: $TMM_INT_ALIAS_CIDR -> self-IP $TMM_INT_SELFIP"

# --- tmm node: primary pod-CIDR gateway (for route-cluster-pods / route-cluster-svcs) ---
TMM_POD_CIDR="$(kubectl get node "$TMM_NODE_NAME" -o jsonpath='{.spec.podCIDR}')"
TMM_POD_GW="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" nth "$TMM_POD_CIDR" 1)"
log::info "tmm pod CIDR: $TMM_POD_CIDR -> gateway $TMM_POD_GW"

# --- agent node: ext NIC discovery (for the egress route-setter DS) ---
discover::node_json "$AGENT_NODE_NAME" "$WORKDIR/agent-node.json"
AGENT_EXT_IP="$(python3 "$GKEBNKCTL_ROOT/lib/gcejson.py" nic-ip "$WORKDIR/agent-node.json" "$CFG_NETWORK_EXTERNAL_VPC")"
AGENT_EXT_MAC="$(python3 "$GKEBNKCTL_ROOT/lib/netcalc.py" mac-for-ip "$AGENT_EXT_IP")"

log::step "discovering NIC names on $AGENT_NODE_NAME"
AGENT_IFACE_DUMP="$(k8s::exec_on_node "$AGENT_NODE_NAME" "$IFACE_DUMP_SCRIPT")"
AGENT_EXT_IFACE="$(discover::field_for_mac "$AGENT_IFACE_DUMP" "$AGENT_EXT_MAC" IFACE)"

if [[ -z "$AGENT_EXT_IFACE" ]]; then
  log::error "MAC match failed on agent node — dumping raw interface list:"
  echo "$AGENT_IFACE_DUMP" >&2
  exit 1
fi
log::info "agent ext: iface=$AGENT_EXT_IFACE"

# --- write discovered.env ---
OUT="$(config::discovered_env_path)"
cat >"$OUT" <<EOF
# generated by bin/25-discover.sh on $(date -u +%FT%TZ) — do not edit by hand,
# re-run 25-discover.sh instead (e.g. after a node pool recreate).
TMM_NODE_NAME=$TMM_NODE_NAME
AGENT_NODE_NAME=$AGENT_NODE_NAME
TMM_EXT_IFACE=$TMM_EXT_IFACE
TMM_EXT_PCI=$TMM_EXT_PCI
TMM_INT_IFACE=$TMM_INT_IFACE
TMM_INT_PCI=$TMM_INT_PCI
TMM_EXT_ALIAS_CIDR=$TMM_EXT_ALIAS_CIDR
TMM_INT_ALIAS_CIDR=$TMM_INT_ALIAS_CIDR
TMM_EXT_SELFIP=$TMM_EXT_SELFIP
TMM_INT_SELFIP=$TMM_INT_SELFIP
TMM_POD_CIDR=$TMM_POD_CIDR
TMM_POD_GW=$TMM_POD_GW
AGENT_EXT_IFACE=$AGENT_EXT_IFACE
EOF

log::step "25-discover.sh done — wrote $OUT"
