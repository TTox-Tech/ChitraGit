# Changelog

All notable changes to ChitraGit are listed here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-11-07

The first release: the whole v1.0 product spec ([`docs/PRD.md`](docs/PRD.md)),
milestones M0 to M8.

### Added

#### Sync engine, safety and providers

- `init`, `doctor`, `config` and `version` commands, layered settings
  (defaults, user, archive, `CHITRAGIT_*` environment variables, flags) and a
  SQLite archive database.
- A hardened git runner: hooks and `core.fsmonitor` inside archived repos never
  run, and no prompt can hang a run.
- `scan`: finds every repo at any depth, including bare and mirror clones,
  linked worktrees and nested repos, and gives each a stable ID.
- `sync`: parallel updates with a worker pool, per-host limits, retries with
  backoff and per-repo timeouts; `--mode dry-run|fetch`, `--only`.
- `status` and `ls`: the health of every repo from the last sync.
- `--json` output (`"schema": "chitragit/v1"`) and stable exit codes.
- Local changes are stashed, the branch is updated and the changes are put back;
  on a clash the repo is restored exactly as it was.
- Branches deleted upstream are kept under `refs/chitragit/archived/`, and
  history replaced by a force-push under `refs/chitragit/rewritten/`.
- Every run is journaled: a repo left half-updated by a crash is repaired on the
  next run, and an interrupted sync can `--resume` or start `--fresh`.
- GitHub, GitLab and Gitea/Forgejo/Codeberg support, including self-hosted
  servers (`[providers] hosts`).
- `auth login|logout|status`: tokens live in the OS keychain (or a file only you
  can read), from env vars or `gh auth token`; never in git URLs or logs.
- Smart skip: one GitHub API call checks 100 repos, and unchanged repos are not
  fetched.
- Archive guardian: a repo whose upstream is deleted or taken down is marked
  RESCUED after two runs, protected (`lock`/`unlock`) and backed up as a
  verified git bundle (`bundle`). Renamed upstreams are followed automatically.
- `follow`, `unfollow`, `sources` and `clone`: clone every repo of a user, org,
  group or your stars, with filters, and pick up new ones on every sync.

#### The interactive ledger

- Running `chitragit` in a terminal opens a full-screen app with clickable tabs,
  mouse support, help and dialogs, in three themes (`ledger`, `ledger-light`,
  `high-contrast`) with Full / Light / Off / Auto animation.
- Repo Browser with fuzzy search, filters, sorting, multi-select and bulk actions
  (tag, favourite, lock, bundle); Repo Detail with README, commits, branches,
  remotes and sync history.
- Sync screen with live progress, one lane per repo in flight, pause, resume and
  stop; the program always lets repos in flight finish their step.
- Activity Feed, Health Dashboard, Conflict Screen and Settings screen.
- First-run wizard: archive folder, scan, theme, animation and a scheduled sync.
- `activity`, `health`, `show`, `tag` and `fav` commands.

#### Conflicts

- `conflicts`: when local changes clash with an update, resolve them file by file
  or hunk by hunk (mine, theirs or both), in your editor, or abort and get the repo
  back exactly as it was. Original changes are kept under `refs/chitragit/resolved/`.

#### Scheduling and accessibility

- `schedule install|status|remove`: daily or weekly sync through cron, launchd or
  Task Scheduler, with a log per run and a desktop notification at the end.
- `--headless` for scheduled runs and `--accessible` (or `ui.accessible`) for
  screen readers: no colour or animation, words instead of symbols.

#### Packaging

- Release archives for Linux, macOS and Windows on x86-64 and ARM64, with
  `checksums.txt` and build provenance.
- Install with Winget (`winget install TTox-Tech.ChitraGit`), Scoop, `install.sh`,
  `install.ps1` or `go install`. The install scripts verify the checksum.
- A docs site with guides and a command and settings reference generated from
  the code: <https://ttox-tech.github.io/ChitraGit/>.
- Every release is installed with the install scripts on all three OSes and runs
  an end-to-end check that local work survives a sync.

[Unreleased]: https://github.com/TTox-Tech/ChitraGit/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/TTox-Tech/ChitraGit/releases/tag/v1.0.0
