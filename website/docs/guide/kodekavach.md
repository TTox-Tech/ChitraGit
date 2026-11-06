# KodeKavach: security audit

```sh
chitragit audit                          # new commits since the last audit (all history the first time)
chitragit audit --history full           # read every commit again
chitragit audit --history head           # only the files at HEAD, fastest
chitragit audit --only 'torvalds/*'      # some repos
chitragit audit --sarif kodekavach.sarif # also write SARIF for GitHub code scanning or your IDE
chitragit audit findings                 # what earlier audits found, without reading repos again
chitragit audit db update                # download the offline vulnerability database for the deps check
chitragit audit --checks deps            # only vulnerable dependencies
chitragit audit --checks actions,agents  # only workflows and AI agent files
chitragit audit rules update             # fetch the newest signed rules pack
```

KodeKavach reads every repo in the archive and reports **leaked secrets**: API keys,
tokens, passwords in config files and private keys. It reads the whole git history,
so a key that was committed in 2019 and deleted a week later is still found. Bare and
mirror clones work the same as normal ones.

The detector is ChitraGit's own code, with nothing else to install and no other
program run. Its patterns come from the open
[Gitleaks](https://github.com/gitleaks/gitleaks) rule set (MIT licence), converted
into ChitraGit's rule file, so it knows the same 200+ key formats.

It also reports **dependencies with known vulnerabilities** (the `deps` check), see
[below](#vulnerable-dependencies).

## What a finding looks like

```
CRITICAL 8f7580be  GitHub Personal Access Token  [new]
         me/app  .env:1  ghp_****
         commit ad7cf0dcb0 · Dev · 2019-03-14
         Treat it as leaked and rotate it. It is in the repo's history since 2019-03-14; deleting the file does not remove it.
```

The same secret in many commits or files is **one** finding, shown where it first
appeared, with a count of how often it was seen.

| Severity | Meaning |
|---|---|
| critical | a key with direct access to a cloud account, code host, payments or a private key |
| high | a key or token in a provider's own format |
| medium | a generic pattern such as `api_key = "…"`; check it before acting, it is often test data |

## It only reads

- No repo is changed: no checkout, no config write, nothing in the working tree or `.git`.
- A repo's own diff drivers, textconv filters and hooks never run.
- `# gitleaks:allow` comments and `.gitleaksignore` files inside a repo are **not**
  trusted, because a malicious repo could use them to hide its own secrets.
- Nothing about your repos is sent over the network. The only downloads are the
  vulnerability database, when you ask for it, and the signed rules pack (below),
  plain GET requests that are the same for everyone.
- A secret is never stored, logged or printed in full. The database, JSON and SARIF
  output only hold a redacted preview such as `ghp_****`, and the finding ID is
  derived from a hash of the value.
- The audit takes the archive lock, like `sync`, so it never reads a repo halfway
  through an update.

## Vulnerable dependencies

The `deps` check reads the lockfiles at each repo's HEAD, straight from git's object
store, so bare repos work and no working tree is touched:

| Ecosystem | Files |
|---|---|
| Go | `go.mod` (with `replace`) |
| npm | `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock` (classic and Berry), `pnpm-lock.yaml` |
| PyPI | `poetry.lock`, `uv.lock`, `pdm.lock`, `Pipfile.lock`, `requirements*.txt` (exact `==` pins only) |
| crates.io | `Cargo.lock` |
| RubyGems | `Gemfile.lock`, `gems.locked` |
| Packagist | `composer.lock` |
| Maven | `gradle.lockfile` |
| NuGet | `packages.lock.json` |
| Pub | `pubspec.lock` |

Lockfiles under `node_modules`, `vendor` and virtualenv folders are copies of other
projects and are skipped. A manifest without a lockfile (such as a bare
`package.json`) does not say which versions are installed, so it is not read.

Every pinned version is matched against a local copy of the
[OSV database](https://osv.dev), which brings together GitHub's advisories, the
ecosystems' own databases and known malicious packages. Download it once, from the
first-run wizard or with:

```sh
chitragit audit db update     # about 300 MB, into ChitraGit's cache folder
chitragit audit db status     # what it holds and how old it is
```

The download is the same for everyone, whatever your repos hold, so it tells nobody
what you depend on, and matching happens on your machine. Each ecosystem is replaced
in one step only after its download is complete and its checksum matches, so a
failed update keeps the previous data. `audit` reminds you when the database is more
than a week old.

```
HIGH     2c1f7a9e  lodash 4.17.15: Prototype Pollution in lodash  [new]
         me/web  package-lock.json  lodash@4.17.15 · CVE-2020-8203 · CVSS 7.4
         Upgrade lodash to 4.17.19 or later. Details: https://osv.dev/vulnerability/GHSA-p6mc-m468-83gw
```

- The same vulnerability published by several databases (a GHSA, a PYSEC and a CVE)
  is one finding.
- Severity comes from the advisory's own rating, else its CVSS v3 score. A known
  malicious package version is always **critical**; an advisory without any rating is
  **medium**.
- The finding ID depends on the repo, the lockfile, the package and the
  vulnerability, not the version, so moving from one affected version to another
  keeps the finding and any ignore.
- When a later audit no longer sees it (the dependency was upgraded or removed), the
  finding is marked **fixed**. It is kept, not deleted: `audit findings --fixed`
  shows it, and it opens again if the old version comes back.
- Without a database, a plain `chitragit audit` skips the deps check with a note;
  `--checks deps` stops with a hint instead. A repo whose lockfile needs an ecosystem
  that is not downloaded is reported as failed, never as clean.

## GitHub Actions workflows (`actions`)

The `actions` check reads `.github/workflows/*.yml` and every `action.yml` at HEAD:

| Rule | Severity | What it means |
|---|---|---|
| `gha-pr-target-checkout` | critical | a `pull_request_target` or `workflow_run` job checks out the pull request's code, so a fork can steal secrets |
| `gha-script-injection` | high | `${{ github.event.issue.title }}` and similar values go straight into a `run:` script (medium for `inputs.*`) |
| `gha-write-all` | high | `permissions: write-all` |
| `gha-insecure-commands` | high | `ACTIONS_ALLOW_UNSECURE_COMMANDS` turns set-env back on |
| `gha-unpinned-action` | medium | an action used by tag or branch instead of a commit SHA (low for `actions/*`, `github/*` and container images) |
| `gha-secrets-inherit` | medium | `secrets: inherit` to another repo's reusable workflow |
| `gha-self-hosted-pr` | medium | pull request code runs on a self-hosted runner |
| `gha-default-permissions` | low | no `permissions:` block, so the token gets the default scopes |

## AI agent files (`agents`)

Coding agents read instruction files, skills, rules, MCP server configs and hook
settings from the repo and act on them. A poisoned one makes the agent run commands
or leak code as soon as you open the repo. The `agents` check knows the files of
every common agent (AGENTS.md, skills, Copilot, Cursor, Windsurf, Cline, Roo, Gemini,
Codex, Continue, Kiro, Amazon Q, Junie, Zed, OpenCode and more; `chitragit audit rules`
shows the count) and looks for:

- invisible or text-reversing Unicode characters
- download-and-run commands (`curl … | sh`, `iex (iwr …)`)
- "ignore previous instructions" and instructions to hide things from the user
- data-collection endpoints (webhook.site, ngrok, Discord webhooks and similar)
- references to SSH keys, cloud credentials and key stores
- long encoded blobs and instructions hidden in HTML comments
- settings that turn off permission prompts or pre-approve every shell command
- hooks that run network commands
- MCP servers fetched without a pinned version, reached over plain HTTP, or started as an inline script

Only agent files are checked, so the same text in a README is not reported.

## The rules pack

Agent file names, the content patterns and per-rule settings live in a **rules pack**.
One is built into ChitraGit. `chitragit audit rules update` downloads a newer one from
ChitraGit's GitHub Releases, and with `audit.rules_update = "weekly"` (the default)
`chitragit audit` looks once a week by itself. A downloaded pack is used only if its
Ed25519 signature matches the key built into ChitraGit and it is newer than the pack
in use; otherwise the built-in pack is used. The signature is checked again every time
the pack is loaded, so a changed file on disk is never trusted.

For a machine without internet, download `kodekavach-rules.json` and
`kodekavach-rules.json.sig` from the `kodekavach-rules` release elsewhere, then run
`chitragit audit rules update --from <folder>`.

## zizmor and SkillSpector

The built-in rules above are the real check and need nothing installed. As an opt-in
extra, KodeKavach can also run [zizmor](https://docs.zizmor.sh) on the workflows (rules
named `zizmor/…`) and [SkillSpector](https://github.com/NVIDIA/skillspector) on folders
with a `SKILL.md` (`skillspector/…`). Both are off by default: set `audit.zizmor` or
`audit.skillspector` to `"auto"` (run it when installed) or `"on"` (fail when it is
missing). `chitragit doctor` shows whether they are installed. When zizmor reports the same
problem as a built-in rule in the same file, only the zizmor finding is kept.

Each tool gets a copy of just those files in a temporary folder, never the repo
itself. It runs without a shell, with a time limit, with a clean environment (no
tokens or API keys) and with its network pointed at a dead proxy. zizmor runs with
`--offline --no-config --no-ignores`, so a repo cannot switch rules off for itself;
SkillSpector runs with `--no-llm`.

## Ignoring a finding

```sh
chitragit audit ignore 8f7580be --reason "test token, never valid"
chitragit audit findings --ignored
chitragit audit unignore 8f7580be
```

An ignored finding stays in the database and stays ignored when a later audit finds
it again.

## Exit codes and scripts

`audit` exits with `5` when this run saw an open finding at least as severe as
`--fail-on` (default `high`, setting `audit.fail_on`), `1` when some repos could not be
read, and `0` otherwise. With `--json` the output has `"kind": "audit"`.

## Settings

```toml
[audit]
checks       = ["secrets", "deps", "actions", "agents"]
history      = "incremental"  # head | incremental | full
fail_on      = "high"
workers      = 0              # repos in parallel, 0 = 4
max_file_kb  = 1024           # skip files larger than this
osv_db       = "offline"      # offline | off (skip the deps check)
rules_update = "weekly"       # weekly | off
zizmor       = "off"          # off | auto | on
skillspector = "off"          # off | auto | on
```

The `deps`, `actions` and `agents` checks look at HEAD only. An incremental audit skips a repo
for `actions` and `agents` when its HEAD, rules pack and tool versions are the same as last time.

Coming next: a KodeKavach screen in the ledger, and audits after scheduled syncs.
