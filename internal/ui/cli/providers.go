package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/auth"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/provider"
)

// tokenStore returns the token store in the user config dir.
func tokenStore() (*auth.Store, error) {
	dir, err := platform.ConfigDir()
	if err != nil {
		return nil, err
	}
	return auth.New(dir), nil
}

// registry builds provider API clients that use the saved tokens.
func registry(ctx context.Context, cfg *config.Config) *provider.Registry {
	reg := &provider.Registry{Hosts: cfg.Providers.Hosts}
	tokens, err := tokenStore()
	if err != nil {
		return reg
	}
	reg.Token = func(host string) string { return tokens.Lookup(ctx, reg.KindOf(host), host).Value }
	return reg
}

// hostFor turns "github", "codeberg" or a host name into a host.
func hostFor(name string) string {
	switch strings.ToLower(name) {
	case "github":
		return "github.com"
	case "gitlab":
		return "gitlab.com"
	case "codeberg", "forgejo":
		return "codeberg.org"
	case "gitea":
		return "gitea.com"
	}
	return strings.ToLower(name)
}

func (a *app) authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in to GitHub, GitLab or Gitea for private repos, stars and higher API limits",
		Long: "Tokens are kept in your OS keychain (or, where there is none, in a file only you\n" +
			"can read), never in the archive, config or logs. Git itself keeps using your own\n" +
			"credentials and SSH keys; ChitraGit never puts a token into a remote URL.\n\n" +
			"Tokens are also read from GH_TOKEN / GITHUB_TOKEN (github.com), GITLAB_TOKEN\n" +
			"(gitlab.com), CODEBERG_TOKEN, GITEA_TOKEN (gitea.com), CHITRAGIT_TOKEN_<HOST>\n" +
			"(e.g. CHITRAGIT_TOKEN_GIT_EXAMPLE_COM) and `gh auth token`.",
	}
	var withToken bool
	login := &cobra.Command{
		Use:   "login <github|gitlab|codeberg|gitea|host>",
		Short: "Log in and save a token",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			host := hostFor(args[0])
			reg := registry(ctx, cfg)
			kind := reg.KindOf(host)
			if kind == provider.Generic {
				return &core.Error{Category: core.CatConfig, Message: host + " is not a known GitHub, GitLab or Gitea server",
					Hint: "add it first: chitragit config set --user providers.hosts " + host + "=gitea"}
			}
			token, err := a.obtainToken(ctx, cfg, kind, host, withToken)
			if err != nil {
				return err
			}
			check := &provider.Registry{Hosts: cfg.Providers.Hosts, Token: func(string) string { return token }}
			user, err := check.For(host).User(ctx)
			if err != nil {
				return &core.Error{Category: core.CatAuth, Message: "the token did not work: " + plainMessage(err)}
			}
			store, err := tokenStore()
			if err != nil {
				return err
			}
			where, err := store.Save(host, token)
			if err != nil {
				return &core.Error{Category: core.CatPermission, Message: "cannot save the token", Err: err}
			}
			if where == auth.FromFile {
				a.warn(i18n.T("auth.file_fallback", "file", store.File()))
			}
			if a.jsonOut {
				return a.printJSON(map[string]string{"host": host, "user": user, "stored_in": where})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("auth.logged_in", "host", host, "user", user, "where", where))
			return nil
		},
	}
	login.Flags().BoolVar(&withToken, "with-token", false, "read a personal access token from standard input")

	logout := &cobra.Command{
		Use:   "logout <github|gitlab|codeberg|gitea|host>",
		Short: "Forget the saved token",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := hostFor(args[0])
			store, err := tokenStore()
			if err != nil {
				return err
			}
			if err := store.Delete(host); err != nil {
				return &core.Error{Category: core.CatPermission, Message: "cannot remove the token", Err: err}
			}
			cfg, _, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			left := store.Lookup(cmd.Context(), registry(cmd.Context(), cfg).KindOf(host), host)
			if a.jsonOut {
				return a.printJSON(map[string]any{"host": host, "still_from": left.Source + left.Var})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("auth.logged_out", "host", host))
			if left.Value != "" {
				src := left.Source
				if left.Var != "" {
					src = "$" + left.Var
				}
				a.warn(i18n.T("auth.still_token", "source", src))
			}
			return nil
		},
	}

	status := &cobra.Command{
		Use:   "status [github|gitlab|codeberg|gitea|host]",
		Short: "Show which servers you are logged in to",
		Args:  argsN(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			store, err := tokenStore()
			if err != nil {
				return err
			}
			reg := registry(ctx, cfg)
			hosts := []string{"github.com", "gitlab.com", "codeberg.org", "gitea.com"}
			for h := range cfg.Providers.Hosts {
				hosts = append(hosts, strings.ToLower(h))
			}
			if len(args) == 1 {
				hosts = []string{hostFor(args[0])}
			}
			type row struct {
				Host   string `json:"host"`
				Source string `json:"source,omitempty"`
				User   string `json:"user,omitempty"`
				Error  string `json:"error,omitempty"`
			}
			var rows []row
			for _, h := range hosts {
				tok := store.Lookup(ctx, reg.KindOf(h), h)
				r := row{Host: h, Source: tok.Source}
				if tok.Var != "" {
					r.Source = "$" + tok.Var
				}
				if tok.Value != "" {
					if c := reg.For(h); c != nil {
						cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
						if r.User, err = c.User(cctx); err != nil {
							r.Error = plainMessage(err)
						}
						cancel()
					}
				}
				rows = append(rows, r)
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "hosts": rows})
			}
			for _, r := range rows {
				name := a.styles.Label.Render(fmt.Sprintf("%-16s", r.Host))
				switch {
				case r.Source == "":
					fmt.Fprintln(a.out, name+a.styles.Muted.Render(i18n.T("auth.not_logged_in")))
				case r.Error != "":
					fmt.Fprintln(a.out, name+a.styles.Fail.Render(r.Error)+a.styles.Muted.Render("  ("+r.Source+")"))
				default:
					fmt.Fprintln(a.out, name+a.styles.OK.Render(r.User)+a.styles.Muted.Render("  ("+r.Source+")"))
				}
			}
			return nil
		},
	}
	cmd.AddCommand(login, logout, status)
	return cmd
}

// obtainToken gets a token by browser login (GitHub, when this build has
// an OAuth app), from stdin, or by asking for it without echo.
func (a *app) obtainToken(ctx context.Context, cfg *config.Config, kind, host string, fromStdin bool) (string, error) {
	clientID := cfg.Providers.GitHubClientID
	if clientID == "" {
		clientID = auth.GitHubClientID
	}
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if !fromStdin && kind == provider.GitHub && clientID != "" {
		d := &auth.Device{ClientID: clientID, Base: "https://" + host, Scopes: []string{"repo", "read:org"}}
		return d.Login(ctx, func(c auth.DeviceCode) {
			fmt.Fprintln(a.errOut, i18n.T("auth.device_code", "code", a.styles.Accent.Render(c.UserCode), "url", c.VerificationURI))
		})
	}
	if !fromStdin && !stdinTTY {
		return "", &usageError{fmt.Errorf("no terminal to ask for a token; pipe one in with --with-token")}
	}
	if !fromStdin {
		fmt.Fprintln(a.errOut, i18n.T("auth.token_help", "url", tokenPage(kind, host)))
		fmt.Fprint(a.errOut, i18n.T("auth.token_prompt"))
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(a.errOut)
		if err != nil {
			return "", err
		}
		return validToken(string(b))
	}
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 64<<10)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return validToken(line)
}

func validToken(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return "", &usageError{fmt.Errorf("that does not look like a token")}
	}
	return s, nil
}

func tokenPage(kind, host string) string {
	switch kind {
	case provider.GitHub:
		return "https://" + host + "/settings/tokens"
	case provider.GitLab:
		return "https://" + host + "/-/user_settings/personal_access_tokens"
	}
	return "https://" + host + "/user/settings/applications"
}

func plainMessage(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

// cloneOptions builds clone settings from the config.
func (s *session) cloneOptions() engine.CloneOptions {
	return engine.CloneOptions{Root: s.arc.Root, TmpDir: filepath.Join(s.arc.Meta(), "tmp"), Layout: s.cfg.Clone.Layout,
		Mirror: s.cfg.Clone.Type == "mirror", Timeout: time.Duration(s.cfg.Sync.Timeout)}
}

func (a *app) followCmd() *cobra.Command {
	var f engine.Filters
	var noArchived, noClone bool
	cmd := &cobra.Command{
		Use:   "follow <source>",
		Short: "Clone every repo of a user, org or group, now and on every sync",
		Long: "Sources:\n" +
			"  github:torvalds          all repos of a user or org\n" +
			"  github:@starred          your starred repos (needs `auth login`)\n" +
			"  gitlab:gnome/            a GitLab group, with its subgroups\n" +
			"  codeberg:forgejo         Codeberg (Gitea/Forgejo)\n" +
			"  gitea@git.example.com:team   a self-hosted server\n\n" +
			"Forks are skipped unless --forks. Repos removed from a source are never deleted\n" +
			"from your archive: they go through the rescue flow instead.",
		Args: argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := provider.ParseSource(args[0])
			if err != nil {
				return &usageError{err}
			}
			if f.Name != "" {
				if _, err := filepath.Match(f.Name, ""); err != nil {
					return &usageError{fmt.Errorf("invalid --name pattern %q", f.Name)}
				}
			}
			if noArchived {
				no := false
				f.Archived = &no
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit follow", true)
			if err != nil {
				return err
			}
			defer s.close()
			added, err := s.st.AddSource(ctx, src.String(), f.JSON())
			if err != nil {
				return err
			}
			if !a.jsonOut {
				key := "follow.added"
				if !added {
					key = "follow.updated"
				}
				fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T(key, "source", src.String()))
			}
			if noClone {
				return nil
			}
			return a.runFollow(ctx, s)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.Forks, "forks", false, "also clone forks")
	fl.BoolVar(&noArchived, "no-archived", false, "skip repos archived upstream")
	fl.Int64Var(&f.MaxSizeMB, "max-size", 0, "skip repos larger than this many MB (0 = no limit)")
	fl.StringVar(&f.Name, "name", "", "only repos whose name matches this glob")
	fl.StringVar(&f.Language, "language", "", "only repos in this language")
	fl.BoolVar(&noClone, "no-clone", false, "only save the source; clone on the next sync")
	return cmd
}

// runFollow checks every source, clones new repos and registers them.
func (a *app) runFollow(ctx context.Context, s *session) error {
	rep, err := engine.Follow(ctx, s.cloneOptions(), registry(ctx, s.cfg), s.git, s.st)
	if rep == nil {
		return err
	}
	if rep.Cloned > 0 {
		if _, serr := engine.Scan(ctx, s.arc.Root, s.scanOptions(), s.git, s.st); serr != nil && err == nil {
			err = serr
		}
	}
	if a.jsonOut {
		if jerr := a.printJSON(struct {
			Schema string `json:"schema"`
			*engine.FollowReport
		}{jsonSchema, rep}); jerr != nil {
			return jerr
		}
	} else {
		a.printFollow(rep)
	}
	if err != nil {
		return err
	}
	for _, src := range rep.Sources {
		if src.Problem != "" || len(src.Failed) > 0 {
			return &silentError{code: core.ExitPartial}
		}
	}
	return nil
}

func (a *app) printFollow(rep *engine.FollowReport) {
	for _, w := range rep.Warnings {
		a.warn(w)
	}
	for _, src := range rep.Sources {
		name := a.styles.Label.Render(src.Spec)
		if src.Problem != "" {
			fmt.Fprintln(a.out, a.styles.Fail.Render("✗ ")+name+"  "+a.styles.Text.Render(src.Problem))
			continue
		}
		for _, p := range src.Cloned {
			fmt.Fprintln(a.out, a.styles.OK.Render("  + ")+a.styles.Text.Render(p))
		}
		for _, f := range src.Failed {
			fmt.Fprintln(a.out, a.styles.Fail.Render("  ✗ ")+a.styles.Text.Render(f))
		}
		fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+name+"  "+
			a.styles.Muted.Render(i18n.T("follow.summary", "listed", src.Listed, "cloned", len(src.Cloned))))
	}
}

func (a *app) unfollowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unfollow <source>",
		Short: "Stop following a source (repos already cloned stay)",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]
			if src, err := provider.ParseSource(spec); err == nil {
				spec = src.String()
			}
			s, err := a.open(cmd.Context(), "chitragit unfollow", true)
			if err != nil {
				return err
			}
			defer s.close()
			ok, err := s.st.RemoveSource(cmd.Context(), spec)
			if err != nil {
				return err
			}
			if !ok {
				return &core.Error{Category: core.CatConfig, Message: "not following " + spec, Hint: "see `chitragit sources`"}
			}
			if a.jsonOut {
				return a.printJSON(map[string]string{"unfollowed": spec})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("follow.removed", "source", spec))
			return nil
		},
	}
}

func (a *app) sourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sources",
		Short: "List followed sources",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open(cmd.Context(), "", false)
			if err != nil {
				return err
			}
			defer s.close()
			list, err := s.st.Sources(cmd.Context())
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "sources": list})
			}
			if len(list) == 0 {
				fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("follow.none")))
			}
			for _, src := range list {
				line := a.styles.Label.Render(src.Spec)
				if src.Filters != "" {
					line += "  " + a.styles.Muted.Render(src.Filters)
				}
				if src.LastError != "" {
					line += "  " + a.styles.Fail.Render(src.LastError)
				} else if src.LastCheckedAt != "" {
					line += a.styles.Muted.Render(strings.Replace(since(src.LastCheckedAt), "synced", "checked", 1))
				}
				fmt.Fprintln(a.out, line)
			}
			return nil
		},
	}
}

func (a *app) cloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <url | provider:owner/repo>",
		Short: "Clone one repo into the archive, using the clone layout",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit clone", true)
			if err != nil {
				return err
			}
			defer s.close()
			rel, err := engine.Clone(ctx, args[0], s.cloneOptions(), registry(ctx, s.cfg), s.git, s.st)
			if err != nil {
				return err
			}
			if rel == "" {
				return &core.Error{Category: core.CatConfig, Message: "the target folder already exists", Hint: "run `chitragit scan` to register it"}
			}
			if _, err := engine.Scan(ctx, s.arc.Root, s.scanOptions(), s.git, s.st); err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]string{"path": rel})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("clone.done", "path", rel))
			return nil
		},
	}
}

// findRepo resolves a repo by its archive path.
func (s *session) findRepo(ctx context.Context, p string) (core.Repo, error) {
	p = strings.Trim(filepath.ToSlash(p), "/")
	repos, err := s.st.Repos(ctx)
	if err != nil {
		return core.Repo{}, err
	}
	for _, r := range repos {
		if r.Path == p {
			return r, nil
		}
	}
	return core.Repo{}, &core.Error{Category: core.CatConfig, Message: "no repo at " + p + " in this archive",
		Hint: "paths are relative to the archive root; see `chitragit ls`"}
}

func (a *app) lockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lock <repo>",
		Short: "Protect a repo: ChitraGit will refuse to delete or move it",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.setLock(cmd.Context(), args[0], true, "")
		},
	}
}

func (a *app) unlockCmd() *cobra.Command {
	var confirm string
	cmd := &cobra.Command{
		Use:   "unlock <repo>",
		Short: "Remove a repo's protection (asks you to type its path)",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.setLock(cmd.Context(), args[0], false, confirm)
		},
	}
	cmd.Flags().StringVar(&confirm, "confirm", "", "the repo path, typed again, to unlock without a prompt")
	return cmd
}

func (a *app) setLock(ctx context.Context, path string, locked bool, confirm string) error {
	s, err := a.open(ctx, "chitragit lock", true)
	if err != nil {
		return err
	}
	defer s.close()
	r, err := s.findRepo(ctx, path)
	if err != nil {
		return err
	}
	if !locked {
		if r.Rescued {
			a.warn(i18n.T("lock.rescued_warning"))
		}
		if confirm == "" && term.IsTerminal(int(os.Stdin.Fd())) && !a.jsonOut {
			fmt.Fprint(a.errOut, i18n.T("lock.type_path", "path", r.Path))
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			confirm = strings.TrimSpace(line)
		}
		if confirm != r.Path {
			return &usageError{fmt.Errorf("not unlocked: type the repo path exactly (%s), or pass --confirm %s", r.Path, r.Path)}
		}
	}
	if err := s.st.SetLocked(ctx, r.ID, locked); err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"path": r.Path, "locked": locked})
	}
	key := "lock.locked"
	if !locked {
		key = "lock.unlocked"
	}
	fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T(key, "path", r.Path))
	return nil
}

func (a *app) bundleCmd() *cobra.Command {
	var rescued bool
	cmd := &cobra.Command{
		Use:   "bundle <repo>... | --rescued",
		Short: "Write a single-file backup (git bundle) of repos' full history",
		Long: "Bundles go to .chitragit/bundles/<repo path>.bundle. A bundle holds every branch,\n" +
			"tag and ChitraGit safety ref; restore one with `git clone <file>.bundle`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if rescued == (len(args) > 0) {
				return &usageError{fmt.Errorf("name repos or pass --rescued, not both")}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit bundle", true)
			if err != nil {
				return err
			}
			defer s.close()
			var repos []core.Repo
			if rescued {
				all, err := s.st.Repos(ctx)
				if err != nil {
					return err
				}
				for _, r := range all {
					if r.Rescued && !r.Missing {
						repos = append(repos, r)
					}
				}
			}
			for _, p := range args {
				r, err := s.findRepo(ctx, p)
				if err != nil {
					return err
				}
				repos = append(repos, r)
			}
			results := engine.Bundle(ctx, engine.BundleRequest{Root: s.arc.Root, BundlesDir: s.arc.BundlesDir(), Repos: repos}, s.git, s.st)
			if a.jsonOut {
				if err := a.printJSON(map[string]any{"schema": jsonSchema, "bundles": results}); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					if r.Error != "" {
						fmt.Fprintln(a.out, a.styles.Fail.Render("✗ ")+a.styles.Label.Render(r.Path)+"  "+a.styles.Text.Render(r.Error))
					} else {
						fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Label.Render(r.Path)+"  "+a.styles.Muted.Render(r.Bundle))
					}
				}
				if len(results) == 0 {
					fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("bundle.none")))
				}
			}
			if slices.ContainsFunc(results, func(r engine.BundleResult) bool { return r.Error != "" }) {
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&rescued, "rescued", false, "bundle every rescued repo")
	return cmd
}
