# Forges, logins and Rescued repos

ChitraGit works with any Git remote. For GitHub, GitLab and Gitea / Forgejo / Codeberg,
including self-hosted servers, it also talks to the forge's API.

## Log in

```sh
chitragit auth login github       # browser login, or paste a token
chitragit auth login gitlab
chitragit auth status
```

Tokens are kept in your OS keychain (or, where there is none, in a file only you can
read), never in the archive, config or logs, and never in a remote URL. Git itself keeps
using your own credentials and SSH keys. Tokens are also read from `GH_TOKEN` /
`GITHUB_TOKEN`, `GITLAB_TOKEN`, `CODEBERG_TOKEN`, `GITEA_TOKEN`,
`CHITRAGIT_TOKEN_<HOST>` and `gh auth token`.

A self-hosted server is added with `[providers] hosts`; see [Settings](../reference/settings.md).

## Smart skip

With a GitHub login, one API call checks 100 repos and unchanged repos are not fetched at
all. A no-change sync of 5,000 GitHub repos takes under two minutes.

## Follow a user, org or group

```sh
chitragit follow github:torvalds        # every repo of a user or org
chitragit follow github:@starred        # your stars
chitragit follow gitlab:gnome/          # a GitLab group, with its subgroups
chitragit sources                       # what you follow
```

New repos are cloned on every sync. Forks are skipped unless `--forks`. Repos removed
from a source are **never deleted** from your archive.

## Rescued repos

When a repo's upstream is deleted, made private or taken down, ChitraGit marks it
**RESCUED** after two failed runs (`guardian.rescue_confirmations`), protects it from
deletion and writes a verified git bundle of its full history to
`.chitragit/bundles/` (`guardian.auto_bundle_rescued`). Renamed upstreams are followed
automatically instead.

```sh
chitragit activity --type rescued  # what was rescued, and when
chitragit bundle --rescued          # back up every rescued repo now
chitragit lock owner/repo           # protect any repo by hand
git clone .chitragit/bundles/owner/repo.bundle restored-repo
```
