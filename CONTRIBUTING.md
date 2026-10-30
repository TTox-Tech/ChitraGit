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
   `internal/ui/cli` and the TUI only call the engine and render results.
3. **All git calls go through `internal/gitx`.** Never call `exec.Command("git", ...)`
   elsewhere. The runner adds the security flags that stop archived repos from
   running code.
4. **All user-facing text goes through `i18n.T`.** Add new strings to
   `internal/i18n/locales/en.toml`.
5. **Errors are `*core.Error`** with a category, a plain-language message and, where
   possible, a hint that tells the user how to fix it.

## Before opening a pull request

```sh
gofmt -l .                       # must print nothing
go vet ./...
go test -race ./...
golangci-lint run                # optional locally, enforced in CI
```

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
