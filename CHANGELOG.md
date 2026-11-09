# Changelog

All notable changes to ChitraGit are listed here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Safer clones and syncs: every object git receives is checked, paths that disguise a
  `.git` folder on Windows or macOS are refused, and the `ext::` and `fd::` transports are
  off. A repo that sends unsafe objects is refused before anything is written.
- `sync.lfs` now works: checkouts keep Git LFS pointer files unless it is `always`, so an
  archived repo cannot make git-lfs download whatever it points at.
- `chitragit doctor` warns when git lacks the fix for CVE-2024-32002 or CVE-2025-48384,
  and sync stops fetching submodules until git is updated.
- `chitragit restore <repo> --to <snapshot>`: puts a repo's branches and tags back as a
  snapshot recorded them, after first snapshotting the current refs so the restore can be
  undone. All refs move in one transaction; the checked-out branch moves with its files and
  needs a clean tree, or `--stash`. `--dry-run`, `--json`, `--yes`.
- `chitragit history <repo>`: everything that happened to one repo, newest first: every ref
  a sync created, moved, rewrote or saw deleted upstream, every snapshot and events such as
  rescues and renames. `--json` and `--limit`.
- Snapshots: every sync records each repo's full ref state before and after it touches the
  repo, storing nothing when the repo did not change, and pins every object a snapshot names
  under `refs/chitragit/snap/` so `git gc` never removes it. `chitragit snapshot ls`,
  `snapshot show` and `snapshot take` list, inspect and record them.
- An operation log: every change ChitraGit makes to an archive (syncs, scans, tags,
  favourites, sources, rescues, bundles, resolved conflicts, audits) is recorded in the
  same transaction as the change, in a SHA-256 hash chain. `chitragit log` lists it
  (`--repo`, `--since`, `--json`) and `chitragit log --verify` reports the first entry that
  was changed or removed. Older archives are chained on first open.
- TRADEMARKS.md (the code is MIT, the name and logo are not) and an "Official builds"
  section in SECURITY.md that lists every official channel and how to verify a download.
  The docs site now asks AI training crawlers not to use it.
- KodeKavach, a read-only security audit (`chitragit audit`). It finds leaked
  secrets in every repo's files and whole git history, bare and mirror clones
  included, using ChitraGit's own detector with patterns converted from the
  Gitleaks rule set (MIT). Audits are incremental, never
  change a repo, never run a repo's diff drivers, and never store a secret in
  full. `audit findings`, `audit ignore` and `audit unignore` manage results;
  `--json` and `--sarif` export them; exit code `5` means findings at or above
  `--fail-on`. New `[audit]` settings.
- KodeKavach `deps` check: dependencies with known vulnerabilities. It reads the
  lockfiles at each repo's HEAD (Go, npm, yarn, pnpm, Cargo, Poetry, uv, PDM,
  Pipenv, requirements.txt, Bundler, Composer, Gradle, NuGet and Pub) and matches
  every pinned version against an offline copy of the OSV database, so no package
  list leaves the machine. `audit db update` downloads it (also offered in the
  first-run wizard) and `audit db status` shows its age. Aliases are merged into
  one finding, severity comes from the advisory or its CVSS v3 score, and an
  upgraded dependency is marked fixed rather than deleted (`audit findings
  --fixed`). New setting `audit.osv_db`; schema v4 adds `findings.fixed_at`.
- KodeKavach `actions` check: risky GitHub Actions workflows (pull_request_target
  checkouts, script injection, write-all tokens, unpinned actions and more), and
  `agents` check: poisoned AI agent files (instructions, skills, rules, MCP configs,
  hook settings) across every common coding agent. Detection data ships as an
  Ed25519-signed rules pack that `audit rules update` refreshes from ChitraGit's own
  GitHub Releases (weekly by default, `--from` for offline machines). zizmor and
  SkillSpector can run as an opt-in extra (off by default), offline and
  sandboxed; `doctor` shows whether they are installed. A problem removed from
  HEAD is marked fixed, like an upgraded dependency.
- Releases sign `checksums.txt` with keyless cosign. `checksums.txt.sigstore.json` holds the signature
  and the certificate that ties it to ChitraGit's release workflow, so one check covers every archive.

### Changed

- The brand font is now M PLUS Code Latin (variable, weight 100 to 700) for the wordmark,
  titles, artwork and code, embedded in every SVG; the docs site keeps Inter for long body
  text. Inter Display and JetBrains Mono are gone from `docs/assets/fonts`.
- New look: the ledger theme, the logo, the README artwork, the docs site and the demo
  GIFs now use black, dark grey and blue (neel) instead of brown and saffron. Theme
  names and settings are unchanged. Text banners for terminals live in `docs/assets/ascii`.
- The startup screen shows the new mark: the ledger tile and a block wordmark, with an
  ASCII version for consoles without Unicode. The demo GIFs are re-recorded with it.
- A livelier README: a 30-second promo GIF, a stats strip, a before/after picture, a
  feature grid, install steps per OS, a "Try it in 30 seconds" section and an FAQ.

### Fixed

- KodeKavach reports a prompt-injection phrase that a guide or skill cites inside a code span,
  a code block or double quotes as low instead of high, so security docs and prompt filters
  stop raising alarms. The same phrase as plain text, or in an MCP or settings file, stays
  high. Rules pack 2026.11.09.
- KodeKavach reports the official installers of rustup, Homebrew, nvm, uv, Docker, Bun, Deno
  and similar tools piped into a shell as medium instead of high. A line with any other URL
  stays high. Rules pack 2026.11.09.
- KodeKavach no longer reports emoji or Hindi text as hidden characters. A single zero-width
  joiner (U+200D or U+200C) is part of every family emoji and of many Devanagari conjuncts;
  only a run of two or more joiners is reported now. Rules pack 2026.11.09.
- On a case-insensitive disk (macOS, Windows), a repo with two files whose names differ only
  in case always looked changed, so a sync stashed it and could not put the stash back,
  leaving the repo stuck. The sync now downloads the updates, leaves the working tree alone
  and says which file clashes.
- A sync killed between fetching and pinning a repo could leave the old tips of force-pushed
  branches unreachable, so `git gc` could remove them. Every tip is now pinned before the
  fetch. Found by the new chaos tests, which kill a real sync at random points.
- A connection the server dropped mid-transfer ("empty reply from server", "transfer closed",
  "bytes missing", "RPC failed") was reported as an internal error and not retried; it is now a network error.
- Day durations too large for a duration (`106752d` and up) wrapped around to negative
  values; they are now refused. A source owner with a control character, and a remote URL
  with an empty host such as `git@:team/tool`, are refused or treated as generic too.
- `config edit` and `conflicts edit` now open editors installed under a path with spaces,
  such as `"C:\Program Files\Notepad++\notepad++.exe"`, from $VISUAL or $EDITOR.
- An archive on an NFS or SMB share (a NAS, a mapped network drive or a `\\server\share`
  path) no longer uses SQLite's WAL mode, which needs shared memory those shares do not
  provide and could corrupt the archive database. Local disks still use WAL.
- Cancelling a sync on Windows now also stops the helpers git started
  (`git-remote-https`, `ssh`), so none keeps `index.lock` open and fails the next sync.

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
