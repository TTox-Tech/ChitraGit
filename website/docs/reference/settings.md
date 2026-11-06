<!-- Generated from the code by TestReferenceDocs in internal/ui/cli. Do not edit. -->
# Settings

Settings are layered: built-in defaults, then your personal `config.toml`, then the
archive's `.chitragit/config.toml`, then `CHITRAGIT_*` environment variables, then flags.
Change them with `chitragit config set <key> <value>` or on the Settings screen.

Every key can also be set from the environment: `sync.workers` is `CHITRAGIT_SYNC_WORKERS`.
Lists are comma separated; maps use `k=v,k2=v2`.

## [audit]

| Key | Default | |
|---|---|---|
| `audit.checks` | `secrets,deps` | checks to run: secrets, deps |
| `audit.history` | `incremental` | head \| incremental \| full (incremental reads only commits added since the last audit) |
| `audit.fail_on` | `high` | exit with code 5 when an open finding is at least this severe: low \| medium \| high \| critical |
| `audit.workers` | `0` | repos audited in parallel (0 = 4) |
| `audit.max_file_kb` | `1024` | skip files (or one commit's change to a file) larger than this, in KiB (0 = no limit) |
| `audit.osv_db` | `offline` | offline (match dependencies against the downloaded OSV database) \| off (skip the deps check) |

## [clone]

| Key | Default | |
|---|---|---|
| `clone.layout` | `{owner}/{repo}` | folder layout, e.g. {owner}/{repo} or {host}/{owner}/{repo} |
| `clone.type` | `normal` | normal \| mirror |

## [guardian]

| Key | Default | |
|---|---|---|
| `guardian.rescue_confirmations` | `2` | failed runs before a repo is marked Rescued |
| `guardian.auto_bundle_rescued` | `true` | write a git bundle backup of rescued repos |
| `guardian.stale_after` | `180d` | no upstream activity for this long = stale |

## [notify]

| Key | Default | |
|---|---|---|
| `notify.desktop` | `true` |  |
| `notify.on` | `run_finished,rescued,conflict` | run_finished \| rescued \| conflict |

## [providers]

| Key | Default | |
|---|---|---|
| `providers.hosts` |   | self-hosted servers, e.g. git.example.com=gitea (github \| gitlab \| gitea) |
| `providers.smart_skip` | `true` | ask the GitHub API which repos changed and fetch only those (needs a login) |
| `providers.github_client_id` |   | OAuth app client ID for browser login with 'auth login github' |

## [scan]

| Key | Default | |
|---|---|---|
| `scan.max_depth` | `0` | 0 = unlimited |
| `scan.exclude` | `node_modules,.venv,vendor,target,.cache` | directory names or globs to skip |
| `scan.follow_symlinks` | `false` |  |

## [sync]

| Key | Default | |
|---|---|---|
| `sync.mode` | `ff` | fetch \| ff \| dry-run |
| `sync.workers` | `0` | parallel repos (0 = auto: min(16, 2×CPU)) |
| `sync.per_host_limit` | `github.com=8` | max parallel operations per host |
| `sync.timeout` | `10m` | per-repo timeout |
| `sync.retries` | `3` | retries for network errors |
| `sync.prune` | `false` | delete refs removed upstream (off keeps them archived) |
| `sync.stash_local_changes` | `true` | stash local changes, update, re-apply |
| `sync.fetch_all_remotes` | `true` | fetch every remote, not just origin |
| `sync.lfs` | `auto` | auto \| always \| never |
| `sync.submodules` | `false` | also update submodules |
| `sync.on_interrupted` | `resume` | resume \| fresh (used when ChitraGit cannot ask) |

## [ui]

| Key | Default | |
|---|---|---|
| `ui.theme` | `ledger` | ledger \| ledger-light \| high-contrast |
| `ui.animations` | `auto` | auto \| full \| light \| off |
| `ui.mouse` | `true` |  |
| `ui.language` | `en` | UI language code, e.g. en |
| `ui.accessible` | `false` | screen-reader mode: no colour or animation, words instead of symbols |

