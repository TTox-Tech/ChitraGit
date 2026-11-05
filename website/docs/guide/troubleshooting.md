# Troubleshooting

Start with `chitragit doctor`. It checks git, Git LFS, the archive database, the lock,
your configuration and the terminal, and says what to fix.

??? question "A sync says another run holds the archive (exit code 3)"
    Only one run can work on an archive at a time. If no other `chitragit` is running
    (for example after a crash), `chitragit doctor` shows the lock and whether it is stale.

??? question "A repo says `diverged`, `ahead` or `rewritten`"
    You have local commits. ChitraGit never merges or rebases them: it downloads the new
    data and leaves your branch alone. Look with `git log --oneline --graph --all` in
    that repo and decide yourself.

??? question "Where did a deleted branch or force-pushed history go?"
    It is kept under `refs/chitragit/archived/` and `refs/chitragit/rewritten/`:
    `git for-each-ref refs/chitragit/` lists them.

??? question "The ledger looks broken or has no colour"
    Try a bigger window (at least 60x20), `chitragit config set --user ui.animations off`,
    or `--accessible`. On Windows, use Windows Terminal rather than the old console.

??? question "Private repos fail with an authentication error"
    Git uses your own credentials and SSH keys for fetching. Check that a plain
    `git fetch` works in that repo. `chitragit auth login` is for the forge APIs (stars,
    follow, smart skip), not for git itself.

??? question "Something went wrong"
    Run the command again with `--verbose` and
    [open an issue](https://github.com/TTox-Tech/ChitraGit/issues/new/choose) with the
    output and `chitragit version`. For security problems, see
    [SECURITY.md](https://github.com/TTox-Tech/ChitraGit/blob/main/SECURITY.md).
