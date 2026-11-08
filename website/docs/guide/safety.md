# How sync keeps your work safe

ChitraGit has one rule above all others: **after a sync, every repo is either updated or
exactly as it was.** No local change, commit or branch is ever lost.

![Sync pipeline](../assets/sync-pipeline-dark.svg#only-dark)
![Sync pipeline](../assets/sync-pipeline-light.svg#only-light)

## The pipeline

- **Scan** finds every repo at any depth: normal clones, bare and mirror clones, linked
  worktrees and repos nested inside other repos. Each one gets a stable ID stored in its
  own git config, so moving or renaming a folder keeps its history.
- **Fetch** downloads new commits, branches and tags from every remote. A branch deleted
  upstream is kept under `refs/chitragit/archived/`, and history replaced by an upstream
  force-push under `refs/chitragit/rewritten/`, so `git log <that ref>` always gets it
  back. Set `sync.prune = true` for plain pruning instead.
- **Integrate** fast-forwards a branch only when that is safe, meaning the branch simply
  moves ahead to the new commits. Branches that are not checked out are moved too,
  without touching any files. Uncommitted and untracked changes are stashed, the branch
  is updated and the changes are put back, staged files still staged. If they clash, the
  repo is restored **exactly** as it was and the summary names the files.
- **Record** writes what happened to the archive database, which feeds `status`,
  `activity`, `health` and the ledger.

## Crashes, power cuts and Ctrl+C

Every run is journaled. If ChitraGit is killed while updating a repo, the next run puts
that repo back the way it was before it does anything else. An interrupted sync can
continue where it stopped (`--resume`) or start over (`--fresh`); `sync.on_interrupted`
sets the default.

Only one run can work on an archive at a time. A second one exits with code `3`.

## A hardened git

Every git call goes through one runner that turns off hooks and `core.fsmonitor` inside
archived repos, so a repo you cloned can never run code on your machine during a sync,
and no credential prompt can hang a run.

The same runner checks every object a clone or fetch receives (`transfer.fsckObjects`),
refuses paths that disguise a `.git` folder on Windows or macOS, and turns off the `ext::`
and `fd::` transports. A repo that sends unsafe objects fails with "the remote sent unsafe
or malformed git objects" and nothing is written. Old commits with odd author or date
formatting are still accepted.

Git LFS files stay as small pointer files on checkout, because a repo's `.gitattributes`
would otherwise decide what your git-lfs downloads. Set `sync.lfs = "always"` to download
them.

`chitragit doctor` warns when your git lacks the fix for a known flaw that a hostile repo
can use (CVE-2024-32002, CVE-2025-48384). Both need submodules, so until you update git,
ChitraGit does not fetch submodules even if `sync.submodules` is on.

## Every status, explained

When a repo is not fast-forwarded, the summary says why. The new data is downloaded in
every case except `busy`, `local_only` and `failed`.

| Status | What it means |
|---|---|
| `ok` | up to date, or moved ahead safely |
| `ahead` | you have local commits that are not pushed yet; nothing to apply |
| `conflict` | your local changes clash with the update; the repo was put back exactly as it was. See [Conflicts](conflicts.md) |
| `resolving` | a conflict resolution is open; sync leaves the repo alone until you `finish` or `abort` it |
| `dirty` | uncommitted changes and `sync.stash_local_changes` is off; updates downloaded but not applied |
| `diverged` | you and upstream both have new commits; nothing was merged |
| `rewritten` | upstream rewrote its history (force-push) and your branch has local work; it was left as it was, and the old history is saved |
| `detached` | detached HEAD; no branch was moved |
| `no_upstream` | the current branch does not track a remote branch |
| `upstream_gone` | the branch it tracks no longer exists upstream |
| `empty` | the current branch has no commits yet |
| `blocked` | the update would overwrite local files |
| `busy` | a merge or rebase is in progress, or git is already running there |
| `local_only` | no remote configured; nothing to sync |
| `worktree` | a linked worktree, synced through its main repo |
| `failed` | an error; the summary gives a plain-language reason and a hint |

## How we test it

Data safety is tested against real git on Linux, macOS and Windows on every change: the
stash flow, an exact rollback on a clash, recovery from a run killed halfway, force-pushed
and deleted upstream branches, and mirrors. An end-to-end smoke test also runs every
release binary, installed with the install scripts, against a small archive and checks
that local work is still there afterwards.
