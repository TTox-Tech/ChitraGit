# ChitraGit — Product Requirements Document

> *Named after **Chitragupta**, the divine record-keeper who never forgets a single entry.
> ChitraGit does the same for your repos.*

| | |
|---|---|
| **Status** | Draft v1 — ready for build |
| **Date** | 2026-10-20 |
| **License** | MIT |
| **Release target** | v1.0, all features below (single launch) |
| **CLI name** | `chitragit` (short alias: `chit`) |

---

## 1. Problem

People who keep a local archive of Git repositories (learning material, dependencies, "in case it disappears" copies, whole GitHub orgs) have no good way to:

1. **Keep thousands of clones up to date** without writing fragile scripts.
2. **Not lose their own local work** when updating.
3. **Know what changed** across the archive since last time.
4. **Notice when an upstream repo disappears** (deleted, made private, DMCA'd, renamed). That moment is exactly when the local copy becomes precious.
5. **Browse the archive** comfortably, the way they browse GitHub, but offline and in one place.

GitHub shows you one repo at a time, online only. ChitraGit shows you *your whole collection*, offline, and actively guards it.

## 2. Vision

**"A local GitHub for your archive, but better."** A single cross-platform binary with:

- a **sync engine** that safely updates 5,000+ repos in parallel,
- a **beautiful, mouse-clickable TUI** (GUI-quality, Indian ledger theme, ASCII animations),
- a **full headless CLI** for scripts, servers and schedulers,
- an **archive guardian** that detects and protects repos that vanished upstream.

## 3. Goals and non-goals

### Goals (v1.0)
- G1: Sync 5,000+ repos reliably on Linux, macOS and Windows from one binary.
- G2: **Zero data loss.** No ChitraGit operation may destroy user work or archived history.
- G3: GUI-quality TUI: clickable, animated, themed, fast with 10k rows.
- G4: Every feature usable headless (`--json` output, exit codes).
- G5: Highly configurable through both a Settings screen and a config file.
- G6: Multi-provider: GitHub, GitLab, Gitea/Forgejo/Codeberg, and any plain Git URL.
- G7: Architecture ready for remote/NAS, multi-device sync, cloud backup and a web dashboard.

### Non-goals (v1.0)
- No AI features.
- Not a Git client replacement (no commit/branch authoring UI beyond conflict resolution).
- No hosting or serving repos to others (not a Gitea replacement).
- No per-repo rule engine or plugins/hooks (decided: settings screen + config file only).
- Remote/NAS, multi-device sync, cloud backup and web dashboard are **designed for, not built**.

## 4. Users

ChitraGit is for everyone, with simple defaults and depth for power users.

| Persona | Needs | How ChitraGit serves them |
|---|---|---|
| **Collector developer** (primary) | 100–2,000 repos from many owners, updates weekly | Wizard, one-key sync, activity feed |
| **Digital archivist / data hoarder** | 5,000+ repos, cares about preservation | Rescue detection, protection, bundles, health dashboard |
| **Team / company admin** | Backup of org repos on a server | Headless CLI, scheduling, JSON reports, tokens |
| **Non-technical user** | Wants it to "just work" | Guided wizard, clickable UI, plain-language messages |

## 5. Decision log (from discovery Q&A)

| # | Topic | Decision |
|---|---|---|
| 1 | Target user | Everyone: simple defaults, deep options |
| 2 | Interface | Clickable, GUI-quality TUI with ASCII art animations, plus CLI |
| 3 | Scale | Design for **5,000+** repos |
| 4 | Local changes | GitHub-like: stash local changes, update, re-apply |
| 5 | Tech stack | **Go + Bubble Tea** (Charm ecosystem) |
| 6 | Stash conflict | **Interactive conflict-resolution screen** |
| 7 | GitHub-like views | Repo browser, repo detail page, activity feed, health dashboard (all four) |
| 8 | Install | GitHub Releases, Homebrew, Scoop/Winget, one-line install script (all four) |
| 9 | Vanished upstream | **"Rescued" badge + delete-protection** |
| 10 | Storage | **Both**: archive data in `<archive>/.chitragit/`, personal prefs in user config dir |
| 11 | Auto-clone | **Yes, with login/token** (private + starred repos, higher rate limit) |
| 12 | Platforms | GitHub, GitLab, Codeberg/Gitea/Forgejo, any Git URL |
| 13 | Settings | TUI Settings screen + TOML config file |
| 14 | Automation | Manual runs + "set up schedule" button (OS scheduler) |
| 15 | Interrupted run | **Ask the user**: resume or fresh |
| 16 | Notifications | Desktop notification + TUI/log |
| 17 | Visual style | **Indian ledger theme** |
| 18 | Animation | Rich, with Full / Light / Off switch, auto-Light on SSH/slow terminals |
| 19 | First run | Wizard with Skip option (`--no-wizard`) |
| 20 | Headless | Full CLI mode, same engine |
| 21 | Launch scope | **Everything in this PRD ships in v1.0** |
| 22 | License | MIT |
| 23 | Language | English, i18n-ready |
| 24 | Future-ready | Remote/NAS, multi-device sync, cloud backup, web dashboard |

---

## 6. Feature specification

### F1. Discovery (scan)

Find every repository under the archive root, at any depth.

- Detects: normal repos (`.git/` dir), worktrees and submodules (`.git` file), **bare/mirror repos** (dir containing `HEAD`, `objects/` and `refs/`).
- Does **not** descend into a repo's `.git/`. Nested independent repos inside a repo's working tree *are* found (flagged as "nested").
- Skips by default: `node_modules`, `.venv`, `vendor`, `target`, `.cache`, plus patterns from `<archive>/.chitragitignore` (gitignore syntax) and config.
- Parallel directory walker. Results cached in the DB keyed by directory mtime, so rescans of an unchanged tree are near-instant.
- Each repo gets a **stable UUID**, stored inside the repo as git config `chitragit.id`. This survives moves and renames, and is required for future multi-device sync.
- Repos that vanished from disk since the last scan are marked `missing` (never auto-deleted from the DB).

### F2. Sync engine

The heart of the product. One engine, used by TUI and CLI.

**Pipeline for each repo:**
```
preflight → (smart-skip check) → backup ref → fetch → integrate → post-check → record
```

1. **Preflight:** detect state (clean, dirty, detached, mid-rebase/merge/cherry-pick, locked index, no upstream, shallow, LFS, bare). Unsafe states → `skipped` with a reason (see §8).
2. **Smart skip (the "better than a script" part):** for GitHub/GitLab/Gitea repos, batch-query the provider API (GitHub GraphQL: 100 repos per query) for `pushedAt` and the default-branch head SHA. Repos whose remote head equals the local remote-tracking ref are skipped without any network fetch. A no-change sync of 5,000 GitHub repos becomes ~50 API calls instead of 5,000 fetches. Falls back to a normal fetch for plain URLs or when the API is unavailable.
3. **Fetch:** `git fetch --tags` for all remotes (configurable: `origin` only). **Pruning is off by default** (see "archive-safe refs" below).
4. **Integrate** (only for non-bare repos, if `integrate = "ff"`, the default):
   - Clean tree + fast-forwardable → fast-forward the current branch.
   - Also fast-forward *other* local branches that track a remote, without checkout (`git fetch origin main:main` style update via `update-ref` after an ancestry check).
   - Dirty tree → **stash flow** (F3).
   - Diverged (local commits + new remote commits) → never rebase or merge automatically; mark `diverged` and offer actions in the TUI.
   - Upstream force-pushed (old remote head is no longer an ancestor) → save the old history under `refs/chitragit/rewritten/<branch>/<timestamp>`, then update. Mark `upstream rewritten`. *History is never lost.*
5. **Record:** store old→new SHA for every ref that changed. This feeds the activity feed.

**Archive-safe refs:** when a branch or tag is deleted upstream, the remote-tracking ref is not deleted. It is moved to `refs/chitragit/archived/...` so the archive keeps it. Users can enable plain `--prune` in config.

**Concurrency:**
- Worker pool, default `min(16, 2×CPU)`, configurable.
- Per-host concurrency cap (default 8 for github.com) to avoid secondary rate limits.
- Per-repo timeout (default 10 min) plus low-speed abort (`http.lowSpeedLimit`/`lowSpeedTime`).
- Retries with exponential backoff and jitter for transient network errors (default 3).
- Priority ordering: favourites first, then least-recently-synced.

**Modes:** `fetch` (download only), `ff` (default), `dry-run` (report what would happen, touch nothing).

### F3. Safe stash flow (local changes)

GitHub-Desktop-like behaviour, made safe:

```
1. Write backup ref: refs/chitragit/backup/<timestamp> = HEAD
2. git stash push --include-untracked -m "chitragit-auto <run-id>"
3. fetch + fast-forward
4. git stash pop
   ├─ clean   → done (repo looks exactly as before, plus updates)
   └─ conflict:
        a. Interactive TUI session → open Conflict Screen (F4)
        b. Headless / user declines → ROLLBACK:
             reset to backup ref, re-apply stash (guaranteed clean),
             drop stash, mark repo "conflict pending" with details
```

**Invariant:** after any headless run, every repo is either successfully updated or **byte-for-byte as it was before**. No repo is ever left half-way.

### F4. Conflict resolution screen

Opens from the sync screen or the health dashboard ("Pending conflicts").

- Lists conflicted files with conflict counts.
- Per file: side-by-side **Mine vs Theirs** with syntax highlighting, hunk navigation.
- Per hunk or per file: **Keep mine**, **Take theirs**, **Keep both**, **Open in $EDITOR**, **Open in configured mergetool**.
- Binary files: choose a whole side only.
- Once everything is resolved, ChitraGit restores the user's changes as *uncommitted* changes (the same state the user had before) and drops the stash.
- **Abort** at any time performs the full rollback from F3.
- Pending conflicts stay listed until resolved or dismissed (dismiss = rollback).

### F5. Archive guardian: Rescued repos

- After a fetch fails with "not found" or an auth error, ask the provider API why:
  - `404` → deleted **or** made private
  - `451` → DMCA / legal takedown
  - `301` / new full name → **renamed or transferred**: update the remote URL automatically, log it, no rescue
  - `archived: true` → read-only upstream: "Archived upstream" badge (info only)
- A repo is marked **Rescued** only after **2 consecutive** confirmed failures (configurable) on different runs, to avoid false alarms from outages.
- Rescued repos:
  - get a gold **"RESCUED"** badge and their own filter,
  - are **delete-protected**: ChitraGit refuses to delete or move them, shows a warning in the TUI, and needs a typed confirmation to unprotect,
  - optionally get an automatic **git bundle** backup (`.chitragit/bundles/<owner>/<repo>.bundle`), a single-file copy of full history,
  - record when and why they were rescued (shown in the detail page).
- Users can also protect any repo manually ("Lock").

### F6. Providers, auth and auto-clone

**Provider interface** (one implementation each): GitHub (+ GitHub Enterprise), GitLab (+ self-hosted), Gitea/Forgejo/Codeberg, Generic (plain Git URL: sync only, no API features).

**Auth:**
- `chitragit auth login github`: OAuth **device flow** (shows a code, user approves in browser). Also supports pasting a personal access token, reusing `gh auth token`, or env vars (`GITHUB_TOKEN`, `GITLAB_TOKEN`, `GITEA_TOKEN`).
- Tokens are stored in the **OS keychain** (macOS Keychain, Windows Credential Manager, Secret Service on Linux). If no keychain is available (headless Linux), use a `0600` file with a clear warning.
- Tokens are never written to logs, config or the DB.
- Git operations use the user's existing Git credentials/SSH. ChitraGit never rewrites remote URLs to embed tokens.

**Auto-clone ("follow" sources):**
```
chitragit follow github:torvalds          # all repos of a user/org
chitragit follow github:@starred          # my starred repos
chitragit follow gitlab:gnome/            # a GitLab group (recursive)
chitragit follow codeberg:forgejo
```
- Each sync also checks followed sources and clones new repos.
- Filters per source: include/exclude forks, archived repos, max size, name glob, language.
- Layout template (default `{owner}/{repo}`, option `{host}/{owner}/{repo}`), configurable.
- Clone type: `normal` (default, browsable) or `mirror` (bare, every ref, archival purist).
- Repos removed from a followed source are **never deleted locally**. They follow the Rescue flow.

### F7. TUI: Repo Browser (home screen)

- Virtualised list (smooth with 10k+ rows), grouped by owner/folder or flat.
- Columns: name, owner, provider, status badge, last synced, last upstream activity, size, language, tags.
- **Fuzzy search** (`/`), filters (status, owner, provider, tag, rescued, dirty, diverged, stale, favourites), sort by any column.
- **Mouse:** click to select, double-click to open, click headers to sort, scroll wheel, clickable buttons and tabs. Every action also has a keyboard shortcut.
- Multi-select (space / shift-click) for bulk actions: sync, tag, favourite, lock, bundle.
- Favourites (★) and user-defined tags.

### F8. TUI: Repo Detail page

Tabs: **README** (rendered Markdown) · **Commits** (log with graph) · **Branches & Tags** · **Activity** (this repo's sync history) · **Info**.

Info shows remotes, provider metadata (stars, description, topics, archived flag), size on disk, LFS usage, ChitraGit ID, rescue/lock state, and pending conflicts.

Actions: Sync now · Open folder · Open in editor · Open upstream in browser · Lock/Unlock · Create bundle · Copy path.

### F9. TUI: Activity Feed

A timeline of what changed across the archive, built from recorded ref changes (works offline):

```
Today
  ● torvalds/linux        +142 commits on master · new tag v6.18-rc3
  ● charmbracelet/glow    new release v2.3.0
  ▲ someone/cool-tool     RESCUED: upstream returned 404 (2nd confirmation)
  ↻ old/name → new/name   renamed upstream, remote URL updated
Yesterday
  ...
```
Filter by owner, tag or event type. Click an entry to open the repo detail page at that change.

### F10. TUI: Health Dashboard

Cards (clickable → filtered browser):
- Rescued · Archived upstream · Failing (with last error) · Pending conflicts · Diverged · Upstream rewritten · Dirty working trees · Stale (no upstream activity in N months) · Never synced · Missing on disk
- Disk usage: total, top 20 largest repos, LFS usage, suggested `git gc` candidates (one-click "Optimise" runs `git gc --auto`/`maintenance`).
- Last run summary and run history.

### F11. TUI: Sync screen

- Live progress: overall bar, per-worker lanes showing the current repo and phase (preflight/fetch/merge), throughput, ETA.
- Streaming ledger: each finished repo is written as a new "entry" (✓ updated, · unchanged, ⚠ skipped, ✗ failed, ★ rescued).
- Pause / resume / cancel buttons. Cancel = graceful: finish atomic steps, roll back in-flight integrations, write the journal.
- End-of-run summary card with buttons: "View failures", "Resolve conflicts", "Open activity".

### F12. Interrupted runs (resume)

- Every run writes a **journal** (per-repo status) to the DB as it goes.
- On next start, if the last run is unfinished:
  - **TUI:** dialog "Last sync stopped at 3,200 / 5,000 (2 hours ago). **Resume** · **Start fresh** · **Discard**".
  - **CLI:** `--resume` / `--fresh` flags. With no flag in an interactive terminal, prompt; in a non-interactive context use config `on_interrupted = "resume" | "fresh"` (default `resume`) and log the choice.
- Crash safety: in-flight stash flows are recovered on start (backup ref + stash marker → automatic rollback).

### F13. Settings (TUI screen + config file)

- The Settings screen edits the same TOML files, with live validation and descriptions for every option.
- **Layering** (later wins): built-in defaults → user config (`<UserConfigDir>/chitragit/config.toml`) → archive config (`<archive>/.chitragit/config.toml`) → env vars `CHITRAGIT_*` → CLI flags.
- `chitragit config get|set|edit|validate|path`.
- Unknown keys → warning (not error), so newer configs still load on older versions.

Example:
```toml
[sync]
mode            = "ff"        # fetch | ff | dry-run
workers         = 16
per_host_limit  = { "github.com" = 8 }
timeout         = "10m"
retries         = 3
prune           = false       # archive-safe default
stash_local_changes = true
fetch_all_remotes   = true
lfs             = "auto"      # auto | always | never
submodules      = false
on_interrupted  = "resume"    # used only when we cannot ask

[scan]
max_depth = 0                 # 0 = unlimited
exclude   = ["node_modules", ".venv", "vendor", "target"]
follow_symlinks = false

[guardian]
rescue_confirmations = 2
auto_bundle_rescued  = true
stale_after          = "180d"

[clone]
layout = "{owner}/{repo}"
type   = "normal"             # normal | mirror

[ui]
theme      = "ledger"         # ledger | ledger-light | high-contrast | custom
animations = "auto"           # full | light | off | auto
mouse      = true
language   = "en"

[notify]
desktop = true
on      = ["run_finished", "rescued", "conflict"]
```

### F14. Scheduling

- Settings → "Set up schedule" (or `chitragit schedule install --daily 02:00`).
- Writes a native job: **cron** (Linux), **launchd** plist (macOS), **Task Scheduler** via `schtasks` (Windows). Each job runs `chitragit sync --headless --archive <path>`.
- `schedule status` shows next run, last run result and the log path. `schedule remove` cleans up.
- Scheduled runs never prompt, never open a TUI, and hold the archive lock.

### F15. Notifications

- Desktop notification (native on all 3 OSes) at run end: "Synced 5,012 repos · 143 updated · 3 failed · 1 rescued". Click opens the TUI if supported.
- Immediate notification on a new rescue and on new pending conflicts (configurable).
- Everything is also in the TUI and log files (`.chitragit/logs/`, rotated, 30 days default).

### F16. First-run wizard

Animated welcome (ledger opens, kalam writes "ChitraGit"), then:
1. Choose archive folder (defaults to current dir, with a file picker).
2. Scan, with a live counter ("Found 3,412 repos across 287 owners").
3. Connect providers (optional): GitHub / GitLab / Gitea login.
4. Pick theme and animation level (live preview).
5. Schedule (optional): Off / Daily / Weekly + time.
6. "Sync now?" → Sync screen.

**Skip** is available on every step (defaults apply). `--no-wizard` skips it entirely. The wizard is re-runnable from Settings.

### F17. CLI (headless)

```
chitragit                         # open TUI (wizard on first run)
chitragit init [path]             # register an archive
chitragit scan                    # discover repos
chitragit sync [filters]          # update repos
     --mode fetch|ff|dry-run  --workers N  --only <glob>  --tag <t>
     --resume | --fresh  --headless  --json  --no-color
chitragit status [--json]         # one-line health per repo
chitragit ls [filters] [--json]   # list repos
chitragit show <repo>             # repo details
chitragit activity [--since 7d]   # activity feed
chitragit health [--json]         # health dashboard as text
chitragit conflicts [list|resolve <repo>|abort <repo>]
chitragit follow <source> | unfollow <source> | sources
chitragit clone <url|provider:owner/repo>
chitragit lock|unlock <repo>
chitragit bundle <repo|--rescued>
chitragit auth login|logout|status <provider>
chitragit config get|set|edit|validate|path
chitragit schedule install|status|remove
chitragit doctor                  # environment check (git version, keychain, perms, terminal)
chitragit version
```

**Exit codes:** `0` success · `1` finished with some repo failures · `2` usage/config error · `3` archive locked by another run · `4` environment error (git missing/too old) · `130` interrupted.

**JSON output** has a versioned schema (`"schema": "chitragit/v1"`) for scripting stability.

### F18. Look and feel: Indian ledger theme

- **Palette (dark default):** deep ink-brown background and parchment text, with **kesariya** (saffron) as the *single* accent for primary actions and focus. Everything else is quiet: brass for hints and the Rescued badge, sage for success, amber for warnings, muted **sindoor** red for errors. Every text colour meets WCAG AA contrast (at least 4.5:1) against its background. Plus a light "parchment" variant and a **high-contrast** accessible theme. User themes are loaded from TOML. The exact values live in `internal/ui/theme` and are documented in [`docs/brand.md`](brand.md).
- **Motifs:** ledger rules and margins as panel borders, entries written line by line, page-turn transitions between screens.
- **Animations:** ASCII-art logo reveal on startup; **diya flame** spinner; kalam (pen) "writing" progress; gentle page-turn on navigation; celebratory rangoli burst on a fully clean run (only in Full mode).
- **Animation levels:** Full / Light (spinners + bars only) / Off. **Auto** picks Light over SSH (`SSH_CONNECTION`), in low-capability terminals, or when frame rendering is slow; picks Off for `TERM=dumb` or `NO_COLOR` without a TTY.
- **Graceful degradation:** Unicode box drawing falls back to ASCII on legacy consoles. True colour falls back to 256 or 16 colours. Minimum supported size 80×24 (a compact layout below 100 columns, a "please enlarge" message below 60×20).

### F19. Internationalisation

- All user-facing strings go through a message catalogue (`i18n/en.toml`) from day one. No hard-coded strings in UI code.
- Locale from config → `LANG` → `en`. Hindi and other languages can be added by community translation files.
- Numbers, dates and relative times are locale-aware.

### F20. Distribution

- **GoReleaser** builds for linux/macOS/windows × amd64/arm64.
- **GitHub Releases** with checksums (SHA-256), SBOM and signed checksums.
- **Homebrew tap** (`brew install <owner>/tap/chitragit`), **Scoop bucket**, **Winget** manifest (submitted via PR to winget-pkgs).
- **One-line installer** (`install.sh` for macOS/Linux, `install.ps1` for Windows) that downloads the right release and **verifies the checksum** before installing.
- `chitragit version --check` tells the user if a newer version exists (opt-in, no telemetry).

---

## 7. Architecture

### 7.1 Principles
1. **Engine first, UI second.** All logic lives in a UI-free core. TUI and CLI are thin clients. The future web dashboard and remote/NAS mode are just more clients.
2. **Shell out to system Git** for all write operations (full compatibility with credentials, SSH config, LFS, hooks policy and performance). Pure-Go reads (go-git) are allowed only where measurably faster and safe.
3. **Never trust repo config.** Every Git call runs with hardened flags (see §10).
4. **Everything is an event.** The engine emits typed events (`RepoStarted`, `PhaseChanged`, `RepoFinished`, `RescueDetected`…) that the TUI renders, the CLI prints/serialises, and the store persists.
5. **Pure Go, no CGO**, so cross-compiling to all 6 targets from one CI job is trivial.

### 7.2 Package layout
```
cmd/chitragit/            main; wires app
internal/
  core/                   domain types: Repo, RepoState, Run, RefChange, errors
  engine/                 public engine API (Scan, Sync, Resolve, Follow…) + event bus
  scan/                   parallel walker, ignore rules, repo-type detection
  gitx/                   safe git runner (timeouts, env, hardened -c flags, porcelain parsers)
  syncer/                 worker pool, per-host limiter, pipeline, stash flow, rollback
  conflict/               conflict model, hunk parsing, resolution actions
  guardian/               rescue detection, protection, bundles
  provider/               interface + github/, gitlab/, gitea/, generic/
  auth/                   keychain, device flow, token sources
  store/                  SQLite (pure Go) + migrations; journal; activity
  config/                 TOML layering, schema, validation, env/flags binding
  schedule/               cron / launchd / schtasks backends
  notify/                 desktop notifications
  i18n/                   catalogues + lookup
  ui/tui/                 Bubble Tea app: screens, components, theme, animations
  ui/cli/                 Cobra commands, text and JSON renderers
  platform/               OS specifics: paths, long paths, signals, terminal detection
```

### 7.3 Key libraries
Bubble Tea, Bubbles, Lip Gloss, Glamour (README rendering), Harmonica (spring animations), Chroma (syntax highlighting), Cobra (CLI), `modernc.org/sqlite` (pure-Go SQLite), BurntSushi/toml, `zalando/go-keyring`, `gen2brain/beeep` (notifications), `google/go-github`, GitLab and Gitea official Go clients, `sabhiram/go-gitignore`. *Exact versions are pinned in go.mod at build time.*

### 7.4 Data storage
- **Archive DB:** `<archive>/.chitragit/chitragit.db` (SQLite, WAL mode).
  Tables: `repos`, `remotes`, `runs`, `run_items` (journal), `ref_changes` (activity), `events` (append-only log), `tags`, `favourites`, `rescues`, `conflicts`, `sources`, `schema_migrations`.
- **Archive files:** `.chitragit/config.toml`, `.chitragit/logs/`, `.chitragit/bundles/`, `.chitragit/lock`.
- **User files:** `<UserConfigDir>/chitragit/config.toml` (theme, keys, UI prefs, list of known archives), `<UserCacheDir>/chitragit/` (API caches with ETags).
- The archive is **portable**: copy it to another disk or machine and it keeps its history, tags and rescues.
- Migrations are forward-only and run automatically with a backup copy of the DB taken first.

### 7.5 Future-ready hooks (designed now, built later)
| Future feature | What v1.0 already does to prepare |
|---|---|
| Web dashboard | Engine API + event bus are UI-agnostic. A `chitragit serve` HTTP/WebSocket adapter can expose them unchanged. |
| Remote / NAS archive | Same `serve` adapter, with auth. The TUI talks to a `Client` interface (local in-process now, remote later). |
| Multi-device sync | Stable repo UUIDs and an append-only `events` table make merging metadata between devices possible. |
| Cloud backup | Bundles are produced behind a `BackupTarget` interface (local dir in v1; S3/B2/Drive later). |

---

## 8. Edge cases and handling

### 8.1 Discovery
| Case | Handling |
|---|---|
| Repo nested inside another repo's working tree (not a submodule) | Detected as its own repo, flagged "nested". Parent sync never touches it. |
| Submodules | Not treated as top-level repos. Updated only if `submodules = true`. |
| Worktrees (`.git` file pointing elsewhere) | Recognised. Synced via the main repo only (avoid double fetch). Listed as worktree. |
| Bare / mirror clones | Detected by structure. Sync = `remote update`. No integrate step. |
| Symlink loops / symlinked repos | Symlinks not followed by default. If enabled, inode/realpath de-duplication prevents loops and duplicates. |
| Permission-denied directories | Skipped with a warning in scan results. Scan continues. |
| Archive root is itself inside a repo | Allowed. The root repo is treated as one entry. Warn in `doctor`. |
| Very deep trees / huge non-repo folders | Ignore list plus optional `max_depth`. Walker is parallel and does not stat inside `.git`. |
| Repo moved or renamed on disk | Matched by stable UUID (`chitragit.id`), so history is kept. |
| Repo deleted from disk | Marked `missing`. Never purged automatically. |
| Unicode / spaces / emoji in paths | All paths handled as raw bytes/UTF-8. Never passed through a shell. |
| Windows paths > 260 chars | Use `\\?\` long-path APIs. Run git with `-c core.longpaths=true`. |
| Case-insensitive FS collisions (`Repo` vs `repo` from auto-clone) | Detected before clone. Second one gets a suffix and a warning. |

### 8.2 Git state (preflight)
| Case | Handling |
|---|---|
| Uncommitted changes | Stash flow (F3). |
| Untracked files only | Included in stash (`--include-untracked`). Ignored files left untouched. |
| Merge / rebase / cherry-pick / bisect in progress | **Skip** ("operation in progress"). Never interfere. |
| `index.lock` exists | Skip with message. If the lock is older than 1 h and no git process holds it, offer "remove stale lock" in the TUI only (never automatic). |
| Detached HEAD | Fetch only. No integrate. Status "detached". |
| Branch has no upstream | Fetch only. Status "no upstream". |
| Local commits ahead, remote has new commits (diverged) | Fetch only. Mark `diverged`. TUI offers rebase/merge actions explicitly. |
| Upstream force-push | Old head saved under `refs/chitragit/rewritten/...`, then update. Event in activity feed. |
| Default branch renamed upstream (`master` → `main`) | Detected via `remote set-head --auto` / API. Notify, and offer a one-click local rename. |
| Upstream branch/tag deleted | Kept under `refs/chitragit/archived/...` (prune off by default). |
| Shallow clone | Fetch keeps depth. Offer "unshallow" action (`--unshallow`). Badge "shallow". |
| Partial clone (`--filter`) | Respected. No forced full download. |
| Git LFS repo, `git-lfs` not installed | Fetch Git objects. Warn "LFS objects not downloaded". `doctor` reports it. |
| Empty repo (no commits) | Fetch. Status "empty". Not an error. |
| Repo with no remote | Status "local-only". Never synced, never rescued. |
| Multiple remotes (fork + upstream) | Fetch all (configurable). Integrate only from the branch's configured upstream. |
| Corrupt repo (`fsck` errors, missing objects) | Mark `corrupt` with git's message. Offer "re-clone into new folder" (old folder kept). |
| Stash pop conflict | F3/F4: interactive resolve or full rollback. |
| Crash/power loss mid stash flow | On next start, detect marker + backup ref → automatic rollback, then notify. |

### 8.3 Network and providers
| Case | Handling |
|---|---|
| Offline | Detected up front (DNS/connect probe). Abort early with a clear message. TUI still fully browsable. |
| Flaky network / timeouts | Per-repo timeout + low-speed abort + retry with backoff. Failure is isolated to that repo. |
| Credential prompt would hang | `GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=never`, SSH `BatchMode=yes`. Fails fast as "auth required". |
| Unknown SSH host key | Fail as "host key not trusted" with instructions. Never auto-accept. |
| GitHub rate limit (primary/secondary) | Honour `X-RateLimit-*` / `Retry-After`. Global token bucket. Conditional requests with ETags (free). Fall back to plain fetch. |
| Repo 404 | Ask the API: deleted vs private vs no access. Rescue only after N confirmations. |
| 451 DMCA | Rescued with reason "legal takedown". |
| Renamed / transferred (301) | Update remote URL automatically. Activity event. |
| Archived upstream | Badge only. Sync continues (nothing new will come). |
| Token expired / revoked | Mark provider "needs re-login". API features pause. Plain fetch continues. One notification, not 5,000. |
| Self-signed / corporate TLS | Respect the system trust store and git's `http.sslCAInfo`. Never disable verification. |
| Proxy | Respect `HTTPS_PROXY`/`NO_PROXY` and git's `http.proxy`. |
| Huge repo (tens of GB) | Progress shown per repo. Timeout configurable per repo pattern via config. |

### 8.4 System and environment
| Case | Handling |
|---|---|
| Git not installed / too old | `doctor` and startup check. Require Git ≥ 2.30. Clear install instructions per OS. Exit code 4. |
| "Dubious ownership" (`safe.directory`) | Report clearly, with the exact command to fix. Never add `safe.directory=*` silently. |
| Two ChitraGit runs on the same archive (e.g. schedule + manual) | Lock file with PID + hostname. Second run: exit code 3 (CLI) or "a sync is already running, watch it?" (TUI). Stale lock (dead PID) auto-cleared. |
| Disk full / low space | Check free space before the run and between repos (threshold configurable). Stop gracefully, journal intact. |
| External drive unplugged mid-run | I/O errors → stop run, mark interrupted. Resume when the archive is back. |
| Laptop sleep / resume | Timeouts based on progress, not wall clock. Stalled connections are retried. |
| Ctrl+C | First press: graceful stop (finish atomic steps, roll back in-flight integrations, save journal). Second press: immediate stop (journal still consistent thanks to per-step commits). |
| Windows antivirus slowing git | `doctor` detects Defender and suggests excluding the archive folder. |
| Network filesystem (SMB/NFS) archive | Supported. SQLite WAL may be unsafe on NFS → auto-switch DB to rollback-journal mode and warn. |
| Clock skew | Relative times only for display. Ordering uses DB sequence numbers. |
| CRLF / `core.autocrlf` differences | ChitraGit never changes the user's autocrlf. Uses the repo's own settings. |

### 8.5 Terminal / UI
| Case | Handling |
|---|---|
| Tiny terminal | Compact layout under 100 cols. Message under 60×20. Re-layout on resize. |
| No mouse support (tmux without mouse, some SSH) | Everything works by keyboard. Mouse is optional. |
| No colour / `NO_COLOR` / dumb terminal | Monochrome theme. Animations off. ASCII borders. |
| Legacy Windows console | Detect VT support. Fall back to ASCII and 16 colours. |
| Screen-reader users | `--accessible` mode: no animations, linear text output, explicit labels. |
| 10k+ repos in list | Virtualised rendering. Filtering and search done in the DB/index, not by re-rendering. |
| Very long repo names / Unicode width | Width-aware truncation (East Asian wide characters, emoji). |
| Stdout not a TTY (piped) | Automatically headless text output. TUI never starts. |

---

## 9. Error handling strategy

1. **Isolation:** a failure in one repo never stops the run. Errors are collected per repo.
2. **Typed errors:** every failure maps to a category, and each category has a plain-language message, a technical detail, and a suggested fix:
   `network`, `auth`, `not_found`, `rate_limited`, `timeout`, `unsafe_state`, `conflict`, `corrupt`, `disk`, `permission`, `config`, `internal`.
3. **Plain language first:** the TUI shows "GitHub says this repo no longer exists. We've kept your copy safe." with "Show git output" expandable.
4. **Retry policy:** only `network`, `timeout` and `rate_limited` are retried. Others fail fast.
5. **Rollback guarantee:** any step that modifies a working tree is preceded by a backup ref and is reversible.
6. **No silent failures:** every skip has a reason that is shown in the summary, the JSON and the logs.
7. **Logs:** structured (JSON lines) in `.chitragit/logs/`, human summary per run. `--verbose` mirrors them to stderr. Tokens are redacted.
8. **Panics:** recovered at the worker boundary, logged with stack, repo marked `internal` error, run continues. The crash report path is shown with a "report a bug" link (no automatic upload).

---

## 10. Security and privacy

- **Hardened Git calls** (repos in an archive are untrusted content):
  `-c core.hooksPath=<empty dir>` (no repo hooks run on merge/checkout), `-c core.fsmonitor=false`, `-c protocol.file.allow=user`, `-c submodule.recurse=false` unless enabled, `GIT_CONFIG_NOSYSTEM` is not forced (respect the admin), but repo-local `core.sshCommand`/`credential.helper` overrides are logged.
- No shell invocation, ever: Git is executed with argument arrays.
- Tokens in the OS keychain only. Redacted from logs and errors.
- **No telemetry.** Network calls go only to the user's Git remotes and configured provider APIs, plus the opt-in update check.
- The one-line installer verifies checksums. Release checksums are signed.
- Bundles and the DB inherit archive folder permissions. `.chitragit/` is created with `0700` on Unix.

---

## 11. Non-functional requirements

| Area | Target |
|---|---|
| Scan, 5,000 repos, SSD, warm cache | < 3 s |
| Scan, 5,000 repos, cold | < 30 s |
| No-change sync, 5,000 GitHub repos (smart skip) | < 2 min |
| Full fetch, 5,000 repos, 5% changed, 100 Mbps | < 15 min (network bound) |
| TUI frame time with 10k rows | < 16 ms (60 fps) |
| Memory, 10k repos loaded | < 200 MB |
| Binary size | < 40 MB |
| Startup to first frame | < 300 ms |
| Supported OS | Linux (glibc + musl), macOS 12+, Windows 10/11 |
| Supported Git | ≥ 2.30 |

---

## 12. Quality and testing

- **Unit tests** for parsers, config layering, error mapping, provider clients (recorded HTTP fixtures).
- **Integration tests** against real Git: generate fixture archives (local `file://` remotes) covering every edge case in §8 (dirty, diverged, force-push, deleted branch, conflicts, LFS, shallow, bare, nested, worktrees…).
- **Data-safety tests:** property tests that run random interruption points through the stash flow and assert the "updated or untouched" invariant.
- **TUI golden tests** with Bubble Tea's test harness (screens rendered to text snapshots at several sizes and themes).
- **Fuzzing** of porcelain parsers.
- **Benchmarks:** synthetic 5,000- and 20,000-repo archives in CI (nightly).
- **CI matrix:** ubuntu, macOS, windows × Go stable, with race detector, `golangci-lint`, `govulncheck`.
- **Release checklist** automated via GoReleaser plus a smoke test of each artifact.

---

## 13. Build plan

v1.0 ships everything above. Internal build order (each milestone is merged and tested before the next):

| Milestone | Contents |
|---|---|
| **M0 Foundation** | Repo, CI matrix, lint, config layering, SQLite store + migrations, `gitx` hardened runner, i18n scaffold, `doctor` |
| **M1 Engine** | Scan (all repo types, ignore rules, UUIDs), sync pipeline, worker pool, per-host limits, retries, CLI `scan/sync/status/ls`, JSON output, exit codes |
| **M2 Safety** | Backup refs, stash flow, rollback, archive-safe refs, rewritten-history capture, journal + resume, lock file, crash recovery, data-safety test suite |
| **M3 TUI core** | App shell, ledger theme (3 themes), animation system (Full/Light/Off/Auto), mouse, Repo Browser, Sync screen |
| **M4 TUI depth** | Repo Detail, Activity Feed, Health Dashboard, bulk actions, favourites/tags |
| **M5 Providers** | Provider interface, GitHub/GitLab/Gitea/Generic, auth + keychain + device flow, smart skip, rescue detection, rename-follow, bundles, follow/auto-clone |
| **M6 Conflicts** | Conflict model, Conflict Screen, CLI `conflicts` |
| **M7 Experience** | First-run wizard, Settings screen, scheduling (3 OS), notifications, accessibility mode |
| **M8 Ship** | GoReleaser, Homebrew tap, Scoop bucket, Winget manifest, install scripts, README + demo GIFs (VHS), docs site, launch |

## 14. Launch plan

- **Pre-launch:** confirm the `chitragit` name is free on GitHub, Homebrew, Scoop and Winget. Reserve the GitHub org/repo and tap repo.
- **README:** Chitragupta story, animated demo GIF (recorded with VHS), 3-command quickstart, feature tour, comparison table vs. plain scripts / `mr` / `gita` / `ghorg`.
- **Channels:** Show HN, r/DataHoarder (perfect audience), r/selfhosted, r/golang, r/commandline, Charm community showcase, dev.to / Hashnode launch post, X/Mastodon/LinkedIn.
- **Community:** CONTRIBUTING.md, issue templates, "good first issue" labels (translations, themes), Code of Conduct, Discussions enabled.

## 15. Success metrics (first 90 days)

- 0 confirmed data-loss bugs (hard requirement).
- Sync of a 5,000-repo archive meets §11 targets on all 3 OSes.
- 1,000+ GitHub stars, 20+ external contributors, 3+ community translations/themes.
- < 5% of issues are install problems.

## 16. Open questions

1. GitHub owner for the public repo (personal account or a new `chitragit` org)?
2. OAuth device flow needs a registered GitHub OAuth App (client ID). Who owns it?
3. macOS binary notarisation and Windows code signing: buy certificates now, or rely on Homebrew/Scoop (which avoid the Gatekeeper/SmartScreen warnings) at launch?
4. Default for `on_interrupted` in non-interactive runs: proposed `resume`. OK?
5. Should Rescued-repo bundles be on by default (uses disk space)? Proposed: yes.
