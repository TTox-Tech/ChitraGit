#!/bin/sh
# Installs the latest ChitraGit release on macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/TTox-Tech/ChitraGit/main/install.sh | sh
#
# Settings (environment variables):
#   CHITRAGIT_VERSION      a tag such as v0.3.0 (default: the latest release)
#   CHITRAGIT_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#
# The archive is checked against the release's checksums.txt before anything
# is installed.
set -eu

REPO="TTox-Tech/ChitraGit"
INSTALL_DIR="${CHITRAGIT_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'chitragit install: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required but not installed"; }
need curl
need tar
need uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS $(uname -s); on Windows use install.ps1 or Scoop" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required to verify the download"
fi

tag="${CHITRAGIT_VERSION:-}"
if [ -z "$tag" ]; then
  # The latest-release page redirects to /releases/tag/<tag>.
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
    fail "could not reach GitHub"
  tag="${url##*/}"
  [ "$tag" != "latest" ] || fail "no release has been published yet"
fi
version="${tag#v}"

archive="chitragit_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading ChitraGit $tag for $os/$arch..."
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not listed in checksums.txt"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || fail "checksum mismatch for $archive (expected $want, got $got); nothing was installed"
say "Checksum verified."

tar -xzf "$tmp/$archive" -C "$tmp" chitragit
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/chitragit" "$INSTALL_DIR/chitragit" 2>/dev/null ||
  { cp "$tmp/chitragit" "$INSTALL_DIR/chitragit" && chmod 0755 "$INSTALL_DIR/chitragit"; } ||
  fail "could not write to $INSTALL_DIR; set CHITRAGIT_INSTALL_DIR to a folder you own"

say "Installed $("$INSTALL_DIR/chitragit" version 2>/dev/null || echo chitragit) to $INSTALL_DIR"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Add $INSTALL_DIR to your PATH, for example:"
     say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile" ;;
esac
say "Next: cd into your archive folder and run 'chitragit init'."
