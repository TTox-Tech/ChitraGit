# Coming from git

`chit` is the short name for `chitragit`: the same program, so `chit sync` and
`chitragit sync` do the same thing. The install scripts and Scoop add it; on winget or
from a zip, run `chitragit alias add chit`. `cg` and `git-chit` (so you can type
`git chit sync`) are there too, with `alias add`, when no other program already uses
the name.

## One idea

**The archive is the repository, and each repo is a file in it.** So the git words you
know mean the same thing, one level up: `chit status` is the status of the whole archive,
`chit log` is its history, and `chit log torvalds/linux` is the history of one repo,
like `git log <path>`.

## The words

| git | chit | What it does |
|---|---|---|
| `git init` | `chit init` | set up a folder as an archive |
| `git clone <url>` | `chit clone <url>` | clone one repo into the archive layout |
| `git pull` | `chit pull` | sync: download and fast-forward every repo when it is safe |
| `git fetch` | `chit fetch` | download only, never move a branch |
| `git fetch --dry-run` | `chit fetch --dry-run` | ask the remotes what changed, download nothing |
| `git status` / `-s` | `chit status` / `-s` | health of every repo from the last sync |
| `git log` | `chit log` | the operation log: everything ChitraGit changed |
| `git log <path>` | `chit log <repo>` | everything that happened to one repo |
| `git log -n 5` | `chit log -n 5` | only the newest entries |
| `git show <commit>` | `chit show <repo>` or `chit show <snapshot>` | one repo in detail, or one snapshot |
| `git show <commit>` | `chit show <repo>@<commit>` | a commit: header, files changed and the coloured diff; `--stat` for the files only |
| `git show <rev>:<path>` | `chit show <repo>[@<rev>]:<path>` | a file as committed, with syntax colours; `--raw` for the bytes |
| `git ls-tree <rev> [path]` | `chit ls-tree <repo>[@<rev>] [path]` | the files of a repo at a branch, tag or commit; `-r` for all |
| `git blame <file>` | `chit blame <repo>[@<rev>] <file>` | who last changed each line, following renames |
| `git restore --source` | `chit restore <repo> --source <snapshot>` | put a repo's refs back as a snapshot recorded them |
| `git remote -v` | `chit remote -v` | the users, orgs and groups you follow |
| `git remote add` | `chit remote add github:torvalds` | follow a source: clone its repos now and on every sync |
| `git remote remove` | `chit remote rm github:torvalds` | stop following (repos already cloned stay) |
| `git config <key> [value]` | `chit config <key> [value]` | read or write a setting; `--list`, `--edit` |
| `git bundle` | `chit bundle <repo>` | a single-file backup of a repo's full history |
| `git -C <dir>` | `chit -C <archive>` | run in another archive |
| `git ls-files` | `chit ls` | list the repos in the archive |
| `git fsck` | `chit fsck` (also `chit verify`) | check that the archive is whole, changing nothing |
| `git gc` | `chit gc` (also `chit retention`) | remove old snapshots; `--apply` to do it |
| `git diff a b` | `chit diff [a] [b] [repo]`, `--stat` | what the last sync changed, or what changed between two snapshots |
| `git revert`, roughly | `chit undo` | put back every ref the last sync moved |

Coming during the release candidates: `chit tag <name>`
for named snapshots, more `chit fsck` checks and `chit grep`.

Your own repo labels (work, to-read, …) are `chit label`, because `tag` will mean what it
means in git: a named point in history.

## Words that write history

ChitraGit keeps your archive; it never writes commits. `chit commit`, `push`, `merge`,
`rebase`, `checkout`, `switch`, `reset`, `cherry-pick`, `stash` and `add` say so and print
the git command to run in that repo:

```text
$ chit commit -am "fix" torvalds/linux
✗ ChitraGit keeps your archive and does not write commits, so `commit` is git's job
  Try: git -C "/home/me/archive/torvalds/linux" commit
```

Nothing is passed through to git, so no hooks or filters run inside archived repos.
