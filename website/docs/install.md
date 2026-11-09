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

    The script puts `chitragit` in `~/.local/bin`. Homebrew support is planned.

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
| `CHITRAGIT_ALIASES` | short names for the same program: any of `chit`, `cg`, `git-chit` (default `chit`, `none` for none). A name another program already uses is skipped. |

## Download by hand

Every [release](https://github.com/TTox-Tech/ChitraGit/releases) has an archive per OS and
CPU, a `checksums.txt` and build provenance you can check with the GitHub CLI:

```sh
gh attestation verify chitragit_1.0.0_linux_amd64.tar.gz --repo TTox-Tech/ChitraGit
```

## Check the install

```sh
chitragit version
chitragit doctor     # git, Git LFS, database, config and terminal
```

## Shell completion

```sh
chitragit completion bash > ~/.local/share/bash-completion/completions/chitragit
chitragit completion zsh  > "${fpath[1]}/_chitragit"
chitragit completion fish > ~/.config/fish/completions/chitragit.fish
chitragit completion powershell | Out-String | Invoke-Expression   # add to $PROFILE
```
