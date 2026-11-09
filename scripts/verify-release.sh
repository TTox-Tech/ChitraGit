#!/bin/sh
# Verifies a published release the way docs/install tells users to: the cosign
# signature on checksums.txt, every archive's checksum and the build provenance of
# the Linux archive. Then it checks that a tampered checksums.txt is refused, so a
# verification that always passes cannot go unnoticed.
#
# Usage: scripts/verify-release.sh v1.0.0 <work dir>   (needs gh, cosign, sha256sum)
set -eu

tag="${1:?usage: $0 <tag> <dir>}"
dir="${2:?usage: $0 <tag> <dir>}"
repo="${GH_REPO:-TTox-Tech/ChitraGit}"
identity="^https://github.com/${repo}/\.github/workflows/release\.yml@refs/tags/v"
issuer="https://token.actions.githubusercontent.com"

mkdir -p "$dir"
cd "$dir"
gh release download "$tag" --repo "$repo" --clobber \
  --pattern checksums.txt --pattern checksums.txt.sigstore.json \
  --pattern '*_linux_amd64.tar.gz' --pattern '*_windows_amd64.zip'

verify() {
  cosign verify-blob "$1" --bundle checksums.txt.sigstore.json \
    --certificate-identity-regexp "$identity" --certificate-oidc-issuer "$issuer"
}

verify checksums.txt
sha256sum --ignore-missing --check --strict checksums.txt
[ "$(sha256sum --ignore-missing --check checksums.txt | grep -c ': OK$')" -eq 2 ] || {
  echo "expected two archives to be checked" >&2
  exit 1
}
gh attestation verify ./*_linux_amd64.tar.gz --repo "$repo"

# A changed checksums.txt must fail the signature check.
sed '1s/^./0/' checksums.txt > tampered.txt
if cmp -s checksums.txt tampered.txt; then sed '1s/^./1/' checksums.txt > tampered.txt; fi
if verify tampered.txt 2>/dev/null; then
  echo "a tampered checksums.txt passed the signature check" >&2
  exit 1
fi
echo "release $tag verified: signature, checksums and provenance"
