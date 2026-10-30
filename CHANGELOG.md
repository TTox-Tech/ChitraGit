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
