#!/bin/sh
# gkebnkctl installer for Linux + macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/JLCode-tech/gkebnkctl/main/install.sh | sh
#
# Downloads a release archive for this OS/arch, verifies its checksum, extracts
# the binary, and hands off to the binary's own `gkebnkctl install` (which copies
# it onto $PATH, replacing any existing copy). The archive + extracted binary are
# then removed, leaving only the installed binary.
#
# Options (env or first arg):
#   VERSION=vX.Y.Z            install a specific release (default: latest)
#   GKEBNKCTL_INSTALL_ARGS    passed to `gkebnkctl install` (e.g. "--dir $HOME/bin")
set -eu

REPO="JLCode-tech/gkebnkctl"
INSTALL_ARGS="${GKEBNKCTL_INSTALL_ARGS:-}"

# ---- OS / arch (goreleaser naming) -----------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux) os=linux ;;
  darwin) os=darwin ;;
  *) echo "gkebnkctl: unsupported OS '$os' (Linux/macOS only; on Windows use install.ps1)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "gkebnkctl: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

# ---- resolve the release version -------------------------------------------
ver="${1:-${VERSION:-}}"
if [ -z "$ver" ]; then
  ver=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/')
fi
[ -n "$ver" ] || { echo "gkebnkctl: could not resolve a release version" >&2; exit 1; }
verNoV="${ver#v}"

asset="gkebnkctl_${verNoV}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/${ver}"

# ---- download into a temp dir that is cleaned up on exit -------------------
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t gkebnkctl)
# The trap removes the archive AND the extracted binary, so only the copy that
# `gkebnkctl install` places on $PATH remains.
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading gkebnkctl ${ver} (${os}/${arch})..."
curl -fsSL "${base}/${asset}" -o "$tmp/$asset"

# ---- verify checksum (best-effort; skipped if checksums.txt is absent) -----
if curl -fsSL "${base}/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
  want=$(grep " ${asset}\$" "$tmp/checksums.txt" 2>/dev/null | awk '{print $1}' | head -1)
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    else
      got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    fi
    if [ "$want" != "$got" ]; then
      echo "gkebnkctl: checksum mismatch for $asset (want $want, got $got)" >&2
      exit 1
    fi
    echo "Checksum OK."
  fi
fi

# ---- extract + hand off to the binary's own installer ----------------------
tar -xzf "$tmp/$asset" -C "$tmp"
if [ ! -x "$tmp/gkebnkctl" ]; then
  echo "gkebnkctl: binary not found in $asset" >&2
  exit 1
fi

echo "Installing via 'gkebnkctl install'..."
# shellcheck disable=SC2086
"$tmp/gkebnkctl" install --force $INSTALL_ARGS

echo "Done. Run 'gkebnkctl version' to confirm."
