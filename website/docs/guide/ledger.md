# The interactive ledger

Run `chitragit` with no command in a terminal to open the ledger: a full-screen app styled
after the *bahi-khata*, the red-bound Indian account book. Everything works with the
keyboard and the mouse (click, double-click, shift-click and the wheel).

![The interactive ledger](../assets/demo-tui.gif)

## Screens

| Screen | |
|---|---|
| **Repos** | every repo, with fuzzy search, filters, sorting and multi-select. ++enter++ opens a repo's details: README, commits, branches, remotes and its sync history. |
| **Sync** | live progress with an ETA, one lane per repo in flight, a streaming ledger of results, and pause, resume and stop. |
| **Conflicts** | settle clashes between your changes and an update, file by file or hunk by hunk. |
| **Activity** | what changed across the archive, newest first. |
| **Health** | problems, disk usage and recent runs at a glance. |
| **Settings** | every setting, with its value and what it does. |

## Keys

| Key | |
|---|---|
| ++tab++ / ++shift+tab++, ++1++ … ++6++ | switch screens |
| ++up++ ++down++ | move |
| ++slash++ | search |
| ++f++ / ++o++ | filter / sort |
| ++space++ / ++a++ | mark a repo / mark all |
| ++enter++ | open |
| ++s++ / ++shift+s++ | sync the marked repos / sync everything |
| ++question++ | help with every key on this screen |
| ++q++ | quit |

## Themes, animation and accessibility

There are three themes: `ledger` (dark), `ledger-light` and `high-contrast`. Animation can
be `full`, `light`, `off` or `auto`; auto picks light over SSH and off for terminals
without colour.

```sh
chitragit config set --user ui.theme ledger-light
chitragit config set --user ui.animations off
```

`--accessible` (or `ui.accessible = true`) turns off colour and animation everywhere and
uses words instead of symbols, for screen readers.

Syncing from the ledger is as safe as from the CLI: stopping or quitting lets repos in
flight finish their step, and the program waits for the results to be recorded before
it exits.
