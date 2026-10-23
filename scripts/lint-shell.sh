#!/usr/bin/env bash
# scripts/lint-shell.sh — Syntax-check and shellcheck all repository shell scripts.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

shopt -s nullglob
SCRIPTS=()
while IFS= read -r file; do
  [[ -f "$file" ]] && SCRIPTS+=("$file")
done < <(git ls-files '*.sh'; find scripts -maxdepth 2 -type f -name '*.sh' 2>/dev/null | sort -u)

# Deduplicate while preserving order.
UNIQUE_SCRIPTS=()
for s in "${SCRIPTS[@]}"; do
  skip=0
  for existing in "${UNIQUE_SCRIPTS[@]:-}"; do
    if [[ "$existing" == "$s" ]]; then
      skip=1
      break
    fi
  done
  if [[ "$skip" -eq 0 ]]; then
    UNIQUE_SCRIPTS+=("$s")
  fi
done

if [[ "${#UNIQUE_SCRIPTS[@]}" -eq 0 ]]; then
  echo "lint-shell: no shell scripts found"
  exit 0
fi

for s in "${UNIQUE_SCRIPTS[@]}"; do
  bash -n "$s"
done

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S error "${UNIQUE_SCRIPTS[@]}"
else
  echo "lint-shell: shellcheck not installed; syntax check (bash -n) passed for ${#UNIQUE_SCRIPTS[@]} script(s)"
fi
