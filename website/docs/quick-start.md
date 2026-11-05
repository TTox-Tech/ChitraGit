# Quick start

## 1. Point ChitraGit at your archive

An archive is any folder that holds your clones, at any depth. Open a terminal in it:

```sh
cd ~/my-archive
chitragit
```

The first time, the **setup wizard** asks for the archive folder, scans it ("Found 3,412
repos across 287 owners"), lets you pick a theme and animation level, and offers a daily
or weekly scheduled sync. Every step can be skipped. Prefer the command line?

```sh
chitragit init        # set up this folder as an archive
chitragit doctor      # check git, LFS, database, config, terminal
```

## 2. Sync

```sh
chitragit sync        # find every repo, then update them all in parallel
chitragit status      # health of every repo from the last sync
```

Each repo gets one line in the summary. A repo that was not moved ahead always says why,
for example `you have local commits that are not pushed yet`. The full list is in
[How sync keeps your work safe](guide/safety.md#every-status-explained).

The first ++ctrl+c++ lets repos that already started finish safely; a second one exits
at once. An interrupted sync can continue with `chitragit sync --resume`.

## 3. Useful next steps

```sh
chitragit sync --mode dry-run      # ask the remotes what changed; download nothing
chitragit sync --mode fetch        # download only, never move a branch
chitragit sync --only 'torvalds/*' # just some repos
chitragit ls --status diverged     # repos that need a decision
chitragit activity --since 7d      # what changed this week
chitragit health                   # problems, disk usage and recent runs
chitragit schedule install --daily 02:00
chitragit auth login github        # private repos, stars, smart skip
chitragit follow github:torvalds   # clone every repo of a user or org
```

Every command takes `--json` (`"schema": "chitragit/v1"`) for scripts. See the
[command reference](reference/commands.md).
