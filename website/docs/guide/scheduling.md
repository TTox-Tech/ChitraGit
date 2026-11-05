# Scheduled sync

```sh
chitragit schedule install --daily 02:00              # or --weekly 03:30 --day sun
chitragit schedule status                             # next run, last result, log file
chitragit schedule remove
```

The job goes into your system's own scheduler and runs `chitragit sync --headless`:

| OS | Scheduler | Notes |
|---|---|---|
| Linux | cron | ChitraGit keeps its own marked block in your crontab, leaves every other line exactly as it was, and backs up the old crontab first. cron does not catch up runs missed while the machine was off. |
| macOS | launchd | a per-user agent that also runs a missed job after sleep. |
| Windows | Task Scheduler | a per-user task; paths with spaces are fine. |

A headless run never asks questions, writes a log to `.chitragit/logs/` (kept 30 days)
and shows a desktop notification at the end, for example "Synced 5,012 repos · 143
updated · 3 failed". Turn notifications off with:

```sh
chitragit config set notify.desktop false
```
