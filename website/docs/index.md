# ChitraGit

<p align="center"><img src="assets/banner-dark.svg#only-dark" alt="ChitraGit"><img src="assets/banner-light.svg#only-light" alt="ChitraGit"></p>

**The keeper of your Git archive.** *Chitragupta* is the divine record-keeper who never
forgets a single entry. ChitraGit does the same for a folder of hundreds or thousands of
cloned repositories: it keeps every one up to date, never loses your local work, and
notices when an upstream repo disappears, which is exactly when your copy becomes precious.

<video controls muted loop playsinline preload="metadata" poster="assets/chitragit-ad-poster.png" width="100%">
  <source src="assets/chitragit-ad.mp4" type="video/mp4">
  <img src="assets/chitragit-ad.gif" alt="ChitraGit in two minutes">
</video>
<small>A two-minute preview of ChitraGit v1.0. Some screens show features still being built.</small>

![chitragit sync updating an archive](assets/demo-sync.svg)

## What it does

- **Parallel sync for 5,000+ repos**, with per-host limits, retries and timeouts.
- **Never loses your work.** Local changes are stashed and put back. If they clash with
  the update, the repo is restored exactly as it was. Diverged repos get the new data but
  are never merged. See [How sync keeps your work safe](guide/safety.md).
- **Never loses history.** Branches deleted upstream and history replaced by a force-push
  are kept under `refs/chitragit/`. A sync killed halfway is repaired on the next run.
- **An interactive ledger**: a clickable, animated terminal app with a repo browser,
  README and commit views, an activity feed and a health dashboard.
- **Rescued repos**: when a repo vanishes upstream it gets a badge, delete-protection and
  a single-file backup.
- **Any forge**: GitHub, GitLab, Gitea / Forgejo / Codeberg or any plain Git URL.
- **Scheduled sync** with cron, launchd or Task Scheduler. No telemetry, ever.
- **A full headless CLI**: every command has `--json` output and stable exit codes.

## Three commands

```sh
cd ~/my-archive
chitragit init     # set up this folder as an archive
chitragit sync     # find every repo, then update them all in parallel
chitragit          # open the interactive ledger
```

[Install ChitraGit](install.md){ .md-button .md-button--primary } [Quick start](quick-start.md){ .md-button }
