# Install

ChitraGit is one binary with no dependencies other than **Git 2.30 or newer**.
It runs on Linux (glibc and musl), macOS 12+ and Windows 10/11, on x86-64 and ARM64.

=== "Windows"

    With [Winget](https://learn.microsoft.com/windows/package-manager/):

    ```powershell
    winget install TTox-Tech.ChitraGit
    ```

    With [Scoop](https://scoop.sh):

    ```powershell
    scoop bucket add chitragit https://github.com/TTox-Tech/ChitraGit
    scoop install chitragit/chitragit
    ```

    Or with the install script (puts `chitragit.exe` in
    `%LOCALAPPDATA%\Programs\chitragit` and adds it to your PATH):

    ```powershell
    irm https://raw.githubusercontent.com/TTox-Tech/ChitraGit/main/install.ps1 | iex
    ```

=== "macOS and Linux"

    ```sh
    curl -fsSL https://raw.githubusercontent.com/TTox-Tech/ChitraGit/main/install.sh | sh
    ```

    The script puts `chitragit` in `~/.local/bin`.

=== "From source"

    With [Go 1.26+](https://go.dev/dl/):

    ```sh
    go install github.com/TTox-Tech/chitragit/cmd/chitragit@latest
    ```

The install scripts download the latest release for your OS and CPU and **refuse to install
it if its checksum does not match** `checksums.txt`. Two settings change what they do:

| Variable | |
|---|---|
| `CHITRAGIT_VERSION` | a tag such as `v1.0.0` (default: the latest release) |
| `CHITRAGIT_INSTALL_DIR` | where to put the binary |

## Download by hand

Every [release](https://github.com/TTox-Tech/ChitraGit/releases) has an archive per OS and
CPU, an SBOM per archive (`<archive>.sbom.json`), a `checksums.txt` and its signature
`checksums.txt.sigstore.json`.

## Verify your download

There are two independent checks. Either one is enough to trust an archive; the release
workflow runs both on every release.

**1. The signed checksums** ([cosign](https://docs.sigstore.dev/cosign/system_config/installation/)).
`checksums.txt` is signed by ChitraGit's release workflow, with no long-lived key: the
signature names the workflow and the tag it ran for. Check the signature, then the archive:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/TTox-Tech/ChitraGit/\.github/workflows/release\.yml@refs/tags/v'
sha256sum --ignore-missing --check checksums.txt      # macOS: shasum -a 256 --ignore-missing -c checksums.txt
```

On Windows (PowerShell), compare the hash yourself after the cosign check:

```powershell
(Get-FileHash chitragit_1.0.0_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
Select-String chitragit_1.0.0_windows_amd64.zip checksums.txt
```

**2. The build provenance** ([GitHub CLI](https://cli.github.com)). GitHub Actions signs a
record of which workflow, commit and tag built each archive:

```sh
gh attestation verify chitragit_1.0.0_linux_amd64.tar.gz --repo TTox-Tech/ChitraGit
```

If a check fails, do not run the file, and tell us at **contact@ttox.tech**. Releases before
v1.0.0-rc.1 have provenance but no signed checksums.

## Check the install

```sh
chitragit version
chitragit doctor     # git, Git LFS, database, config and terminal
```

## Shell completion

Tab completes commands, flags and their values. Pick your shell, then open a new terminal.

**bash** (needs the `bash-completion` package, bash 4 or newer):

```sh
mkdir -p ~/.local/share/bash-completion/completions
chitragit completion bash > ~/.local/share/bash-completion/completions/chitragit
```

**zsh** (completion must be on: `autoload -U compinit && compinit` in `~/.zshrc`):

```sh
chitragit completion zsh > "${fpath[1]}/_chitragit"
```

**fish**:

```sh
chitragit completion fish > ~/.config/fish/completions/chitragit.fish
```

**PowerShell** (Windows, or `pwsh` anywhere): add this line to your profile (`notepad $PROFILE`):

```powershell
chitragit completion powershell | Out-String | Invoke-Expression
```

Every script is generated and loaded by its shell in ChitraGit's tests.
