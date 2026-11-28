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

## Rate limits

ChitraGit stays inside the limits a forge sets, without you tuning anything:

- **Fewer API calls.** API answers are kept in ChitraGit's cache folder, under `http/`
  (`~/.cache/chitragit` on Linux, `%LocalAppData%\chitragit` on Windows,
  `CHITRAGIT_CACHE_DIR` to move it), readable only by you. The next request for the same
  list or repo asks whether it changed, and an unchanged answer costs no download.
  GitHub does not count that answer against your rate limit. Deleting the folder is always
  safe.
- **Slowing down by itself.** When a host rate limits a fetch, the number of its repos
  synced at once halves, no new repo of that host starts for a few seconds, and the fetch
  is retried with backoff. Each round of repos that finish cleanly raises the number again,
  up to `sync.per_host_limit` (or `sync.workers`). Other hosts carry on at full speed.

## Repo details offline

The answers ChitraGit already gets from a forge also describe each repo: its description,
topics, stars, main language, licence, homepage and whether it is a fork. They are kept in
the archive database, so `chitragit show` prints them offline and `ls` can filter on them:

```sh
chitragit show torvalds/linux         # description, ★ stars · language · licence, topics
chitragit ls --language go            # repos whose main language is Go
chitragit ls --topic cli --language rust
```

Getting them costs no extra API call. A sync with a GitHub login reads them from the same
query smart skip makes, and `follow` reads them from the repo list on GitHub, GitLab and
Gitea. GitLab's list has no licence, language or homepage, so those stay empty there. They
are refreshed every time one of those answers comes back. A repo with a plain git remote, or
one synced without a login, has none, and `ls --language` and `--topic` never match it. A
rescued repo keeps the last details it had, since upstream can't be asked any more.

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
