# The operation log

Every change ChitraGit makes to an archive is written to its operation log: each sync
(when it started, when it finished and what it counted), each scan, tags, favourites,
followed sources, rescues, renames, bundles, resolved conflicts, ignored findings and
KodeKavach runs. Each entry is written in the same database transaction as the change it
describes, so the log never disagrees with the archive.

```sh
chitragit log                     # newest first
chitragit log --since 7d          # the last week
chitragit log --repo alice/notes  # entries about one repo
chitragit log --json              # for scripts
```

## Tamper evidence

Each entry stores a SHA-256 hash of its own fields and of the entry before it, so the
entries form a chain. Changing an entry, or deleting one from the middle, breaks every
hash after it.

```sh
chitragit log --verify
```

`--verify` recomputes the chain from the first entry. If it holds, ChitraGit prints
`Ledger chain intact`. If it is broken, ChitraGit names the first entry that does not
match and exits with code 1. That usually means the database was edited
outside ChitraGit. ChitraGit saves a copy of the database before each upgrade
(`chitragit.db.bak-v<N>`), which you can go back to.

The last entry is also kept outside the database, in `chitragit.db.head`, so entries
removed from the **end** of the log are found too, and so is a database swapped for an
older copy. If you cut the log on purpose, delete that file to accept the shorter log.
`chitragit verify` runs this check along with its others; see
[Verify the archive](verify.md).

## Archives from before the log

The first time a newer ChitraGit opens an older archive, it hashes the events that
archive already has, oldest first, then chains every new entry after them. Nothing is
dropped.

## What the activity feed shows

`chitragit activity` and the TUI timeline show what changed in your repos: new commits,
rescues, renames. Operation entries (their type starts with `op.`) stay out of the feed
because the feed already shows their effects. Ask for them by type if you want them there:
`chitragit activity --type op.sync`.
