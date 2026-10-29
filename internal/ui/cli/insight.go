package cli

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// readGit returns a git runner for read-only commands. Reads never take
// the archive lock, so they work while a sync is running.
func readGit(ctx context.Context) (*gitx.Runner, error) {
	hooks, err := platform.EmptyHooksDir()
	if err != nil {
		return nil, err
	}
	g, err := gitx.New(gitx.Options{HooksDir: hooks, Timeout: time.Minute})
	if err != nil {
		return nil, err
	}
	_, err = g.CheckVersion(ctx)
	return g, err
}

// pickRepos resolves repo arguments and an optional --only glob to IDs.
func pickRepos(ctx context.Context, st *store.Store, args []string, only string) ([]core.Repo, error) {
	var out []core.Repo
	seen := map[string]bool{}
	add := func(r core.Repo) {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	for _, q := range args {
		r, err := st.FindRepo(ctx, q)
		if err != nil {
			return nil, err
		}
		add(r)
	}
	if only != "" {
		if _, err := path.Match(only, ""); err != nil {
			return nil, &usageError{fmt.Errorf("invalid --only pattern %q", only)}
		}
		all, err := st.Repos(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range all {
			if ok, _ := path.Match(only, r.Path); ok {
				add(r)
			}
		}
	}
	if len(out) == 0 {
		return nil, &usageError{fmt.Errorf("name at least one repo, or use --only <glob>")}
	}
	return out, nil
}

func ids(repos []core.Repo) []string {
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.ID
	}
	return out
}

// ---- show ----

type showJSON struct {
	Schema string `json:"schema"`
	*store.RepoInfo
	Readme   string          `json:"readme,omitempty"`
	Commits  []engine.Commit `json:"commits"`
	Refs     []engine.Ref    `json:"refs"`
	History  []store.RepoRun `json:"history"`
	GitError string          `json:"git_error,omitempty"`
}

func (a *app) showCmd() *cobra.Command {
	var commits int
	cmd := &cobra.Command{
		Use:   "show <repo>",
		Short: "Show everything about one repository",
		Long: "Shows a repo's remotes, status, tags, recent commits, branches and sync history.\n" +
			"<repo> can be its path in the archive, owner/name, its folder name if unique, or its ID.",
		Args: argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			r, err := s.st.FindRepo(ctx, args[0])
			if err != nil {
				return err
			}
			info, err := s.st.RepoInfo(ctx, r.ID)
			if err != nil {
				return err
			}
			out := showJSON{Schema: jsonSchema, RepoInfo: info, Commits: []engine.Commit{}, Refs: []engine.Ref{}}
			if out.History, err = s.st.RepoHistory(ctx, r.ID, 10); err != nil {
				return err
			}

			// Git details are best effort: a missing or broken repo still shows
			// what the database knows.
			var gitErr error
			if !info.Missing {
				var g *gitx.Runner
				if g, gitErr = readGit(ctx); gitErr == nil {
					dir := engine.RepoDir(s.arc.Root, r)
					var cs []engine.Commit
					if cs, gitErr = engine.Commits(ctx, g, dir, "", commits); gitErr == nil {
						out.Commits = append(out.Commits, cs...)
						var refs []engine.Ref
						if refs, gitErr = engine.Refs(ctx, g, dir); gitErr == nil {
							out.Refs = append(out.Refs, refs...)
						}
						if rd, err := engine.ReadReadme(ctx, g, dir); err == nil && rd != nil {
							out.Readme = rd.Name
						}
					}
				}
			}
			if gitErr != nil {
				out.GitError = gitErr.Error()
			}
			if a.jsonOut {
				return a.printJSON(out)
			}
			a.printShow(out, gitErr)
			return nil
		},
	}
	cmd.Flags().IntVar(&commits, "commits", 10, "how many recent commits to show")
	return cmd
}

func (a *app) printShow(o showJSON, gitErr error) {
	st, w := a.styles, a.out
	title := o.Path
	if o.Favourite {
		title = "★ " + title
	}
	fmt.Fprintln(w, st.Title.Render(title))
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(w, "  %s %s\n", st.Label.Render(fmt.Sprintf("%-15s", i18n.T(label))), st.Text.Render(value))
		}
	}
	status := statusOf(o.Repo)
	if o.StatusReason != "" {
		status += " · " + o.StatusReason
	}
	row("show.status", status)
	if o.Owner != "" {
		row("show.upstream", strings.TrimPrefix(o.Provider+" · ", " · ")+o.Owner+"/"+o.Name)
	}
	row("show.kind", o.Kind)
	names := make([]string, 0, len(o.Remotes))
	for n := range o.Remotes {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		row("show.remote", n+"  "+o.Remotes[n])
	}
	if len(o.Tags) > 0 {
		row("show.tags", strings.Join(o.Tags, ", "))
	}
	if o.Locked {
		row("show.protection", i18n.T("show.locked"))
	}
	row("show.last_synced", strings.TrimSpace(ago(o.LastSyncedAt)))
	row("show.last_activity", strings.TrimSpace(ago(o.LastUpstreamActivityAt)))
	if o.SizeBytes > 0 {
		row("show.size", humanBytes(o.SizeBytes))
	}
	row("show.readme", o.Readme)
	row("show.id", o.ID)
	if o.Rescue != nil {
		msg := o.Rescue.Reason
		if o.Rescue.RescuedAt == "" {
			msg += " · " + i18n.T("show.rescue_pending", "count", o.Rescue.Confirmations)
		}
		fmt.Fprintln(w, st.Accent.Render("  ▲ "+i18n.T("show.rescued")+": "+msg))
	}
	if o.Conflict != nil {
		fmt.Fprintln(w, st.Warn.Render("  ! "+i18n.T("show.conflict", "files", strings.Join(o.Conflict.Files, ", "))))
	}

	if gitErr != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Warn.Render("! "+i18n.T("show.git_error")))
		a.printDetail(gitErr)
		return
	}
	if len(o.Commits) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Title.Render(i18n.T("show.commits")))
		for _, c := range o.Commits {
			line := "  " + st.Accent.Render(c.Graph)
			if c.SHA != "" {
				line += " " + st.Muted.Render(c.SHA[:min(8, len(c.SHA))]) + " " + st.Text.Render(c.Subject)
				if len(c.Refs) > 0 {
					line += " " + st.Accent.Render("("+strings.Join(c.Refs, ", ")+")")
				}
				line += st.Muted.Render("  " + c.Author + ago(c.Date))
			}
			fmt.Fprintln(w, line)
		}
	}
	counts := map[string]int{}
	for _, r := range o.Refs {
		counts[r.Kind]++
	}
	if len(o.Refs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Title.Render(i18n.T("show.refs")))
		for _, r := range o.Refs {
			if r.Kind != engine.RefBranch {
				continue
			}
			mark := "  "
			if r.Current {
				mark = "* "
			}
			line := "  " + mark + st.Label.Render(r.Name)
			if r.Upstream != "" {
				line += st.Muted.Render(" → " + r.Upstream)
			}
			if r.Track != "" {
				line += " " + st.Warn.Render("["+r.Track+"]")
			}
			fmt.Fprintln(w, line)
		}
		var parts []string
		for _, k := range []string{engine.RefRemote, engine.RefTag, engine.RefArchived, engine.RefRewritten, engine.RefBackup} {
			if counts[k] > 0 {
				parts = append(parts, i18n.T("show.ref_"+k, "count", counts[k]))
			}
		}
		if len(parts) > 0 {
			fmt.Fprintln(w, st.Muted.Render("    "+strings.Join(parts, " · ")))
		}
	}
	if len(o.History) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Title.Render(i18n.T("show.history")))
		for _, h := range o.History {
			line := fmt.Sprintf("  #%-4d %-9s", h.RunID, h.Outcome)
			msg := h.Message
			fmt.Fprintln(w, st.Text.Render(line)+st.Muted.Render(" "+msg+ago(h.FinishedAt)))
		}
	}
}

// ---- activity ----

func (a *app) activityCmd() *cobra.Command {
	var sinceFlag, owner, tag, repo string
	var types []string
	var limit int
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show what changed across the archive, newest first",
		Long: "A timeline built from what past syncs recorded: new commits, branches and tags,\n" +
			"history kept after a force-push, and events like rescues and renames.\n" +
			"It reads only the archive database, so it works offline.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			f := store.ActivityFilter{Owner: owner, Tag: tag, Types: types, Limit: limit}
			if sinceFlag != "" {
				d, err := config.ParseDuration(sinceFlag)
				if err != nil || d <= 0 {
					return &usageError{fmt.Errorf("invalid --since %q; use e.g. 24h or 7d", sinceFlag)}
				}
				f.Since = time.Now().Add(-d)
			}
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			if repo != "" {
				r, err := s.st.FindRepo(ctx, repo)
				if err != nil {
					return err
				}
				f.RepoID = r.ID
			}
			feed, err := s.st.Activity(ctx, f)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if feed == nil {
					feed = []store.Activity{}
				}
				return a.printJSON(map[string]any{"schema": jsonSchema, "activity": feed})
			}
			a.printActivity(feed)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&sinceFlag, "since", "", "only changes newer than this, e.g. 24h or 7d")
	fl.StringVar(&owner, "owner", "", "only repos of this owner")
	fl.StringVar(&tag, "tag", "", "only repos with this tag")
	fl.StringVar(&repo, "repo", "", "only this repo")
	fl.StringSliceVar(&types, "type", nil, "only these event types, e.g. refs, rescued, renamed")
	fl.IntVar(&limit, "limit", 200, "show at most this many entries")
	return cmd
}

func (a *app) printActivity(feed []store.Activity) {
	st := a.styles
	if len(feed) == 0 {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("activity.empty")))
		return
	}
	day := ""
	for _, e := range feed {
		if d := dayLabel(e.At); d != day {
			if day != "" {
				fmt.Fprintln(a.out)
			}
			day = d
			fmt.Fprintln(a.out, st.Title.Render(d))
		}
		name := e.Path
		if name == "" {
			name = "—"
		}
		msg := e.Message
		icon, style := "•", st.Muted
		switch e.Type {
		case store.ActivityRefs:
			icon, style = "●", st.OK
			msg = describeActivity(e.Changes)
		case "rescued":
			icon, style = "▲", st.Accent
		case "renamed":
			icon, style = "↻", st.Accent
		case "conflict", "failed":
			icon, style = "!", st.Warn
		}
		if msg == "" {
			msg = e.Type
		}
		fmt.Fprintf(a.out, "  %s %s  %s\n", style.Render(icon), st.Label.Render(fmt.Sprintf("%-28s", name)), st.Text.Render(msg))
	}
}

// describeActivity extends describe with what the archive kept.
func describeActivity(changes []core.RefChange) string {
	msg := describe(changes)
	kept, rewritten := 0, 0
	for _, c := range changes {
		switch {
		case strings.HasPrefix(c.Ref, "refs/chitragit/archived/"):
			kept++
		case strings.HasPrefix(c.Ref, "refs/chitragit/rewritten/") || c.Kind == "rewrite":
			rewritten++
		}
	}
	var extra []string
	if rewritten > 0 {
		extra = append(extra, i18n.T("activity.rewritten"))
	}
	if kept > 0 {
		extra = append(extra, i18n.T("activity.kept", "count", kept))
	}
	if len(extra) > 0 {
		if msg != "" {
			msg += " · "
		}
		msg += strings.Join(extra, " · ")
	}
	if msg == "" {
		msg = i18n.T("activity.refs_moved", "count", len(changes))
	}
	return msg
}

func dayLabel(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	t = t.Local()
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return i18n.T("activity.today")
	case now.AddDate(0, 0, -1).Format(time.DateOnly) == t.Format(time.DateOnly):
		return i18n.T("activity.yesterday")
	}
	return t.Format("Mon 2 Jan 2006")
}

// ---- health ----

func (a *app) healthCmd() *cobra.Command {
	var measure bool
	var card string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show the archive's health: problems, disk usage and recent runs",
		Long: "Counts the repos that need a look (failing, diverged, dirty, stale…), the\n" +
			"largest repos on disk and the last runs. --card <name> lists the repos behind\n" +
			"one count. --measure first measures every repo's size on disk.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			staleAfter := time.Duration(s.cfg.Guardian.StaleAfter)
			if card != "" {
				return a.printCard(ctx, s.st, card, staleAfter)
			}
			if measure {
				repos, err := s.st.Repos(ctx)
				if err != nil {
					return err
				}
				if err := engine.MeasureSizes(ctx, s.arc.Root, repos, s.st); err != nil {
					return err
				}
			}
			h, err := s.st.Health(ctx, staleAfter)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(struct {
					Schema string `json:"schema"`
					*store.Health
				}{jsonSchema, h})
			}
			a.printHealth(h)
			return nil
		},
	}
	cmd.Flags().BoolVar(&measure, "measure", false, "measure every repo's size on disk first (reads every file)")
	cmd.Flags().StringVar(&card, "card", "", "list the repos behind one count: "+strings.Join(store.Cards, ", "))
	return cmd
}

func (a *app) printCard(ctx context.Context, st *store.Store, card string, staleAfter time.Duration) error {
	idList, err := st.RepoIDs(ctx, card, staleAfter)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range idList {
		want[id] = true
	}
	all, err := st.Repos(ctx)
	if err != nil {
		return err
	}
	repos := []core.Repo{}
	for _, r := range all {
		if want[r.ID] {
			repos = append(repos, r)
		}
	}
	slices.SortFunc(repos, func(x, y core.Repo) int { return strings.Compare(x.Path, y.Path) })
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "card": card, "repos": repos})
	}
	for _, r := range repos {
		fmt.Fprintln(a.out, a.styles.Label.Render(r.Path)+"  "+a.styles.Muted.Render(r.StatusReason))
	}
	return nil
}

func (a *app) printHealth(h *store.Health) {
	st, w := a.styles, a.out
	fmt.Fprintln(w, st.Title.Render(i18n.T("health.title", "count", h.Total)))
	allClear := true
	for _, c := range store.Cards {
		n := h.Cards[c]
		style := st.Muted
		switch {
		case n == 0:
		case c == store.CardRescued:
			style = st.Accent
		case c == store.CardFailing || c == store.CardMissing:
			style = st.Fail
		case c == store.CardLocked || c == store.CardFavourites:
			style = st.Text
		default:
			style = st.Warn
		}
		if n > 0 && c != store.CardLocked && c != store.CardFavourites {
			allClear = false
		}
		fmt.Fprintf(w, "  %s %s\n", style.Render(fmt.Sprintf("%6d", n)), style.Render(i18n.T("health.card."+c)))
	}
	if allClear {
		fmt.Fprintln(w, st.OK.Render("  ✓ "+i18n.T("health.all_clear")))
	}

	if len(h.Failing) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Title.Render(i18n.T("health.failing")))
		for _, f := range h.Failing {
			fmt.Fprintln(w, "  "+st.Fail.Render("✗ ")+st.Label.Render(f.Path)+"  "+st.Text.Render(f.Reason))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, st.Title.Render(i18n.T("health.disk")))
	if h.Measured == 0 {
		fmt.Fprintln(w, st.Muted.Render("  "+i18n.T("health.disk_unmeasured")))
	} else {
		fmt.Fprintln(w, "  "+st.Text.Render(i18n.T("health.disk_total", "size", humanBytes(h.DiskTotal), "count", h.Measured)))
		for i, r := range h.Largest {
			if i == 5 {
				break
			}
			fmt.Fprintf(w, "  %s  %s\n", st.Accent.Render(fmt.Sprintf("%9s", humanBytes(r.SizeBytes))), st.Label.Render(r.Path))
		}
	}

	if len(h.Runs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.Title.Render(i18n.T("health.runs")))
		for _, r := range h.Runs {
			if r.Kind != "sync" {
				continue
			}
			line := fmt.Sprintf("  #%-4d %-11s %s", r.ID, r.State, i18n.T("health.run_counts",
				"updated", r.Updated, "unchanged", r.Unchanged, "skipped", r.Skipped, "failed", r.Failed))
			style := st.Text
			if r.Failed > 0 || r.State != "finished" {
				style = st.Warn
			}
			fmt.Fprintln(w, style.Render(line)+st.Muted.Render(ago(r.StartedAt)))
		}
	}
}

// ---- tags and favourites ----

func (a *app) tagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Label repositories with your own tags",
		Long:  "Tags are your own labels, e.g. work, fonts, to-read. Filter with `activity --tag`.",
	}
	var only string
	edit := func(use, short string, add bool) *cobra.Command {
		c := &cobra.Command{
			Use:   use + " <tag[,tag…]> [repo…]",
			Short: short,
			Args:  argsN(1, 1<<20),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				s, err := a.open(ctx, "", false)
				if err != nil {
					return err
				}
				defer s.close()
				repos, err := pickRepos(ctx, s.st, args[1:], only)
				if err != nil {
					return err
				}
				tags := strings.Split(args[0], ",")
				if add {
					err = s.st.AddTags(ctx, ids(repos), tags)
				} else {
					err = s.st.RemoveTags(ctx, ids(repos), tags)
				}
				if err != nil {
					return err
				}
				return a.done(i18n.T("tag."+use+"_done", "tags", args[0], "count", len(repos)), repos)
			},
		}
		c.Flags().StringVar(&only, "only", "", "also every repo whose path matches this glob")
		return c
	}
	ls := &cobra.Command{
		Use:   "ls [repo]",
		Short: "List tags in use, or one repo's tags",
		Args:  argsN(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			if len(args) == 1 {
				r, err := s.st.FindRepo(ctx, args[0])
				if err != nil {
					return err
				}
				tags, err := s.st.Tags(ctx, r.ID)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return a.printJSON(map[string]any{"schema": jsonSchema, "path": r.Path, "tags": tags})
				}
				for _, t := range tags {
					fmt.Fprintln(a.out, t)
				}
				return nil
			}
			all, err := s.st.AllTags(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if all == nil {
					all = []store.TagCount{}
				}
				return a.printJSON(map[string]any{"schema": jsonSchema, "tags": all})
			}
			for _, t := range all {
				fmt.Fprintf(a.out, "%s  %s\n", a.styles.Label.Render(t.Tag), a.styles.Muted.Render(i18n.T("tag.repos", "count", t.Repos)))
			}
			return nil
		},
	}
	cmd.AddCommand(edit("add", "Add tags to repositories", true), edit("rm", "Remove tags from repositories", false), ls)
	return cmd
}

func (a *app) favCmd(fav bool) *cobra.Command {
	use, short := "fav", "Mark repositories as favourites (synced first, shown with ★)"
	if !fav {
		use, short = "unfav", "Remove repositories from favourites"
	}
	var only string
	cmd := &cobra.Command{
		Use:   use + " [repo…]",
		Short: short,
		Args:  argsN(0, 1<<20),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			repos, err := pickRepos(ctx, s.st, args, only)
			if err != nil {
				return err
			}
			if err := s.st.SetFavourite(ctx, ids(repos), fav); err != nil {
				return err
			}
			return a.done(i18n.T("fav."+use+"_done", "count", len(repos)), repos)
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "also every repo whose path matches this glob")
	return cmd
}

// done reports a bulk change.
func (a *app) done(msg string, repos []core.Repo) error {
	if a.jsonOut {
		paths := make([]string, len(repos))
		for i, r := range repos {
			paths[i] = r.Path
		}
		return a.printJSON(map[string]any{"schema": jsonSchema, "repos": paths})
	}
	fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Text.Render(msg))
	if a.verbose {
		for _, r := range repos {
			fmt.Fprintln(a.out, a.styles.Muted.Render("  "+r.Path))
		}
	}
	return nil
}

// ---- formatting ----

// ago renders "  3h ago" from a stored timestamp; empty if unknown.
func ago(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "  " + i18n.T("time.just_now")
	case d < time.Hour:
		return "  " + i18n.T("time.minutes_ago", "count", int(d.Minutes()))
	case d < 48*time.Hour:
		return "  " + i18n.T("time.hours_ago", "count", int(d.Hours()))
	case d < 60*24*time.Hour:
		return "  " + i18n.T("time.days_ago", "count", int(d.Hours()/24))
	}
	return "  " + t.Local().Format("2 Jan 2006")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
