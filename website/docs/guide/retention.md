# Retention

Every sync stores a snapshot of each repo it changed, and pins the commits that snapshot
names. Over months that is a lot of snapshots. `chitragit retention` (or `chit gc`) removes
the ones you no longer need, under a policy you set, and never removes the last copy of
anything.

## See the plan first

```sh
chitragit retention          # list what would be removed; changes nothing
chitragit retention --all    # also list what is kept, and why
chitragit retention --apply  # remove it
```

Without `--apply` nothing changes, so you can run it as often as you like. `--json` prints
the same plan or result for scripts.

## The policy

Set it in the `[retention]` section of your config (`chitragit config --edit`). A snapshot
is kept when any rule wants it:

| Setting | Default | Keeps |
|---|---|---|
| `keep_last` | `10` | the newest N snapshots of each repo (at least 1) |
| `keep_daily` | `14` | the newest snapshot of each of the last N days that have one |
| `keep_weekly` | `8` | the newest snapshot of each of the last N weeks that have one |

Days and weeks are counted in UTC, and only days or weeks that have a snapshot count. A repo
you have not synced for a month still keeps its last 14 days of history, not nothing.

## What is always kept

- **The newest snapshot of every repo**, whatever the policy says.
- **Named snapshots** are never removed.
- **The only copy.** If a snapshot holds a commit or tag that no branch, tag or other kept
  snapshot reaches, it is kept. That covers history an upstream force-push removed, a
  branch deleted upstream, or a tag that was moved. `--all` shows these as `only copy`.

So retention never makes a commit unreachable. It removes a snapshot's pins under
`refs/chitragit/snap/` only when something else still reaches those commits, and git gc
can only drop objects that nothing points at.

## Half-written bundles

If a `chitragit bundle` run is killed, it can leave a `<name>.bundle.tmp-<pid>` file next to
the finished bundle. Retention lists these and removes them with `--apply`. The finished
bundle, which is the last good backup, is never touched.

## Safe to interrupt

`--apply` takes the archive lock, so no sync runs at the same time. It removes the snapshot
records from the database first, in one transaction, and logs one entry in the
[operation log](operation-log.md). Only then does it remove the pins. If it stops in between,
the extra pins stay, and the next run removes them. A pin is only removed when its commit is
still reachable, so a pin an interrupted sync left behind is never dropped.

Run [`chitragit verify`](verify.md) afterwards if you want to see that every kept snapshot
still has its objects.
