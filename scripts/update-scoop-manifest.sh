#!/usr/bin/env bash
# Prints the Scoop manifest for a release tag to stdout.
# Usage: scripts/update-scoop-manifest.sh v1.0.0 path/to/checksums.txt
set -euo pipefail

tag="${1:?usage: $0 <tag> <checksums.txt>}"
sums="${2:?usage: $0 <tag> <checksums.txt>}"
version="${tag#v}"
base="https://github.com/TTox-Tech/ChitraGit/releases/download/${tag}"

sum_for() {
  local file="chitragit_${version}_windows_$1.zip" hash
  hash="$(awk -v f="$file" '$2 == f { print $1 }' "$sums")"
  [ -n "$hash" ] || { echo "no checksum for $file" >&2; exit 1; }
  echo "$hash"
}

amd64="$(sum_for amd64)"
arm64="$(sum_for arm64)"

jq -n --arg v "$version" --arg base "$base" --arg amd64 "$amd64" --arg arm64 "$arm64" '{
  version: $v,
  description: "The keeper of your Git archive. Keeps every repo synced, safe and accounted for.",
  homepage: "https://github.com/TTox-Tech/ChitraGit",
  license: "MIT",
  architecture: {
    "64bit": { url: "\($base)/chitragit_\($v)_windows_amd64.zip", hash: $amd64 },
    arm64: { url: "\($base)/chitragit_\($v)_windows_arm64.zip", hash: $arm64 }
  },
  bin: "chitragit.exe",
  checkver: "github",
  autoupdate: {
    architecture: {
      "64bit": { url: "https://github.com/TTox-Tech/ChitraGit/releases/download/v$version/chitragit_$version_windows_amd64.zip" },
      arm64: { url: "https://github.com/TTox-Tech/ChitraGit/releases/download/v$version/chitragit_$version_windows_arm64.zip" }
    },
    hash: { url: "$baseurl/checksums.txt" }
  }
}'
