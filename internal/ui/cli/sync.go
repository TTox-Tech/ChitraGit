package cli

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// jsonSchema versions every JSON document the CLI prints (PRD F17).
const jsonSchema = "chitragit/v1"

// session is what scan, sync, status and ls need: the archive, its
// settings and database, and (for commands that write) git and the lock.
type session struct {
	arc  *archive.Archive
	cfg  *config.Config
	st   *store.Store
	git  *gitx.Runner
	lock *archive.Lock
}

func (s *session) close() {
	if s.st != nil {
		s.st.Close()
	}
	_ = s.lock.Release()
}

// open prepares a session. With write set it also finds git and takes the
// archive lock, so two runs never change the same archive at once.
func (a *app) open(ctx context.Context, command string, write bool) (*session, error) {
	arc, err := a.findArchive()
	if err != nil {
		return nil, err
	}
	cfg, warnings, err := a.loadConfig(arc)
	if err != nil {
		return nil, err
	}
	a.printWarnings(warnings)
	s := &session{arc: arc, cfg: cfg}
	if write {
		hooks, err := platform.EmptyHooksDir()
		if err != nil {
			return nil, err
		}
		if s.git, err = gitx.New(gitx.Options{HooksDir: hooks}); err != nil {
			return nil, err
		}
		if _, err := s.git.CheckVersion(ctx); err != nil {
			return nil, err
		}
		if s.lock, err = arc.Acquire(command); err != nil {
			return nil, err
		}
	}
	if s.st, err = store.Open(ctx, arc.DBFile()); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *session) scanOptions() scan.Options {
	return scan.Options{MaxDepth: s.cfg.Scan.MaxDepth, Exclude: s.cfg.Scan.Exclude, FollowSymlinks: s.cfg.Scan.FollowSymlinks}
}

func (a *app) scanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Find every repository in the archive",
		Long: "Walks the archive folder and records every repository it finds: normal clones,\n" +
			"bare/mirror clones, linked worktrees and repos nested inside other repos.\n" +
			"Each repo gets a stable ID saved in its own git config (chitragit.id), so a\n" +
			"moved or renamed folder keeps its history. Folders that disappeared are marked\n" +
			"missing; nothing is ever deleted.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open(cmd.Context(), "chitragit scan", true)
			if err != nil {
				return err
			}
			defer s.close()
			rep, err := engine.Scan(cmd.Context(), s.arc.Root, s.scanOptions(), s.git, s.st)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(struct {
					Schema string `json:"schema"`
					*engine.ScanReport
				}{jsonSchema, rep})
			}
			a.printScan(rep)
			return nil
		},
	}
}

func (a *app) printScan(rep *engine.ScanReport) {
	for _, w := range rep.Warnings {
		a.warn(w)
	}
	line := i18n.T("scan.summary", "found", rep.Found, "added", rep.Added, "moved", rep.Moved, "missing", rep.Missing)
	fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Text.Render(line))
	if rep.Returned > 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("scan.returned", "count", rep.Returned)))
	}
}

func (a *app) syncCmd() *cobra.Command {
	var (
		mode     string
		workers  int
		only     string
		noScan   bool
		noFollow bool
		resume   bool
		fresh    bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Update repositories from their remotes",
		Long: "Downloads new commits, branches and tags for every repo, then fast-forwards\n" +
			"branches when that is safe: a fast-forward simply moves a branch ahead to the\n" +
			"new commits, so nothing is merged, rebased or overwritten.\n\n" +
			"Local changes are put aside (stashed), the branch is updated, and the changes\n" +
			"are put back. If they clash with the update, the repo is restored exactly as it\n" +
			"was and the summary says so. Branches deleted upstream are kept under\n" +
			"refs/chitragit/archived/, and history replaced by a force-push under\n" +
			"refs/chitragit/rewritten/, so the archive never loses a commit.\n\n" +
			"Repos with local commits, a detached HEAD or a merge in progress still get the\n" +
			"new data, but their branches are left alone, with the reason in the summary.\n\n" +
			"Modes:  ff (default) download and fast-forward · fetch  download only\n" +
			"        dry-run  ask the remotes what changed, download nothing",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if resume && fresh {
				return &usageError{fmt.Errorf("use either --resume or --fresh, not both")}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit sync", true)
			if err != nil {
				return err
			}
			defer s.close()
			if cmd.Flags().Changed("mode") {
				s.cfg.Sync.Mode = mode
			}
			if cmd.Flags().Changed("workers") {
				s.cfg.Sync.Workers = workers
			}
			if err := s.cfg.Validate(); err != nil {
				return err
			}

			if !noFollow && mode != "dry-run" && s.cfg.Sync.Mode != "dry-run" {
				srcs, err := s.st.Sources(ctx)
				if err != nil {
					return err
				}
				if len(srcs) > 0 {
					frep, err := engine.Follow(ctx, s.cloneOptions(), registry(ctx, s.cfg), s.git, s.st)
					if err != nil && frep == nil {
						return err
					}
					if !a.jsonOut && frep != nil {
						a.printFollow(frep)
					}
					if frep != nil && frep.Cloned > 0 {
						noScan = false
					}
				}
			}

			if !noScan {
				rep, err := engine.Scan(ctx, s.arc.Root, s.scanOptions(), s.git, s.st)
				if err != nil {
					return err
				}
				if !a.jsonOut {
					a.printScan(rep)
				}
			}

			req := engine.SyncRequest{Root: s.arc.Root, Only: only, Options: engine.SyncOptions(s.cfg.Sync),
				OnInterrupted: s.cfg.Sync.OnInterrupted}
			s.withProviders(ctx, &req)
			if resume {
				req.OnInterrupted = "resume"
			} else if fresh {
				req.OnInterrupted = "fresh"
			}
			if !a.jsonOut {
				req.Listen = a.printResult
			}
			rep, err := engine.Sync(ctx, req, s.git, s.st)
			if rep == nil {
				return err
			}
			if a.jsonOut {
				if jerr := a.printJSON(syncJSON(rep)); jerr != nil {
					return jerr
				}
			} else {
				a.printSyncSummary(rep)
			}
			switch {
			case err != nil:
				return err
			case rep.Interrupted:
				return &silentError{code: core.ExitInterrupted}
			case rep.Counts[syncer.Failed] > 0:
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&mode, "mode", "", "ff | fetch | dry-run (default from config: sync.mode)")
	f.IntVar(&workers, "workers", 0, "repos to sync in parallel (default from config: sync.workers)")
	f.StringVar(&only, "only", "", "sync only repos whose path matches this glob, e.g. 'torvalds/*'")
	f.BoolVar(&noScan, "no-scan", false, "skip the quick folder scan before syncing")
	f.BoolVar(&noFollow, "no-follow", false, "do not check followed sources for new repos")
	f.BoolVar(&resume, "resume", false, "if the last sync was interrupted, finish only the repos it had not done")
	f.BoolVar(&fresh, "fresh", false, "if the last sync was interrupted, start over with every repo")

	return cmd
}

// printResult prints one line per repo that needs a look: updated, skipped,
// failed, or not fully applied. Unchanged repos only show with --verbose.
func (a *app) printResult(e syncer.Event) {
	if e.Kind != syncer.RepoFinished || e.Result.Outcome == "" {
		return
	}
	r := e.Result
	name := a.styles.Label.Render(r.Repo.Path)
	note := r.Reason
	switch {
	case r.Outcome == syncer.Failed:
		fmt.Fprintln(a.out, a.styles.Fail.Render("✗ ")+name+"  "+a.styles.Text.Render(note))
		a.printDetail(r.Err)
		return
	case r.Outcome == syncer.Skipped || (r.Status != syncer.StatusOK && r.Status != syncer.StatusAhead):
		fmt.Fprintln(a.out, a.styles.Warn.Render("! ")+name+"  "+a.styles.Text.Render(note))
	case r.Outcome == syncer.Updated:
		if note == "" {
			note = engine.Describe(r.Changes)
		}
		fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+name+"  "+a.styles.Muted.Render(note))
	case a.verbose:
		fmt.Fprintln(a.out, a.styles.Muted.Render("· "+r.Repo.Path+"  "+i18n.T("sync.unchanged")))
	}
}

func (a *app) printDetail(err error) {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return
	}
	if ce.Hint != "" {
		fmt.Fprintln(a.out, a.styles.Accent.Render("    "+i18n.T("error.hint")+": ")+a.styles.Text.Render(ce.Hint))
	}
	if a.verbose && ce.Detail != "" {
		fmt.Fprintln(a.out, a.styles.Muted.Render(indent(indent(ce.Detail))))
	}
}

func (a *app) printSyncSummary(rep *engine.SyncReport) {
	c := rep.Counts
	line := i18n.T("sync.summary", "updated", c[syncer.Updated], "unchanged", c[syncer.Unchanged],
		"skipped", c[syncer.Skipped], "failed", c[syncer.Failed], "time", rep.Duration.Round(100*time.Millisecond))
	style := a.styles.OK
	if c[syncer.Failed] > 0 || rep.Interrupted {
		style = a.styles.Warn
	}
	for _, m := range rep.Moved {
		fmt.Fprintln(a.out, a.styles.Accent.Render("→ ")+a.styles.Label.Render(m.Path)+"  "+
			a.styles.Text.Render(i18n.T("sync.moved", "to", m.To)))
	}
	for _, r := range rep.Rescued {
		line := a.styles.Accent.Render("◆ RESCUED ") + a.styles.Label.Render(r.Path) + "  " + a.styles.Text.Render(r.Message)
		if r.Bundle != "" {
			line += a.styles.Muted.Render("  (" + i18n.T("sync.bundled") + ")")
		}
		fmt.Fprintln(a.out, line)
	}
	for _, w := range rep.Warnings {
		a.warn(w)
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, style.Render(line))
	if rep.SmartSkips > 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("sync.smart_skipped", "count", rep.SmartSkips)))
	}
	if rep.ResumedFrom != 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("sync.resumed", "run", rep.ResumedFrom)))
	}
	if rep.Mode == "dry-run" {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("sync.dry_run")))
	}
	if rep.Interrupted {
		fmt.Fprintln(a.out, a.styles.Warn.Render(i18n.T("sync.interrupted")))
	}
}

type repoJSON struct {
	Path     string           `json:"path"`
	ID       string           `json:"id"`
	Outcome  string           `json:"outcome"`
	Status   string           `json:"status"`
	Reason   string           `json:"reason,omitempty"`
	Category string           `json:"category,omitempty"`
	Attempts int              `json:"attempts"`
	Seconds  float64          `json:"seconds"`
	Changes  []core.RefChange `json:"changes,omitempty"`
}

func syncJSON(rep *engine.SyncReport) any {
	repos := make([]repoJSON, 0, len(rep.Results))
	for _, r := range rep.Results {
		repos = append(repos, repoJSON{Path: r.Repo.Path, ID: r.Repo.ID, Outcome: r.Outcome, Status: r.Status,
			Reason: r.Reason, Category: r.Category, Attempts: r.Attempts,
			Seconds: r.Finished.Sub(r.Started).Seconds(), Changes: r.Changes})
	}
	return struct {
		Schema string `json:"schema"`
		*engine.SyncReport
		Repos []repoJSON `json:"repos"`
	}{jsonSchema, rep, repos}
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the health of every repository, from the last sync",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repos, err := a.repos(cmd.Context(), "", "")
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "repos": repos})
			}
			counts := map[string]int{}
			for _, r := range repos {
				st := statusOf(r)
				counts[st]++
				line := fmt.Sprintf("%-13s %s", st, r.Path)
				switch st {
				case syncer.StatusOK, syncer.StatusAhead, "unknown":
					fmt.Fprintln(a.out, a.styles.OK.Render(line)+a.styles.Muted.Render(since(r.LastSyncedAt)))
				case syncer.StatusFailed, "missing":
					fmt.Fprintln(a.out, a.styles.Fail.Render(line)+"  "+a.styles.Text.Render(r.StatusReason))
				default:
					fmt.Fprintln(a.out, a.styles.Warn.Render(line)+"  "+a.styles.Text.Render(r.StatusReason))
				}
			}
			a.printCounts(len(repos), counts)
			return nil
		},
	}
}

func (a *app) lsCmd() *cobra.Command {
	var only, status string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List repositories in the archive",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repos, err := a.repos(cmd.Context(), only, status)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "repos": repos})
			}
			for _, r := range repos {
				origin := r.Provider
				if r.Owner != "" || r.Name != "" {
					origin += "  " + r.Owner + "/" + r.Name
				}
				fmt.Fprintf(a.out, "%s  %s  %s%s\n", a.styles.Label.Render(r.Path),
					a.styles.Muted.Render(r.Kind), a.styles.Accent.Render(origin), a.badges(r))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "only repos whose path matches this glob")
	cmd.Flags().StringVar(&status, "status", "", "only repos with this status, e.g. diverged, failed, missing")
	return cmd
}

// repos reads the database, optionally filtered, sorted by path.
func (a *app) repos(ctx context.Context, only, status string) ([]core.Repo, error) {
	if only != "" {
		if _, err := path.Match(only, ""); err != nil {
			return nil, &usageError{fmt.Errorf("invalid --only pattern %q", only)}
		}
	}
	s, err := a.open(ctx, "", false)
	if err != nil {
		return nil, err
	}
	defer s.close()
	all, err := s.st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.Repo, 0, len(all))
	for _, r := range all {
		if ok, _ := path.Match(only, r.Path); only != "" && !ok {
			continue
		}
		if status != "" && statusOf(r) != status {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(x, y core.Repo) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// badges renders the guardian's marks on a repo.
func (a *app) badges(r core.Repo) string {
	var out string
	if r.Rescued {
		out += "  " + a.styles.Accent.Render("[RESCUED]")
	} else if r.Locked {
		out += "  " + a.styles.Muted.Render("[locked]")
	}
	if r.ArchivedUpstream {
		out += "  " + a.styles.Muted.Render("[archived upstream]")
	}
	return out
}

func statusOf(r core.Repo) string {
	if r.Missing {
		return "missing"
	}
	return r.Status
}

func (a *app) printCounts(total int, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, a.styles.Title.Render(i18n.T("status.total", "count", total))+"  "+
		a.styles.Muted.Render(strings.Join(parts, " · ")))
}

// since renders "  synced 3h ago" from a stored timestamp.
func since(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "  " + i18n.T("status.synced", "ago", "just now")
	case d < time.Hour:
		return "  " + i18n.T("status.synced", "ago", fmt.Sprintf("%dm ago", int(d.Minutes())))
	case d < 48*time.Hour:
		return "  " + i18n.T("status.synced", "ago", fmt.Sprintf("%dh ago", int(d.Hours())))
	}
	return "  " + i18n.T("status.synced", "ago", fmt.Sprintf("%dd ago", int(d.Hours()/24)))
}
