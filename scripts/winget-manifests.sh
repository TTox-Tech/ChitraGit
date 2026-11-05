#!/usr/bin/env bash
# Writes the Winget manifests for a release tag into a folder laid out like
# microsoft/winget-pkgs (manifests/t/TTox-Tech/ChitraGit/<version>/).
# Usage: scripts/winget-manifests.sh v1.0.0 path/to/checksums.txt out-dir
set -euo pipefail

tag="${1:?usage: $0 <tag> <checksums.txt> <out-dir>}"
sums="${2:?usage: $0 <tag> <checksums.txt> <out-dir>}"
out="${3:?usage: $0 <tag> <checksums.txt> <out-dir>}"
version="${tag#v}"
id="TTox-Tech.ChitraGit"
base="https://github.com/TTox-Tech/ChitraGit/releases/download/${tag}"
schema="1.9.0"

sum_for() {
  local file="chitragit_${version}_windows_$1.zip" hash
  hash="$(awk -v f="$file" '$2 == f { print $1 }' "$sums")"
  [ -n "$hash" ] || { echo "no checksum for $file" >&2; exit 1; }
  echo "$hash" | tr '[:lower:]' '[:upper:]'
}
amd64="$(sum_for amd64)"
arm64="$(sum_for arm64)"

dir="$out/manifests/t/TTox-Tech/ChitraGit/$version"
mkdir -p "$dir"

cat > "$dir/$id.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.$schema.schema.json
PackageIdentifier: $id
PackageVersion: $version
DefaultLocale: en-US
ManifestType: version
ManifestVersion: $schema
EOF

cat > "$dir/$id.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.$schema.schema.json
PackageIdentifier: $id
PackageVersion: $version
PackageLocale: en-US
Publisher: TTox.Tech
PublisherUrl: https://ttox.tech
PublisherSupportUrl: https://github.com/TTox-Tech/ChitraGit/issues
PackageName: ChitraGit
PackageUrl: https://github.com/TTox-Tech/ChitraGit
License: MIT
LicenseUrl: https://github.com/TTox-Tech/ChitraGit/blob/main/LICENSE
ShortDescription: The keeper of your Git archive. Keeps every repo synced, safe and accounted for.
Description: |-
  ChitraGit keeps a folder of hundreds or thousands of cloned Git repositories up to date
  in parallel, without ever losing local work: changes are stashed and put back, clashes
  leave the repo exactly as it was, and repos that vanish upstream are kept and backed up.
Moniker: chitragit
Tags:
- git
- archive
- backup
- sync
- cli
- tui
ReleaseNotesUrl: https://github.com/TTox-Tech/ChitraGit/releases/tag/$tag
ManifestType: defaultLocale
ManifestVersion: $schema
EOF

cat > "$dir/$id.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.$schema.schema.json
PackageIdentifier: $id
PackageVersion: $version
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
- RelativeFilePath: chitragit.exe
  PortableCommandAlias: chitragit
Dependencies:
  PackageDependencies:
  - PackageIdentifier: Git.Git
Installers:
- Architecture: x64
  InstallerUrl: $base/chitragit_${version}_windows_amd64.zip
  InstallerSha256: $amd64
- Architecture: arm64
  InstallerUrl: $base/chitragit_${version}_windows_arm64.zip
  InstallerSha256: $arm64
ManifestType: installer
ManifestVersion: $schema
EOF

echo "$dir"
