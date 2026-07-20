#!/usr/bin/env bash
# lib/config.sh — parses cluster.yaml into CFG_* env vars (via lib/yaml2env.py)
# and derives a few computed values every phase script needs.
# Sourced by phase scripts; not meant to be executed directly.

if [[ -n "${GKEBNKCTL_CONFIG_SH_LOADED:-}" ]]; then
  return 0 2>/dev/null || exit 0
fi
GKEBNKCTL_CONFIG_SH_LOADED=1

GKEBNKCTL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GKEBNKCTL_LIB="$GKEBNKCTL_ROOT/lib"

# shellcheck source=lib/common.sh
source "$GKEBNKCTL_LIB/common.sh"

# config::load [path] — default path is ./cluster.yaml, or $CLUSTER_CONFIG
# if set. Exports CFG_* vars plus a handful of DERIVED_* convenience vars.
config::load() {
  local cfg_file="${1:-${CLUSTER_CONFIG:-cluster.yaml}}"
  if [[ ! -f "$cfg_file" ]]; then
    log::error "config file not found: $cfg_file"
    log::error "copy cluster.yaml.example to cluster.yaml and edit it, or pass -f <path>"
    exit 1
  fi
  require_cmd python3

  local parsed
  if ! parsed="$(python3 "$GKEBNKCTL_LIB/yaml2env.py" "$cfg_file")"; then
    log::error "failed to parse $cfg_file (see PyYAML error above)"
    exit 1
  fi
  # `set -a` auto-exports every var assigned while it's on — required so
  # envsubst (a child process) can see CFG_* when rendering manifests/*.tmpl.
  set -a
  eval "$parsed"
  set +a
  export CFG_FILE="$cfg_file"

  config::require CFG_METADATA_NAME
  config::require CFG_METADATA_PROJECT
  config::require CFG_METADATA_ZONE

  # Derived values used across multiple phases — compute once here so
  # every script agrees on the same gateway/self-IP conventions.
  export DERIVED_EXT_GW_IP DERIVED_INT_GW_IP
  DERIVED_EXT_GW_IP="$(python3 "$GKEBNKCTL_LIB/netcalc.py" nth "$CFG_NETWORK_EXTERNAL_CIDR" 1)"
  DERIVED_INT_GW_IP="$(python3 "$GKEBNKCTL_LIB/netcalc.py" nth "$CFG_NETWORK_INTERNAL_CIDR" 1)"

  export DERIVED_REGION
  DERIVED_REGION="${CFG_METADATA_ZONE%-*}"
}

# config::require <VAR_NAME> — fail with a clear message if a config value
# that a phase script depends on is empty.
config::require() {
  local var_name="$1"
  if [[ -z "${!var_name:-}" ]]; then
    log::error "required config value missing: $var_name (check ${CFG_FILE:-cluster.yaml})"
    exit 1
  fi
}

# config::discovered_env_path — where 25-discover.sh writes/reads its output.
config::discovered_env_path() {
  echo "$GKEBNKCTL_ROOT/discovered.env"
}

# config::load_discovered — sources discovered.env, failing with a clear
# "run 25-discover.sh first" message if it's missing.
config::load_discovered() {
  local path
  path="$(config::discovered_env_path)"
  if [[ ! -f "$path" ]]; then
    log::error "discovered.env not found at $path"
    log::error "run bin/25-discover.sh first (after node pools are Ready)"
    exit 1
  fi
  set -a
  # shellcheck disable=SC1090,SC1091
  source "$path"
  set +a
}
