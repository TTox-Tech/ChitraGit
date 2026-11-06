# KodeKavach: security audit

```sh
chitragit audit                          # new commits since the last audit (all history the first time)
chitragit audit --history full           # read every commit again
chitragit audit --history head           # only the files at HEAD, fastest
chitragit audit --only 'torvalds/*'      # some repos
chitragit audit --sarif kodekavach.sarif # also write SARIF for GitHub code scanning or your IDE
chitragit audit findings                 # what earlier audits found, without reading repos again
```

KodeKavach reads every repo in the archive and reports **leaked secrets**: API keys,
tokens, passwords in config files and private keys. It reads the whole git history,
so a key that was committed in 2019 and deleted a week later is still found. Bare and
mirror clones work the same as normal ones.

The rules are the [Gitleaks](https://github.com/gitleaks/gitleaks) rule set, built
into ChitraGit, so there is nothing else to install.

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
- Nothing is sent over the network.
- A secret is never stored, logged or printed in full. The database, JSON and SARIF
  output only hold a redacted preview such as `ghp_****`, and the finding ID is
  derived from a hash of the value.
- The audit takes the archive lock, like `sync`, so it never reads a repo halfway
  through an update.

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
checks      = ["secrets"]
history     = "incremental"  # head | incremental | full
fail_on     = "high"
workers     = 0              # repos in parallel, 0 = 4
max_file_kb = 1024           # skip files larger than this
```

Coming next: vulnerable dependencies (offline OSV database), risky GitHub Actions
workflows, poisoned AI-agent files, a KodeKavach screen in the ledger, and audits
after scheduled syncs.
