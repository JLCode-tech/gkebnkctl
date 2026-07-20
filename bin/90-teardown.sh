#!/usr/bin/env bash
# bin/90-teardown.sh — cluster + its OWN VPC/subnet/firewall only.
#
# NEVER deletes network.external / network.internal (bnkhd-ext/bnkhd-int by
# default) — those are treated as shared dataplane infrastructure that other
# clusters may depend on (see cluster.yaml.example comments and
# reference/install/RUNBOOK.md's teardown note). This script does not even
# reference their names below; that's deliberate, not an oversight.
set -euo pipefail

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/config.sh
source "$GKEBNKCTL_ROOT/lib/config.sh"

require_cmd gcloud
config::load "${1:-}"
common::confirm_gcloud_project "$CFG_METADATA_PROJECT"

log::warn "about to delete cluster $CFG_METADATA_NAME and its own VPC ($CFG_NETWORK_MAIN_VPC) in project $CFG_METADATA_PROJECT"
log::warn "shared dataplane VPCs ($CFG_NETWORK_EXTERNAL_VPC, $CFG_NETWORK_INTERNAL_VPC) are NEVER touched by this script."
common::confirm "proceed?" || { log::info "aborted"; exit 0; }

if common::gcloud_resource_exists container clusters describe "$CFG_METADATA_NAME" \
    --zone="$CFG_METADATA_ZONE" --project="$CFG_METADATA_PROJECT"; then
  log::step "deleting cluster $CFG_METADATA_NAME (node pools go with it)"
  gcloud container clusters delete "$CFG_METADATA_NAME" \
    --zone="$CFG_METADATA_ZONE" --project="$CFG_METADATA_PROJECT" --quiet
else
  log::info "cluster $CFG_METADATA_NAME not found — skipping"
fi

fw_name="${CFG_NETWORK_MAIN_VPC}-allow-internal"
if common::gcloud_resource_exists compute firewall-rules describe "$fw_name" --project="$CFG_METADATA_PROJECT"; then
  log::step "deleting firewall rule $fw_name"
  gcloud compute firewall-rules delete "$fw_name" --project="$CFG_METADATA_PROJECT" --quiet
else
  log::info "firewall rule $fw_name not found — skipping"
fi

if common::gcloud_resource_exists compute networks subnets describe "$CFG_NETWORK_MAIN_SUBNET" \
    --region="$DERIVED_REGION" --project="$CFG_METADATA_PROJECT"; then
  log::step "deleting subnet $CFG_NETWORK_MAIN_SUBNET"
  gcloud compute networks subnets delete "$CFG_NETWORK_MAIN_SUBNET" \
    --region="$DERIVED_REGION" --project="$CFG_METADATA_PROJECT" --quiet
else
  log::info "subnet $CFG_NETWORK_MAIN_SUBNET not found — skipping"
fi

if common::gcloud_resource_exists compute networks describe "$CFG_NETWORK_MAIN_VPC" --project="$CFG_METADATA_PROJECT"; then
  log::step "deleting VPC $CFG_NETWORK_MAIN_VPC"
  gcloud compute networks delete "$CFG_NETWORK_MAIN_VPC" --project="$CFG_METADATA_PROJECT" --quiet
else
  log::info "VPC $CFG_NETWORK_MAIN_VPC not found — skipping"
fi

rm -f "$GKEBNKCTL_ROOT/discovered.env"

log::step "90-teardown.sh done — $CFG_NETWORK_EXTERNAL_VPC / $CFG_NETWORK_INTERNAL_VPC left untouched"
