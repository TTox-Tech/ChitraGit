# Changelog

All notable changes to ChitraGit are listed here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first public preview: the headless sync engine and its safety layer
(milestones M0 to M2).

### Added

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
