#!/usr/bin/env bash
# lib/common.sh — logging, dependency checks, small idempotency helpers.
# Sourced by phase scripts; not meant to be executed directly.

if [[ -n "${GKEBNKCTL_COMMON_SH_LOADED:-}" ]]; then
  return 0 2>/dev/null || exit 0
fi
GKEBNKCTL_COMMON_SH_LOADED=1

log::info()  { printf '\033[1;34m[info]\033[0m  %s\n' "$*" >&2; }
log::warn()  { printf '\033[1;33m[warn]\033[0m  %s\n' "$*" >&2; }
log::error() { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; }
log::step()  { printf '\033[1;36m==>\033[0m %s\n' "$*" >&2; }

# require_cmd <name> [name...] — fail fast with a clear message if a
# required binary isn't on PATH.
require_cmd() {
  local missing=()
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || missing+=("$c")
  done
  if ((${#missing[@]} > 0)); then
    log::error "missing required command(s): ${missing[*]}"
    exit 1
  fi
}

# common::confirm_gcloud_project <project> — sanity check gcloud is
# authenticated and the configured project matches, without mutating
# anything.
common::confirm_gcloud_project() {
  local want_project="$1"
  local have_account
  have_account="$(gcloud config get-value account 2>/dev/null || true)"
  if [[ -z "$have_account" || "$have_account" == "(unset)" ]]; then
    log::error "gcloud has no active account — run 'gcloud auth login' first"
    exit 1
  fi
  log::info "gcloud account: $have_account, target project: $want_project"
}

# common::gcloud_resource_exists <describe-args...> — returns 0 if the
# resource already exists (used to keep every phase script idempotent).
common::gcloud_resource_exists() {
  gcloud "$@" >/dev/null 2>&1
}

# common::render_template <template-file> <out-file> — envsubst wrapper.
# Fails loudly if envsubst isn't installed rather than silently leaving
# ${VAR} placeholders in rendered manifests.
common::render_template() {
  local tmpl="$1" out="$2"
  require_cmd envsubst
  mkdir -p "$(dirname "$out")"
  envsubst <"$tmpl" >"$out"
  log::info "rendered $out from $(basename "$tmpl")"
}

# common::confirm <prompt> — interactive yes/no gate for destructive ops.
# Skipped automatically when GKEBNKCTL_YES=1 (CI / non-interactive use).
common::confirm() {
  local prompt="$1"
  if [[ "${GKEBNKCTL_YES:-0}" == "1" ]]; then
    return 0
  fi
  read -r -p "$prompt [y/N] " reply
  [[ "$reply" =~ ^[Yy]$ ]]
}
