# Restore

`chitragit restore` puts a repo's branches, remote-tracking branches and tags back exactly as
a [snapshot](snapshots.md) recorded them. Use it to undo a sync you did not want, to get back
a branch an upstream force-push replaced, or to see an old state of a repo.

```sh
chitragit history alice/notes                      # find the snapshot ID
chitragit restore alice/notes --to 41 --dry-run    # see what would change
chitragit restore alice/notes --to 41              # asks, then restores
```

```text
Restore alice/notes to snapshot 41:
  move    refs/heads/main  a81e04c2f6b3 → 3f9c2a1b7d10
  create  refs/heads/spike  c02d9e81aa47
  delete  refs/tags/v2  (at 91be7710c3d2; the snapshot taken first keeps it)
Change 3 ref(s) of alice/notes to match snapshot 41? [y/N] y
✓ restored alice/notes to snapshot 41 (3 refs)
To undo: chitragit restore alice/notes --to 57
```

## What it does

1. **Plans.** It compares the repo's refs now with the snapshot and lists every ref to move,
   create or delete. `--dry-run` stops here.
2. **Records the present.** Before changing anything it takes a snapshot of the current refs
   and pins their commits against `git gc`. That snapshot is the undo: the last line of the
   output is the command that restores it.
3. **Moves the checked-out branch** with git's own checkout, so the files in the working tree
   follow it. Checkout refuses to overwrite anything it would lose.
4. **Moves every other ref in one transaction** (`git update-ref --stdin`) that checks each
   ref's old value: they all move or none does, and a ref that changed since the plan stops
   the whole restore. If this step fails, the checked-out branch is put back.
5. **Logs a `restored` event**, shown by `chitragit history` and in the operation log.

`--json` prints the plan and whether it was applied. Without a terminal, or with `--json`,
pass `--yes`; there is no prompt to answer.

## Uncommitted changes

A restore that would move the checked-out branch refuses to run if the working tree has
uncommitted changes or untracked files:

```text
✗ not restored: main has uncommitted changes
  Hint: commit them, or pass --stash to put them aside and back
```

With `--stash` the changes go through the same stash flow a sync uses: they are put aside,
the restore runs, and they are applied again on the restored branch. If they clash with the
snapshot's files, every ref and the changes are put back exactly as they were, and nothing is
restored.

## Safety notes

- **Nothing is lost.** Refs a restore deletes or moves away are in the snapshot it takes
  first, and their commits are pinned under `refs/chitragit/snap/`.
- **A crash is safe.** If ChitraGit is killed mid-restore, the repo is left consistent: with
  `--stash`, the next sync rolls the checked-out branch and your changes back to how they
  were; otherwise git's checkout either finished or did not. Run the restore again to finish.
- **Refused, not forced:** a branch checked out in another worktree, a snapshot without the
  branch that is checked out, a merge or rebase in progress, an interrupted sync not yet
  recovered, and a snapshot whose commits the repo no longer has (for example, after it was
  cloned again).
- **The next sync still syncs.** A restore moves refs; it does not stop upstream. If upstream
  still has the commits you restored away from, the next sync brings them back in the usual
  safe way (remote-tracking branches move, local branches fast-forward when they can).
