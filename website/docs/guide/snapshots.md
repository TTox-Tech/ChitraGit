# Snapshots

A snapshot is the full ref state of one repo at one moment: every branch, remote-tracking
branch and tag, with the commit it pointed at. ChitraGit takes them for you, so you can
always see what a repo looked like before and after any sync.

## When snapshots are taken

- **Every sync** takes one snapshot before it touches a repo (`pre`) and one after
  (`post`), stored in the same database transaction as the sync's result.
- **`chitragit snapshot take`** records one now (`manual`), for the repos you name or
  every repo matching `--only`.

A snapshot identical to the repo's last one is not stored again, so the thousands of repos
that did not change in a sync cost nothing. Dry runs take no snapshot.

```sh
chitragit snapshot ls                    # newest first
chitragit snapshot ls --repo alice/notes # one repo
chitragit snapshot show 42               # every ref in snapshot 42
chitragit snapshot take alice/notes      # record one now
chitragit snapshot take --only 'work/*'
```

Every command takes `--json`.

## A repo's history

`chitragit history <repo>` puts a repo's snapshots next to everything else that happened to
it, newest first:

```text
$ chitragit history alice/notes
alice/notes
  2026-11-07 09:14:02  snapshot  snapshot 41 (post)
  2026-11-07 09:14:02  rewrite   refs/heads/main  3f9c2a1b7d10 → a81e04c2f6b3  old commits kept
  2026-11-07 09:14:00  snapshot  snapshot 40 (pre)
  2026-11-06 21:30:11  delete    refs/heads/spike  c02d9e81aa47  deleted upstream, kept here
  2026-11-05 08:02:45  update    refs/heads/main  91be7710c3d2 → 3f9c2a1b7d10  2 new commit(s)
  2026-11-01 12:00:00  rescued   upstream says this repo no longer exists …
```

It lists every ref a sync created (`create`), moved forward (`update`), rewrote after an
upstream force-push (`rewrite`) or saw deleted upstream (`delete`), every snapshot with
its ID for `chitragit snapshot show`, and events such as `rescued`, `renamed`, `locked`
and `unlocked`. Within one sync the `pre` snapshot comes before the ref changes and the
`post` snapshot after them. `--limit` caps the number of entries (default 200) and
`--json` prints them for scripts. It reads only the archive database, so it works offline.

To put a repo back as a snapshot recorded it, see [Restore](restore.md).

## Safe from git gc

A snapshot is only useful if the commits it names still exist. Before a snapshot is stored,
every object it points at gets a ref in the repo itself:

```
refs/chitragit/snap/<sha>
```

`git gc` never removes an object a ref points at, so a snapshot's commits survive an upstream
force-push, a deleted branch or tag, plain pruning and an aggressive `git gc --prune=now`.
Refs are named by object, so an object that appears in many snapshots is pinned once: the
number of refs grows with new history, not with the number of syncs. Git stores them in
packed-refs, so they cost a few bytes each. Only snapshot retention (coming later) will
remove them.

To look at a pinned commit with plain git:

```sh
git log refs/chitragit/snap/<sha>
```

## Cost

Recording an unchanged repo's result takes about 0.17 ms more with snapshots, under one
second of database time for 5,000 repos. Listing the pins rides on a git call the sync
already makes, so an unchanged repo adds no git call.
