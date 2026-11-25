# Verify the archive

`chitragit verify` checks that the archive is whole. It only reads, so it is safe to run
at any time, even while you work. As in git, `chit fsck` does the same thing.

```sh
chitragit verify          # quick: a few seconds to a few minutes
chitragit verify --full   # also reads every git object, slower on a big archive
chitragit verify --json   # each check's result, for scripts
```

It exits `0` when every check passes and `1` when one fails. Nothing is repaired: it tells
you what is wrong and leaves the fix to you.

## The four checks

| Check | What it looks at | What a failure means |
|---|---|---|
| **ledger** | recomputes the [operation log](operation-log.md)'s hash chain, and compares its last entry with the copy kept outside the database (`chitragit.db.head`) | an entry was edited or deleted outside ChitraGit, or the database was swapped for an older copy |
| **pinned** | every commit a [snapshot](snapshots.md) points at is still in its repo, and pinned under `refs/chitragit/snap/` | a missing object means that snapshot can no longer be restored; an unpinned one may be removed by the next `git gc` |
| **bundles** | every backup under `.chitragit/bundles` passes `git bundle verify` and unpacks cleanly, and every bundle a rescued repo points at still exists | the backup is cut short or damaged, or was deleted |
| **fsck** | runs `git fsck` on every repo. By default it checks that every ref reaches a whole history; `--full` also reads and hashes every object | a missing object (quick check) or a damaged one (`--full`) |

A repo that is gone from disk is counted, not checked, and worktrees are skipped because
their main repo holds the objects.

## Run it on a schedule

```sh
chitragit schedule install --daily 02:00 --verify
```

The scheduled job then runs `chitragit sync --headless --verify`. After each sync it runs
the quick checks, and if one fails you get a desktop notification. Run `chitragit verify`
to see what is wrong. See [Scheduled sync](scheduling.md).

## When a check fails

- **ledger:** ChitraGit saves a copy of the database before each upgrade
  (`chitragit.db.bak-v<N>`), which you can go back to. If you cut entries from the end on
  purpose, delete `chitragit.db.head` to accept the shorter log.
- **pinned:** the problem lists the commits. An unpinned one is still there; pin it again
  in that repo with `git update-ref refs/chitragit/snap/<sha> <sha>`. A missing one is gone,
  so that one snapshot can no longer be restored. Other snapshots are not affected.
- **bundles:** write a fresh backup with `chitragit bundle <repo>`, or
  `chitragit bundle --rescued` for every rescued repo.
- **fsck:** git's own message is shown for each repo. If the remote still has the repo, a
  fresh clone next to the damaged one is the safest fix. Keep the damaged copy until you
  have checked the new one.
