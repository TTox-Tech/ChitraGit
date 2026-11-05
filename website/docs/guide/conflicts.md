# Conflicts

When your uncommitted changes clash with an update, sync puts the repo back **exactly**
as it was, downloads the update without applying it, and lists the repo as a conflict.
Nothing happens to it until you decide.

## In the ledger

Open the **Conflicts** screen, pick a repo, and settle each clash: keep mine, take
theirs (upstream), or keep both, for a whole file or one hunk at a time.

## From the command line

```sh
chitragit conflicts                          # list repos with conflicts
chitragit conflicts start owner/repo         # apply the update, show the clashes
chitragit conflicts show owner/repo file.go  # one file's hunks
chitragit conflicts resolve owner/repo file.go mine|theirs|both [--hunk N]
chitragit conflicts edit owner/repo file.go  # fix it in your editor, then `conflicts done`
chitragit conflicts finish owner/repo        # keep the result as uncommitted changes
chitragit conflicts abort owner/repo         # put the repo back exactly as it was
```

Nothing is ever lost: your original changes are kept under `refs/chitragit/resolved/`,
and an aborted session's work under `refs/chitragit/aborted/`.
