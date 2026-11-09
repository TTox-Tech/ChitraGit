# Security Policy

ChitraGit handles untrusted content (cloned repositories) and access tokens, so we take
security reports seriously.

## Reporting a vulnerability

Please **do not open a public issue.** Instead, either:

- use GitHub's [private vulnerability reporting](https://github.com/TTox-Tech/ChitraGit/security/advisories/new), or
- email **contact@ttox.tech** with "ChitraGit security" in the subject.

We aim to acknowledge reports within 3 days and to ship a fix for confirmed issues as
quickly as possible. We're happy to credit you in the release notes.

## Supported versions

Only the latest release receives security fixes.

## Official builds

ChitraGit is only published from these places:

| Channel | Where |
|---|---|
| GitHub Releases | <https://github.com/TTox-Tech/ChitraGit/releases> |
| Install scripts | `install.sh` and `install.ps1` in this repository |
| Scoop | the manifest at `bucket/chitragit.json` in this repository |
| Winget | package id `TTox-Tech.ChitraGit` |
| Docs | <https://ttox-tech.github.io/ChitraGit/> |

Anything else that calls itself ChitraGit is a copy we did not build. Every official
release archive is listed in the release's `checksums.txt`, which is signed with keyless
cosign by the release workflow, and carries build provenance signed by GitHub Actions:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/TTox-Tech/ChitraGit/\.github/workflows/release\.yml@refs/tags/v'
sha256sum --ignore-missing --check checksums.txt
gh attestation verify chitragit_<version>_<os>_<arch>.tar.gz --repo TTox-Tech/ChitraGit
```

Step-by-step instructions for every OS are in the
[install guide](https://ttox-tech.github.io/ChitraGit/install/#verify-your-download). The
install scripts refuse to install an archive whose checksum does not match. If a
download fails a check, do not run it, and please tell us at
**contact@ttox.tech**. See also [TRADEMARKS.md](TRADEMARKS.md).
