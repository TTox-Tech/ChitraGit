<!-- Generated from the code by TestReferenceDocs in internal/ui/cli. Do not edit. -->
# Commands

Every command works without the interactive ledger, takes `--json` for scripts and
returns the [exit codes](#exit-codes) below. Run `chitragit <command> --help` for the same text.

## Global flags

| Flag | |
|---|---|
| `--accessible` | screen-reader friendly output: no colour, words instead of symbols |
| `--archive string` | path to the archive (default: search upward from the current folder) |
| `--headless` | never ask questions or start the TUI; sync also writes a log (used by schedules) |
| `--json` | machine-readable JSON output |
| `--no-color` | disable colours |
| `-v, --verbose` | show technical details |

## activity

Show what changed across the archive, newest first.

A timeline built from what past syncs recorded: new commits, branches and tags,
history kept after a force-push, and events like rescues and renames.
It reads only the archive database, so it works offline.

```
chitragit activity [flags]
```

| Flag | |
|---|---|
| `--limit int` | show at most this many entries (default `200`) |
| `--owner string` | only repos of this owner |
| `--repo string` | only this repo |
| `--since string` | only changes newer than this, e.g. 24h or 7d |
| `--tag string` | only repos with this tag |
| `--type stringSlice` | only these event types, e.g. refs, rescued, renamed |

## audit

Check the archive for leaked secrets, vulnerable dependencies, risky workflows and poisoned agent files (KodeKavach).

KodeKavach reads every repo in the archive and reports leaked secrets such as API
keys, tokens and private keys, in the files and in the whole git history, so a key
that was committed and later deleted is still found. Bare and mirror repos work too.

Checks:  secrets  keys and tokens in all history
         deps     lockfiles at HEAD (go.mod, package-lock.json, yarn.lock, pnpm-lock.yaml,
                  Cargo.lock, poetry.lock, uv.lock, Pipfile.lock, requirements.txt,
                  Gemfile.lock, composer.lock, gradle.lockfile, packages.lock.json,
                  pubspec.lock) matched against the offline OSV database; download it
                  once with `chitragit audit db update`. Upgraded ones are marked fixed.
         actions  GitHub Actions workflows at HEAD: unpinned actions, script injection,
                  pull_request_target checkouts, write-all tokens and more
         agents   AI agent files at HEAD (agent instructions, skills, rules, MCP configs,
                  hook settings): hidden characters, download-and-run, prompt overrides,
                  exfiltration endpoints, auto-approve settings and more
zizmor and SkillSpector can also run as an opt-in extra (audit.zizmor, audit.skillspector,
off by default), offline and without the repo's own ignore settings.

The audit only reads. It never changes a repo, never runs a repo's own diff drivers
or hooks, and sends nothing about your repos over the network (the only downloads are
the OSV database, see `audit db`, and a weekly signed rules pack, see `audit rules`). A secret is never stored or printed
in full: findings show a redacted preview such as "ghp_****".

By default only commits added since the last audit are read, so a daily audit of a
large archive stays fast. The first audit reads all history.

History:  incremental (default)  new commits only · full  all history again · head  files at HEAD only
Exit code 5 means an open finding is at least as severe as --fail-on (default: high).

```
chitragit audit [repo…] [flags]
```

Examples:

```sh
chitragit audit
  chitragit audit --only 'torvalds/*' --history full
  chitragit audit --sarif kodekavach.sarif --fail-on critical
  chitragit audit --checks deps
```

| Flag | |
|---|---|
| `--checks string` | comma-separated checks to run: secrets, deps, actions, agents (default from audit.checks) |
| `--fail-on string` | exit with code 5 if an open finding is at least this severe (default from audit.fail_on) |
| `--history string` | head \| incremental \| full (default from audit.history) |
| `--only string` | audit only repos whose path matches this glob, e.g. 'torvalds/*' |
| `--sarif string` | also write the findings to this file as SARIF 2.1.0 |
| `--workers int` | repos audited in parallel (default from audit.workers) |

## audit db

Manage the offline vulnerability database for the deps check.

The deps check matches the versions pinned in each repo's lockfiles against a local
copy of the OSV database (osv.dev). `audit db update` downloads it, about 300 MB
in total, into ChitraGit's cache folder. The download is the same whatever your
repos hold, so it tells nobody what you depend on; matching happens on this machine.

```
chitragit audit db
```

## audit db status

Show what the vulnerability database holds and how old it is.

```
chitragit audit db status
```

## audit db update

Download or refresh the vulnerability database.

Downloads the OSV export of every ecosystem the deps check reads (Go, npm, PyPI, crates.io, RubyGems, Packagist, Maven, NuGet, Pub)
and indexes it locally. Each ecosystem is replaced in one step only after its download
is complete and its checksum matches, so a failed or interrupted update keeps the
previous data. Run it again any time; unchanged exports are not re-indexed.

```
chitragit audit db update [flags]
```

Examples:

```sh
chitragit audit db update
  chitragit audit db update --ecosystems npm,PyPI
```

| Flag | |
|---|---|
| `--ecosystems string` | only these ecosystems, comma-separated (default: all) |

## audit findings

List what earlier audits found.

Lists stored findings, most severe first, without reading any repo again.

```
chitragit audit findings [repo…] [flags]
```

| Flag | |
|---|---|
| `--check string` | only this check: secrets, deps, actions or agents |
| `--fixed` | include fixed findings (dependencies that were upgraded since) |
| `--ignored` | include ignored findings |
| `--only string` | only repos whose path matches this glob |
| `--sarif string` | also write them to this file as SARIF 2.1.0 |
| `--severity string` | only findings at least this severe |

## audit ignore

Accept a finding so it stops being reported.

Marks a finding as accepted, for example a test key that was never real. The
finding is kept and shown with --ignored; `audit unignore` brings it back.

```
chitragit audit ignore <finding-id> [flags]
```

| Flag | |
|---|---|
| `--reason string` | why this finding is safe to ignore (required) |

## audit rules

Show the rules pack the actions and agents checks use.

The actions and agents checks read their agent file names, content patterns and
rule settings from a rules pack. One is built into ChitraGit; `audit rules update`
fetches a newer one from ChitraGit's own GitHub Releases. A downloaded pack is used
only if its Ed25519 signature checks out and it is newer than the built-in one.

```
chitragit audit rules
```

## audit rules update

Download a newer signed rules pack.

Downloads the latest rules pack from ChitraGit's GitHub Releases and installs it
if its signature is valid and it is newer than the pack in use. It sends no data
about your archive. If anything fails, the current pack stays in use.

For a machine without internet, download kodekavach-rules.json and
kodekavach-rules.json.sig elsewhere and use --from with the folder that has them.

```
chitragit audit rules update [flags]
```

Examples:

```sh
chitragit audit rules update
  chitragit audit rules update --from ./usb/kodekavach
```

| Flag | |
|---|---|
| `--from string` | install from this folder instead of downloading |

## audit unignore

Report an ignored finding again.

```
chitragit audit unignore <finding-id>
```

## auth

Log in to GitHub, GitLab or Gitea for private repos, stars and higher API limits.

Tokens are kept in your OS keychain (or, where there is none, in a file only you
can read), never in the archive, config or logs. Git itself keeps using your own
credentials and SSH keys; ChitraGit never puts a token into a remote URL.

Tokens are also read from GH_TOKEN / GITHUB_TOKEN (github.com), GITLAB_TOKEN
(gitlab.com), CODEBERG_TOKEN, GITEA_TOKEN (gitea.com), CHITRAGIT_TOKEN_<HOST>
(e.g. CHITRAGIT_TOKEN_GIT_EXAMPLE_COM) and `gh auth token`.

```
chitragit auth
```

## auth login

Log in and save a token.

```
chitragit auth login <github|gitlab|codeberg|gitea|host> [flags]
```

| Flag | |
|---|---|
| `--with-token` | read a personal access token from standard input |

## auth logout

Forget the saved token.

```
chitragit auth logout <github|gitlab|codeberg|gitea|host>
```

## auth status

Show which servers you are logged in to.

```
chitragit auth status [github|gitlab|codeberg|gitea|host]
```

## bundle

Write a single-file backup (git bundle) of repos' full history.

Bundles go to .chitragit/bundles/<repo path>.bundle. A bundle holds every branch,
tag and ChitraGit safety ref; restore one with `git clone <file>.bundle`.

```
chitragit bundle <repo>... | --rescued [flags]
```

| Flag | |
|---|---|
| `--rescued` | bundle every rescued repo |

## clone

Clone one repo into the archive, using the clone layout.

```
chitragit clone <url | provider:owner/repo>
```

## config

View and change settings.

Settings are layered, later wins:

```text
built-in defaults → user config → archive config → CHITRAGIT_* environment → flags
```

Keys are written as section.key, e.g. sync.workers.

```
chitragit config
```

## config edit

Open the config file in your editor (archive config, or --user).

```
chitragit config edit [flags]
```

| Flag | |
|---|---|
| `--user` | edit your personal config instead of the archive's |

## config get

Print the effective value of a setting.

```
chitragit config get <key>
```

## config list

Show every setting with its effective value.

```
chitragit config list
```

## config path

Show where config files live.

```
chitragit config path
```

## config set

Change a setting (in the archive config, or --user).

```
chitragit config set <key> <value> [flags]
```

| Flag | |
|---|---|
| `--user` | write to your personal config instead of the archive's |

## config validate

Check config files for mistakes.

```
chitragit config validate
```

## conflicts

List and resolve clashes between your changes and updates.

When your uncommitted changes clash with an update, sync puts the repo back
exactly as it was and lists it here. Resolving applies the update again and
lays the clashes out in the files, for you to settle file by file or hunk by
hunk: keep mine, take theirs (upstream), or keep both.

```text
chitragit conflicts                         list repos with conflicts
chitragit conflicts start <repo>            apply the update, show the clashes
chitragit conflicts show <repo> [file]      files, or one file's hunks
chitragit conflicts resolve <repo> <file> mine|theirs|both [--hunk N]
chitragit conflicts edit <repo> <file>      fix it in your editor
chitragit conflicts finish <repo>           keep the result as uncommitted changes
chitragit conflicts abort <repo>            put the repo back exactly as it was
chitragit conflicts dismiss <repo>          take it off the list, change nothing
```

Nothing is ever lost: your original changes are kept under
refs/chitragit/resolved/, and an aborted session's work under refs/chitragit/aborted/.

```
chitragit conflicts
```

## conflicts abort

Put the repo back exactly as it was before the session.

```
chitragit conflicts abort <repo>
```

## conflicts dismiss

Take a repo off the conflict list without changing it.

```
chitragit conflicts dismiss <repo>
```

## conflicts done

Mark a file resolved after fixing it by hand.

```
chitragit conflicts done <repo> <file>
```

## conflicts edit

Fix a conflicted file in your editor.

```
chitragit conflicts edit <repo> <file>
```

## conflicts finish

End the session; keep the result as uncommitted changes.

```
chitragit conflicts finish <repo>
```

## conflicts resolve

Settle a conflicted file, or one hunk of it with --hunk.

```
chitragit conflicts resolve <repo> <file> <mine|theirs|both> [flags]
```

| Flag | |
|---|---|
| `--hunk int` | settle only this hunk (numbered as `conflicts show` lists them) |

## conflicts show

List the conflicted files, or show one file's hunks.

```
chitragit conflicts show <repo> [file]
```

## conflicts start

Apply the waiting update and lay out the clashes.

```
chitragit conflicts start <repo>
```

## doctor

Check that your system and archive are ready.

```
chitragit doctor
```

## fav

Mark repositories as favourites (synced first, shown with ★).

```
chitragit fav [repo…] [flags]
```

| Flag | |
|---|---|
| `--only string` | also every repo whose path matches this glob |

## follow

Clone every repo of a user, org or group, now and on every sync.

Sources:

```text
github:torvalds          all repos of a user or org
github:@starred          your starred repos (needs `auth login`)
gitlab:gnome/            a GitLab group, with its subgroups
codeberg:forgejo         Codeberg (Gitea/Forgejo)
gitea@git.example.com:team   a self-hosted server
```

Forks are skipped unless --forks. Repos removed from a source are never deleted
from your archive: they go through the rescue flow instead.

```
chitragit follow <source> [flags]
```

| Flag | |
|---|---|
| `--forks` | also clone forks |
| `--language string` | only repos in this language |
| `--max-size int64` | skip repos larger than this many MB (0 = no limit) |
| `--name string` | only repos whose name matches this glob |
| `--no-archived` | skip repos archived upstream |
| `--no-clone` | only save the source; clone on the next sync |

## health

Show the archive's health: problems, disk usage and recent runs.

Counts the repos that need a look (failing, diverged, dirty, stale…), the
largest repos on disk and the last runs. --card <name> lists the repos behind
one count. --measure first measures every repo's size on disk.

```
chitragit health [flags]
```

| Flag | |
|---|---|
| `--card string` | list the repos behind one count: rescued, failing, conflicts, diverged, rewritten, dirty, stale, never_synced, missing, locked, favourites |
| `--measure` | measure every repo's size on disk first (reads every file) |

## init

Set up a folder as a ChitraGit archive.

Creates a .chitragit folder in the archive root. It holds the archive's database,
settings, logs and backups, so the archive stays portable.

```
chitragit init [path]
```

## lock

Protect a repo: ChitraGit will refuse to delete or move it.

```
chitragit lock <repo>
```

## ls

List repositories in the archive.

```
chitragit ls [flags]
```

| Flag | |
|---|---|
| `--only string` | only repos whose path matches this glob |
| `--status string` | only repos with this status, e.g. diverged, failed, missing |

## scan

Find every repository in the archive.

Walks the archive folder and records every repository it finds: normal clones,
bare/mirror clones, linked worktrees and repos nested inside other repos.
Each repo gets a stable ID saved in its own git config (chitragit.id), so a
moved or renamed folder keeps its history. Folders that disappeared are marked
missing; nothing is ever deleted.

```
chitragit scan
```

## schedule

Run sync automatically every day or week.

Adds a job to your system's own scheduler: cron on Linux, launchd on macOS,
Task Scheduler on Windows. The job runs `chitragit sync --headless` for this
archive, never asks questions, writes a log to .chitragit/logs/ and shows a
desktop notification when it finishes. If a sync is already running when the
job starts, the job exits without touching anything.

```
chitragit schedule
```

## schedule install

Set up the scheduled sync (replaces an existing one).

```
chitragit schedule install (--daily HH:MM | --weekly HH:MM --day <day>) [flags]
```

Examples:

```sh
chitragit schedule install --daily 02:00
  chitragit schedule install --weekly 03:30 --day sun
```

| Flag | |
|---|---|
| `--daily string` | run every day at this time (24-hour HH:MM) |
| `--day string` | day of the week for --weekly, e.g. sun or monday (default sun) |
| `--weekly string` | run once a week at this time (24-hour HH:MM) |

## schedule remove

Stop the scheduled sync for this archive.

```
chitragit schedule remove
```

## schedule status

Show the schedule, the next run and how the last run went.

```
chitragit schedule status
```

## show

Show everything about one repository.

Shows a repo's remotes, status, tags, recent commits, branches and sync history.
<repo> can be its path in the archive, owner/name, its folder name if unique, or its ID.

```
chitragit show <repo> [flags]
```

| Flag | |
|---|---|
| `--commits int` | how many recent commits to show (default `10`) |

## sources

List followed sources.

```
chitragit sources
```

## status

Show the health of every repository, from the last sync.

```
chitragit status
```

## sync

Update repositories from their remotes.

Downloads new commits, branches and tags for every repo, then fast-forwards
branches when that is safe: a fast-forward simply moves a branch ahead to the
new commits, so nothing is merged, rebased or overwritten.

Local changes are put aside (stashed), the branch is updated, and the changes
are put back. If they clash with the update, the repo is restored exactly as it
was and the summary says so. Branches deleted upstream are kept under
refs/chitragit/archived/, and history replaced by a force-push under
refs/chitragit/rewritten/, so the archive never loses a commit.

Repos with local commits, a detached HEAD or a merge in progress still get the
new data, but their branches are left alone, with the reason in the summary.

Modes:  ff (default) download and fast-forward · fetch  download only
        dry-run  ask the remotes what changed, download nothing

```
chitragit sync [flags]
```

| Flag | |
|---|---|
| `--fresh` | if the last sync was interrupted, start over with every repo |
| `--mode string` | ff \| fetch \| dry-run (default from config: sync.mode) |
| `--no-follow` | do not check followed sources for new repos |
| `--no-scan` | skip the quick folder scan before syncing |
| `--only string` | sync only repos whose path matches this glob, e.g. 'torvalds/*' |
| `--resume` | if the last sync was interrupted, finish only the repos it had not done |
| `--workers int` | repos to sync in parallel (default from config: sync.workers) |

## tag

Label repositories with your own tags.

Tags are your own labels, e.g. work, fonts, to-read. Filter with `activity --tag`.

```
chitragit tag
```

## tag add

Add tags to repositories.

```
chitragit tag add <tag[,tag…]> [repo…] [flags]
```

| Flag | |
|---|---|
| `--only string` | also every repo whose path matches this glob |

## tag ls

List tags in use, or one repo's tags.

```
chitragit tag ls [repo]
```

## tag rm

Remove tags from repositories.

```
chitragit tag rm <tag[,tag…]> [repo…] [flags]
```

| Flag | |
|---|---|
| `--only string` | also every repo whose path matches this glob |

## unfav

Remove repositories from favourites.

```
chitragit unfav [repo…] [flags]
```

| Flag | |
|---|---|
| `--only string` | also every repo whose path matches this glob |

## unfollow

Stop following a source (repos already cloned stay).

```
chitragit unfollow <source>
```

## unlock

Remove a repo's protection (asks you to type its path).

```
chitragit unlock <repo> [flags]
```

| Flag | |
|---|---|
| `--confirm string` | the repo path, typed again, to unlock without a prompt |

## version

Show version information.

```
chitragit version
```

## Shell completion

`chitragit completion bash|zsh|fish|powershell` prints a completion script. For example:

```sh
chitragit completion bash > ~/.local/share/bash-completion/completions/chitragit
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | everything succeeded |
| `1` | finished, but some repos failed |
| `2` | bad flags, arguments or configuration |
| `3` | another run holds the archive lock |
| `4` | git is missing or too old, or the system is unusable |
| `5` | `audit` found an open issue at least as severe as `--fail-on` |
| `130` | stopped by Ctrl+C |
