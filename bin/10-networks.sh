#!/usr/bin/env bash
# bin/10-networks.sh — VPCs, subnets (with secondary ranges), firewall, Cloud NAT.
#
# Idempotent: every gcloud call is guarded by a `describe` check first, so
# re-running this script after a partial failure just fills in what's missing.
#
# network.external / network.internal are treated as potentially SHARED
# dataplane infrastructure (see cluster.yaml.example comments) — created here
# only if absent, and never deleted by bin/90-teardown.sh.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"

require_cmd gcloud python3
config::load "${1:-}"
common::confirm_gcloud_project "$CFG_METADATA_PROJECT"

net::network_exists() {
  common::gcloud_resource_exists compute networks describe "$1" --project="$CFG_METADATA_PROJECT"
}

net::subnet_exists() {
  common::gcloud_resource_exists compute networks subnets describe "$1" \
    --region="$DERIVED_REGION" --project="$CFG_METADATA_PROJECT"
}

net::firewall_exists() {
  common::gcloud_resource_exists compute firewall-rules describe "$1" --project="$CFG_METADATA_PROJECT"
}

net::router_exists() {
  common::gcloud_resource_exists compute routers describe "$1" \
    --region="$DERIVED_REGION" --project="$CFG_METADATA_PROJECT"
}

net::nat_exists() {
  gcloud compute routers nats describe "$2" --router="$1" \
    --region="$DERIVED_REGION" --project="$CFG_METADATA_PROJECT" >/dev/null 2>&1
}

# net::create_vpc <vpc> — custom-mode VPC, MTU optional (default 1460).
net::create_vpc() {
  local vpc="$1" mtu="${2:-1460}"
  if net::network_exists "$vpc"; then
    log::info "VPC $vpc already exists — skipping"
    return
  fi
  log::step "creating VPC $vpc (mtu=$mtu)"
  gcloud compute networks create "$vpc" \
    --project="$CFG_METADATA_PROJECT" \
    --subnet-mode=custom \
    --mtu="$mtu"
}

# net::create_subnet <vpc> <subnet> <cidr> [pods-range-name pods-cidr [svcs-range-name svcs-cidr]]
net::create_subnet() {
  local vpc="$1" subnet="$2" cidr="$3"
  local pods_name="${4:-}" pods_cidr="${5:-}" svcs_name="${6:-}" svcs_cidr="${7:-}"

  if net::subnet_exists "$subnet"; then
    log::info "subnet $subnet already exists — skipping"
    return
  fi

  local ranges_args=()
  if [[ -n "$pods_name" ]]; then
    ranges_args+=(--secondary-range "${pods_name}=${pods_cidr}")
  fi
  if [[ -n "$svcs_name" ]]; then
    ranges_args+=(--secondary-range "${svcs_name}=${svcs_cidr}")
  fi

  log::step "creating subnet $subnet ($cidr) in $vpc"
  gcloud compute networks subnets create "$subnet" \
    --project="$CFG_METADATA_PROJECT" \
    --network="$vpc" \
    --region="$DERIVED_REGION" \
    --range="$cidr" \
    "${ranges_args[@]}"
}

net::create_allow_internal_fw() {
  local vpc="$1"
  local name="${vpc}-allow-internal"
  if net::firewall_exists "$name"; then
    log::info "firewall rule $name already exists — skipping"
    return
  fi
  log::step "creating firewall rule $name (allow all from $CFG_NETWORK_ALLOWINTERNALCIDR)"
  gcloud compute firewall-rules create "$name" \
    --project="$CFG_METADATA_PROJECT" \
    --network="$vpc" \
    --direction=INGRESS \
    --action=ALLOW \
    --rules=all \
    --source-ranges="$CFG_NETWORK_ALLOWINTERNALCIDR"
}

# net::create_nat <vpc> <router-name-prefix> — tolerates 403 (lacking
# compute.routers.create) with a clear message; never fatal.
net::create_nat() {
  local vpc="$1" router="${2}-router"
  if net::router_exists "$router"; then
    log::info "router $router already exists — skipping create"
  else
    log::step "creating Cloud Router $router on $vpc"
    if ! gcloud compute routers create "$router" \
        --project="$CFG_METADATA_PROJECT" \
        --network="$vpc" \
        --region="$DERIVED_REGION" 2>/tmp/gkebnkctl-router-err.$$; then
      if grep -qi "403\|permission" /tmp/gkebnkctl-router-err.$$; then
        log::warn "no permission to create router $router (compute.routers.create denied)."
        log::warn "ASK YOUR PROJECT OWNER TO RUN:"
        log::warn "  gcloud compute routers create $router --project=$CFG_METADATA_PROJECT --network=$vpc --region=$DERIVED_REGION"
        log::warn "  gcloud compute routers nats create ${vpc}-nat --router=$router --region=$DERIVED_REGION --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges --project=$CFG_METADATA_PROJECT"
        log::warn "egress will only reach in-VPC targets until this exists."
        rm -f /tmp/gkebnkctl-router-err.$$
        return 0
      fi
      cat /tmp/gkebnkctl-router-err.$$ >&2
      rm -f /tmp/gkebnkctl-router-err.$$
      exit 1
    fi
    rm -f /tmp/gkebnkctl-router-err.$$
  fi

  local nat="${vpc}-nat"
  if net::nat_exists "$router" "$nat"; then
    log::info "NAT $nat already exists — skipping"
    return
  fi
  log::step "creating Cloud NAT $nat on router $router"
  gcloud compute routers nats create "$nat" \
    --router="$router" \
    --region="$DERIVED_REGION" \
    --project="$CFG_METADATA_PROJECT" \
    --auto-allocate-nat-external-ips \
    --nat-all-subnet-ip-ranges || {
      log::warn "NAT create failed on $router (see error above) — treating as non-fatal, matching router-create 403 handling."
    }
}

# --- main VPC: owned by this cluster ---
net::create_vpc "$CFG_NETWORK_MAIN_VPC"
net::create_subnet "$CFG_NETWORK_MAIN_VPC" "$CFG_NETWORK_MAIN_SUBNET" "$CFG_NETWORK_MAIN_CIDR" \
  "$CFG_NETWORK_MAIN_PODSRANGENAME" "$CFG_NETWORK_MAIN_PODSCIDR" \
  "$CFG_NETWORK_MAIN_SVCSRANGENAME" "$CFG_NETWORK_MAIN_SVCSCIDR"
net::create_allow_internal_fw "$CFG_NETWORK_MAIN_VPC"

# --- dataplane VPCs: shared, created-if-missing, never torn down ---
net::create_vpc "$CFG_NETWORK_EXTERNAL_VPC" "$CFG_NETWORK_EXTERNAL_MTU"
net::create_subnet "$CFG_NETWORK_EXTERNAL_VPC" "$CFG_NETWORK_EXTERNAL_SUBNET" "$CFG_NETWORK_EXTERNAL_CIDR" \
  "$CFG_NETWORK_EXTERNAL_PODSRANGENAME" "$CFG_NETWORK_EXTERNAL_PODSCIDR"
net::create_allow_internal_fw "$CFG_NETWORK_EXTERNAL_VPC"

net::create_vpc "$CFG_NETWORK_INTERNAL_VPC" "$CFG_NETWORK_INTERNAL_MTU"
net::create_subnet "$CFG_NETWORK_INTERNAL_VPC" "$CFG_NETWORK_INTERNAL_SUBNET" "$CFG_NETWORK_INTERNAL_CIDR" \
  "$CFG_NETWORK_INTERNAL_PODSRANGENAME" "$CFG_NETWORK_INTERNAL_PODSCIDR"
net::create_allow_internal_fw "$CFG_NETWORK_INTERNAL_VPC"

if [[ "$CFG_NETWORK_NATGATEWAYS" == "true" ]]; then
  net::create_nat "$CFG_NETWORK_EXTERNAL_VPC" "$CFG_NETWORK_EXTERNAL_VPC"
  net::create_nat "$CFG_NETWORK_INTERNAL_VPC" "$CFG_NETWORK_INTERNAL_VPC"
else
  log::info "network.natGateways=false — skipping Cloud NAT"
fi

log::step "10-networks.sh done"
