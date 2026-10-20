<div align="center">

<pre>
   ___ _    _ _             ___ _ _
  / __| |_ (_) |_ _ _ __ _ / __(_) |_
 | (__| ' \| |  _| '_/ _' | (_ | |  _|
  \___|_||_|_|\__|_| \__,_|\___|_|\__|
</pre>

### 📜 The keeper of your Git archive.

Sync, browse and protect thousands of local repos from one beautiful terminal UI.

[![CI](https://github.com/TTox-Tech/ChitraGit/actions/workflows/ci.yml/badge.svg)](https://github.com/TTox-Tech/ChitraGit/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-d4a017.svg)](LICENSE)
![Go](https://img.shields.io/badge/go-1.26+-1b4965.svg)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-ff9933.svg)

</div>

> **🚧 Status: in active development.** The headless sync engine works today; the safety
> layer (stash and rollback) and the interactive ledger are being built now. Star or watch
> the repo to follow the launch.

---

## Why ChitraGit?

*Chitragupta* is the divine record-keeper who never forgets a single entry.
ChitraGit does the same for your repos.

If you keep a folder full of cloned repositories (learning material, dependencies,
whole GitHub orgs, "just in case it disappears" copies), you know the pain:

- updating hundreds of clones means fragile shell loops,
- one `git pull` in the wrong repo and your local work is in a mess,
- you never notice when an upstream repo is **deleted, made private or taken down**,
  which is exactly when your copy becomes precious.

ChitraGit is a **local GitHub for your archive, but better**: offline, all in one place,
and actively guarding what you've collected.

## What it will do

| | |
|---|---|
| ⚡ **Parallel sync for 5,000+ repos** | Smart skip asks the provider API what changed, so unchanged repos cost nothing. |
| 🛡️ **Zero data loss** | Local changes are stashed and re-applied. If anything conflicts, the repo is restored exactly as it was. Force-pushed or deleted upstream history is kept, never pruned. |
| 🏅 **Rescued repos** | When a repo vanishes upstream (404, DMCA, made private), it gets a gold badge, delete-protection and an optional single-file backup. Renamed repos are followed automatically. |
| 🖱️ **GUI-quality TUI** | Clickable, animated, Indian *bahi-khata* ledger theme. Repo browser, README and commit views, activity feed, health dashboard, conflict resolver. |
| 🤖 **Full headless CLI** | Every feature scriptable, with `--json` output and stable exit codes. One-click scheduling with cron, launchd or Task Scheduler. |
| 🌐 **Any forge** | GitHub, GitLab, Gitea/Forgejo/Codeberg, or any plain Git URL. Auto-clone a user's repos or your stars. |
| 🔒 **Safe by design** | Hooks and `core.fsmonitor` inside archived repos never run. No prompts hang a run. Tokens live in your OS keychain. No telemetry. |

The full product spec, including 55+ edge cases and how each is handled, is in
[`docs/PRD.md`](docs/PRD.md).

## Try the current build

Requires [Go 1.26+](https://go.dev/dl/) and Git 2.30+.

```sh
go install github.com/TTox-Tech/chitragit/cmd/chitragit@latest

cd ~/my-archive
chitragit init        # set up this folder as an archive
chitragit doctor      # check git, LFS, database, config, terminal
chitragit sync        # find every repo, then update them all in parallel
chitragit status      # health of every repo from the last sync
```

What `sync` does today:

- **Finds every repo**, at any depth: normal clones, bare/mirror clones, linked worktrees
  and repos nested inside other repos. Each one gets a stable ID stored in its own git
  config, so moving or renaming a folder keeps its history.
- **Downloads** new commits, branches and tags from every remote. Branches and tags deleted
  upstream are **kept** in your copy (pruning is off unless you turn on `sync.prune`).
- **Fast-forwards** branches when that is safe, meaning the branch simply moves ahead to the
  new commits. Branches that are not checked out are moved too, without touching any files.
- **Never touches your work.** A repo with uncommitted changes, local commits, a detached
  HEAD, a merge in progress or an upstream force-push still gets the new data, but its
  branches and files stay exactly as they were, and the summary says why.
- **Handles flaky networks**: per-repo timeout, retries with exponential backoff for network
  errors, a per-host limit (8 at a time for github.com by default) to avoid rate limits, and
  a fast stop when a host is unreachable.

```sh
chitragit sync --mode dry-run      # ask the remotes what changed; download nothing
chitragit sync --mode fetch        # download only, never move a branch
chitragit sync --only 'torvalds/*' # just some repos
chitragit ls --status diverged     # repos that need a decision
chitragit sync --json              # versioned JSON ("schema": "chitragit/v1") for scripts
```

Exit codes: `0` all good · `1` some repos failed · `2` bad flags or config ·
`3` another run holds the archive · `4` git missing or too old · `130` stopped with Ctrl+C.
The first Ctrl+C lets repos that already started finish safely; a second one exits at once.

To skip folders, list names or globs in `scan.exclude`, or in a `.chitragitignore` file in
the archive root (one pattern per line; a pattern with `/` matches the path from the root).

Settings are layered (later wins): built-in defaults → your config → the archive's
config → `CHITRAGIT_*` environment variables → command-line flags.

```sh
chitragit config set sync.workers 32          # this archive only
chitragit config set --user ui.theme ledger   # all your archives
CHITRAGIT_SYNC_MODE=fetch chitragit ...       # one run
```

## Roadmap

- [x] **M0 Foundation**: config layering, archive database, hardened git runner, `init`, `doctor`, `config`, CI on all 3 OSes
- [x] **M1 Engine**: scan, parallel sync, `scan` / `sync` / `status` / `ls`, JSON output, exit codes
- [ ] **M2 Safety**: stash flow, rollback, archive-safe refs, resume
- [ ] **M3 TUI core**: ledger theme, animations, repo browser, sync screen
- [ ] **M4 TUI depth**: repo detail, activity feed, health dashboard
- [ ] **M5 Providers**: GitHub/GitLab/Gitea, login, smart skip, Rescued repos, auto-clone
- [ ] **M6 Conflicts**: interactive conflict resolver
- [ ] **M7 Experience**: first-run wizard, settings screen, scheduling, notifications
- [ ] **M8 Ship**: Homebrew, Scoop, Winget, install scripts, v1.0 🚀

## Contributing

Contributions are very welcome, especially translations and themes.
See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE) © [TTox.Tech](https://ttox.tech)
