## What does this change?

<!-- One or two sentences. Link the issue it fixes, e.g. "Fixes #12". -->

## How was it tested?

<!-- Commands you ran and what you checked. -->

## Checklist

- [ ] `gofmt -l .` prints nothing, `go vet ./...` and `go test -race ./...` pass
- [ ] Code that changes a working tree is reversible and has a test that interrupts it
- [ ] Git calls go through `internal/gitx`; user-facing text goes through `i18n.T`
- [ ] `CHANGELOG.md` has a line under "Unreleased" if users will notice this change
