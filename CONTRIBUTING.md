<div align="center">
  <img src="docs/assets/logo.svg" alt="ChitraGit logo" width="72">
  <h1>Contributing to ChitraGit</h1>
  <p><i>Thanks for helping keep the ledger.</i></p>
</div>

Everyone taking part is expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

```sh
git clone https://github.com/TTox-Tech/chitragit
cd chitragit
go test ./...
go run ./cmd/chitragit doctor
```

You need Go 1.26+ and Git 2.30+.

## Ground rules

1. **No data loss, ever.** Any code that changes a working tree must be reversible
   and covered by a test that interrupts it at every step.
2. **Engine first.** Logic lives in `internal/` packages with no UI code.
   `internal/ui/cli` and the TUI only call the engine and render results. A lint
   rule (depguard in `.golangci.yml`) lets `internal/ui` import only `engine`,
   `config`, `core`, `i18n`, `platform` and `version` from `internal/`; anything
   else the UI needs is re-exported from `internal/engine` (`api_*.go`).
3. **All git calls go through `internal/gitx`.** Never call `exec.Command("git", ...)`
   elsewhere. The runner adds the security flags that stop archived repos from
   running code.
4. **All user-facing text goes through `i18n.T`.** Add new strings to
   `internal/i18n/locales/en.toml`.
5. **Errors are `*core.Error`** with a category, a plain-language message and, where
   possible, a hint that tells the user how to fix it.

## How we write code

The best code is the code we never had to write. Each change is the smallest
*complete* change: small because it is necessary, never because a corner was cut.

**Read first.** Read the task and the code it touches. List every place the change must
reach (callers, tests, fixtures, config, docs) and what it could break for users.
That list is the scope. Extra features are not.

**Then take the first rung that works:**

1. Does it need to exist? Skip options and flexibility nobody asked for, and say so in the PR.
2. Is it already in this codebase? Use the helper, package or pattern the code around it uses.
3. Does the Go standard library or the platform do it? Use that.
4. Does a dependency we already have do it? Use it. Never add a dependency for a few lines.
5. Can it be one line a reader gets at a glance? Write one line.
6. Only then, the minimum new code that works.

**Never cut:** validation at trust boundaries, error handling that prevents data loss,
security, accessibility, or anything the issue asks for.

- No abstraction, wrapper, option or "for later" code without a caller today.
  Keep the layers the codebase already has. Deleting code beats adding it.
- New logic with a branch, a loop, a parser, or anything touching data safety or security
  leaves at least one small test. Trivial changes need none.
- Fixing a bug: find every caller of the function first, then fix the root cause once,
  in the shared code.
- Code you move or merge keeps its validation and error handling.
- Comments explain *why*, in one line. A known limit is marked
  `// shortcut: <the limit>, <when to upgrade>` so it can be found later.
- **One ticket, one commit, one PR.** Each ticket (`CG-<epic>.<n>`) is a single commit that
  builds and passes the tests on its own, in its own pull request that says `Closes #<ticket>`.
  A ticket that needs the one before it opens its PR on that ticket's branch; once the earlier
  PR merges, the branch is rebased on `main` and the PR retargeted. The epic issue closes with
  its last ticket.
- **Squash and merge.** A PR holds one commit, so `main` keeps one commit per ticket with the
  same message, and GitHub signs every one.

## Before opening a pull request

```sh
gofmt -l .                       # must print nothing
go vet ./...
go test -race ./...
golangci-lint run                # optional locally, enforced in CI
```

If you changed a command, a flag or a setting, refresh the generated reference pages of
the docs site (a test fails while they are out of date), and run the end-to-end check:

```sh
go test ./internal/ui/cli -run TestReferenceDocs -update-docs
go build -o /tmp/chitragit ./cmd/chitragit && scripts/smoke-test.sh /tmp/chitragit
```

## Docs site and demo

The docs site lives in `website/` (MkDocs) and is published to GitHub Pages from `main`.
To preview it:

```sh
pip install -r website/requirements.txt
cp -r docs/assets website/docs/assets
mkdocs serve -f website/mkdocs.yml
```

The demo GIFs in the README are recorded with [VHS](https://github.com/charmbracelet/vhs)
from the tapes in `docs/demo/`. After changing a tape or a screen they show, run the
Demo GIFs workflow on your branch from the Actions tab; it commits the new GIFs. Its
commit does not start CI, so run the CI workflow on the branch too.

## Translations

Copy `internal/i18n/locales/en.toml` to `<language-code>.toml` (for example `hi.toml`),
translate the values, and keep every `{placeholder}` unchanged. The test suite checks
that translations only use placeholders that exist in English.

## Themes and artwork

ChitraGit uses one theme family with a single accent colour. Before you touch colours,
the TUI look or anything in `docs/assets`, read [docs/brand.md](docs/brand.md): it lists
the palette, the contrast rule that the tests enforce, and how to regenerate the artwork.

## Reporting bugs

Please include the output of `chitragit doctor` and `chitragit version`.
For security issues, see [SECURITY.md](SECURITY.md) instead of opening a public issue.
