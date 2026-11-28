package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/version"
)

// jsonSchema versions every JSON document the CLI prints (PRD F17).
const jsonSchema = "chitragit/v1"

// session is what scan, sync, status and ls need: the archive, its
// settings and database, and (for commands that write) git and the lock.
type session struct {
	arc  *engine.Archive
	cfg  *config.Config
	st   *engine.Store
	git  *engine.GitRunner
	lock *engine.ArchiveLock
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
		var v engine.GitVersion
		if s.git, v, err = engine.Git(ctx, 0); err != nil {
			return nil, err
		}
		if ids := v.KnownCVEs(); len(ids) > 0 && cfg.Sync.Submodules {
			// Both flaws need submodules, so stop fetching them until git is updated.
			cfg.Sync.Submodules = false
			a.warn(i18n.T("sync.git_cves", "version", v, "ids", strings.Join(ids, ", ")))
		}
		if cfg.Sync.LFS == "always" {
			s.git.DownloadLFS()
		}
		if s.lock, err = arc.Acquire(command); err != nil {
			return nil, err
		}
	}
	if s.st, err = engine.OpenStore(ctx, arc.DBFile()); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *session) scanOptions() engine.ScanOptions {
	return engine.ScanOptions{MaxDepth: s.cfg.Scan.MaxDepth, Exclude: s.cfg.Scan.Exclude, FollowSymlinks: s.cfg.Scan.FollowSymlinks,
		CacheFile: s.arc.ScanCacheFile()}
}

func (a *app) scanCmd() *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Find every repository in the archive",
		Long: "Walks the archive folder and records every repository it finds: normal clones,\n" +
			"bare/mirror clones, linked worktrees and repos nested inside other repos.\n" +
			"Each repo gets a stable ID saved in its own git config (chitragit.id), so a\n" +
			"moved or renamed folder keeps its history. Folders that disappeared are marked\n" +
			"missing; nothing is ever deleted. A rescan reads only folders that changed since\n" +
			"the last one; --full reads every folder.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open(cmd.Context(), "chitragit scan", true)
			if err != nil {
				return err
			}
			defer s.close()
			opts := s.scanOptions()
			opts.Full = full
			rep, err := engine.Scan(cmd.Context(), s.arc.Root, opts, s.git, s.st)
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
	cmd.Flags().BoolVar(&full, "full", false, "read every folder, not only those changed since the last scan")
	return cmd
}

func (a *app) printScan(rep *engine.ScanReport) {
	for _, w := range rep.Warnings {
		a.warn(w)
	}
	line := i18n.T("scan.summary", "found", rep.Found, "added", rep.Added, "moved", rep.Moved, "missing", rep.Missing)
	fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(line))
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
		dryRun   bool
		verify   bool
		tag      string

		followBlocked bool // a new repo from a followed source was quarantined
	)
	cmd := &cobra.Command{
		Use:     "sync",
		Aliases: []string{"pull", "fetch"},
		Short:   "Update repositories from their remotes",
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
			"        dry-run  ask the remotes what changed, download nothing\n\n" +
			"As in git, `pull` is sync and `fetch` is sync --mode fetch.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if resume && fresh {
				return &usageError{fmt.Errorf("use either --resume or --fresh, not both")}
			}
			if tag != "" {
				if err := engine.CheckSnapshotName(tag); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit sync", true)
			if err != nil {
				return err
			}
			defer s.close()
			if a.headless {
				done, err := a.startRunLog(s.arc.LogsDir())
				if err != nil {
					return err
				}
				defer done()
			}
			switch {
			case cmd.Flags().Changed("mode"):
				s.cfg.Sync.Mode = mode
			case cmd.CalledAs() == "fetch":
				s.cfg.Sync.Mode, mode = "fetch", "fetch"
			}
			if dryRun {
				s.cfg.Sync.Mode, mode = "dry-run", "dry-run"
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
					opts, err := s.cloneOptions()
					if err != nil {
						return err
					}
					frep, err := engine.Follow(ctx, opts, registry(ctx, s.cfg), s.git, s.st)
					if err != nil && frep == nil {
						return err
					}
					if !a.jsonOut && frep != nil {
						a.printFollow(frep)
					}
					if frep != nil && frep.Cloned > 0 {
						noScan = false
					}
					for _, src := range frepSources(frep) {
						followBlocked = followBlocked || len(src.Blocked) > 0
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

			opts, shield, err := s.syncOptions()
			if err != nil {
				return err
			}
			req := engine.SyncRequest{Root: s.arc.Root, Only: only, Options: opts,
				OnInterrupted: s.cfg.Sync.OnInterrupted, Baseline: shield}
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
			// Verify only after a sync that ran to the end: an interrupted
			// one leaves the archive half updated, and the next run finishes it.
			var vrep *engine.VerifyReport
			if verify && err == nil && !rep.Interrupted && rep.Mode != "dry-run" {
				if vrep, err = engine.Verify(ctx, s.arc.Root, s.git, s.st, engine.VerifyOptions{}); err != nil {
					return err
				}
			}
			// Name the state the sync left, like `tag` right after it; a
			// half-done or dry run has no state worth a name.
			var named []engine.NamedSnapshot
			namedFailed := 0
			if tag != "" && err == nil && !rep.Interrupted && rep.Mode != "dry-run" {
				repos := make([]core.Repo, 0, len(rep.Results))
				for _, r := range rep.Results {
					repos = append(repos, r.Repo)
				}
				if named, err = engine.NameSnapshots(ctx, s.arc.Root, tag, repos, s.git, s.st); err != nil {
					return err
				}
				for _, n := range named {
					if n.Error != "" {
						namedFailed++
					}
				}
			}
			if a.jsonOut {
				if jerr := a.printJSON(syncJSON(rep, vrep, named)); jerr != nil {
					return jerr
				}
			} else {
				a.printSyncSummary(rep)
				if vrep != nil {
					fmt.Fprintln(a.out)
					a.printVerify(vrep)
				}
				if tag != "" && named != nil {
					for _, n := range named {
						if n.Error != "" {
							a.warn(n.Path + ": " + n.Error)
						}
					}
					fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("tag.done", "name", tag, "count", len(named)-namedFailed))
				}
			}
			a.notifyRun(ctx, s.cfg, rep, vrep)
			switch {
			case err != nil:
				return err
			case rep.Interrupted:
				return &silentError{code: core.ExitInterrupted}
			case blocked(rep) || followBlocked:
				return &silentError{code: core.ExitBlocked}
			case rep.Counts[engine.Failed] > 0, vrep != nil && !vrep.OK, namedFailed > 0:
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&mode, "mode", "", "ff | fetch | dry-run (default from config: sync.mode)")
	f.BoolVar(&dryRun, "dry-run", false, "same as --mode dry-run")
	f.IntVar(&workers, "workers", 0, "repos to sync in parallel (default from config: sync.workers)")
	f.StringVar(&only, "only", "", "sync only repos whose path matches this glob, e.g. 'torvalds/*'")
	f.BoolVar(&noScan, "no-scan", false, "skip the quick folder scan before syncing")
	f.BoolVar(&noFollow, "no-follow", false, "do not check followed sources for new repos")
	f.BoolVar(&resume, "resume", false, "if the last sync was interrupted, finish only the repos it had not done")
	f.BoolVar(&fresh, "fresh", false, "if the last sync was interrupted, start over with every repo")
	f.BoolVar(&verify, "verify", false, "run `verify` after the sync and fail if a check fails")
	f.StringVar(&tag, "tag", "", "name the state the sync leaves, as `tag <name>` would (for restore and diff)")

	return cmd
}

// startRunLog copies everything the command prints into a new log file
// in the archive, and removes logs older than 30 days. The returned func
// closes the file.
func (a *app) startRunLog(dir string) (func(), error) {
	now := time.Now()
	if _, err := engine.PruneRunLogs(dir, engine.RunLogsKept, now); err != nil {
		a.warn("cannot clean old logs: " + err.Error())
	}
	f, err := engine.CreateRunLog(dir, "sync", now)
	if err != nil {
		return nil, &core.Error{Category: core.CatDisk, Message: "cannot create a log file in " + dir, Err: err}
	}
	fmt.Fprintf(f, "%s  %s\n\n", version.String(), now.Format(time.RFC3339))
	a.out, a.errOut = io.MultiWriter(a.out, f), io.MultiWriter(a.errOut, f)
	return func() { _ = f.Close() }, nil
}

// notifyRun shows the end-of-run desktop notification (PRD F15). A
// notification that cannot be shown never changes the run's result.
func (a *app) notifyRun(ctx context.Context, cfg *config.Config, rep *engine.SyncReport, vrep *engine.VerifyReport) {
	if rep.Mode == "dry-run" {
		return
	}
	c := rep.Counts
	total := c[engine.Updated] + c[engine.Unchanged] + c[engine.Skipped] + c[engine.Failed]
	body := i18n.T("notify.run_body", "total", engine.NotifyGroup(total), "updated", engine.NotifyGroup(c[engine.Updated]),
		"failed", engine.NotifyGroup(c[engine.Failed]))
	if rep.Interrupted {
		body = i18n.T("notify.run_stopped", "done", engine.NotifyGroup(total))
	}
	sender := a.notifier
	if sender == nil {
		sender = engine.NativeNotifier()
	}
	n := engine.Notifier{Desktop: cfg.Notify.Desktop, On: cfg.Notify.On, Sender: sender}
	msgs := []engine.Notification{{Event: engine.NotifyRunFinished, Title: i18n.T("notify.title"), Body: body}}
	for _, r := range rep.Rescued {
		msgs = append(msgs, engine.Notification{Event: engine.NotifyRescued, Title: i18n.T("notify.title"),
			Body: i18n.T("notify.rescued", "path", r.Path)})
	}
	if vrep != nil && !vrep.OK {
		msgs = append(msgs, engine.Notification{Event: engine.NotifyRunFinished, Title: i18n.T("notify.title"),
			Body: i18n.T("notify.verify_failed")})
	}
	for _, m := range msgs {
		if err := n.Notify(ctx, m); err != nil && a.verbose {
			a.warn("desktop notification: " + err.Error())
		}
	}
}

// printResult prints one line per repo that needs a look: updated, skipped,
// failed, or not fully applied. Unchanged repos only show with --verbose.
func (a *app) printResult(e engine.SyncEvent) {
	if e.Kind != engine.RepoFinished || e.Result.Outcome == "" {
		return
	}
	r := e.Result
	name := a.styles.Label.Render(r.Repo.Path)
	note := r.Reason
	switch {
	case r.Outcome == engine.Failed:
		fmt.Fprintln(a.out, a.styles.Fail.Render(a.icon(iconFail))+name+"  "+a.styles.Text.Render(note))
		a.printDetail(r.Err)
		return
	case r.Outcome == engine.Skipped || (r.Status != engine.StatusOK && r.Status != engine.StatusAhead):
		fmt.Fprintln(a.out, a.styles.Warn.Render(a.icon(iconWarn))+name+"  "+a.styles.Text.Render(note))
	case r.Outcome == engine.Updated:
		if note == "" {
			note = engine.Describe(r.Changes)
		}
		fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+name+"  "+a.styles.Muted.Render(note))
	case a.verbose:
		fmt.Fprintln(a.out, a.styles.Muted.Render(a.icon(iconInfo)+r.Repo.Path+"  "+i18n.T("sync.unchanged")))
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
	line := i18n.T("sync.summary", "updated", c[engine.Updated], "unchanged", c[engine.Unchanged],
		"skipped", c[engine.Skipped], "failed", c[engine.Failed], "time", rep.Duration.Round(100*time.Millisecond))
	style := a.styles.OK
	if c[engine.Failed] > 0 || rep.Interrupted {
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
	if b := rep.Baseline; b != nil && b.Judged > 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("sync.baseline", "judged", b.Judged, "held", len(b.Blocked))))
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

func syncJSON(rep *engine.SyncReport, vrep *engine.VerifyReport, named []engine.NamedSnapshot) any {
	repos := make([]repoJSON, 0, len(rep.Results))
	for _, r := range rep.Results {
		repos = append(repos, repoJSON{Path: r.Repo.Path, ID: r.Repo.ID, Outcome: r.Outcome, Status: r.Status,
			Reason: r.Reason, Category: r.Category, Attempts: r.Attempts,
			Seconds: r.Finished.Sub(r.Started).Seconds(), Changes: r.Changes})
	}
	return struct {
		Schema string `json:"schema"`
		*engine.SyncReport
		Repos  []repoJSON             `json:"repos"`
		Verify *engine.VerifyReport   `json:"verify,omitempty"`
		Named  []engine.NamedSnapshot `json:"named,omitempty"`
	}{jsonSchema, rep, repos, vrep, named}
}

func (a *app) statusCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
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
				if short {
					fmt.Fprintf(a.out, "%s %s\n", st, r.Path)
					continue
				}
				line := fmt.Sprintf("%-13s %s", st, r.Path)
				switch st {
				case engine.StatusOK, engine.StatusAhead, "unknown":
					fmt.Fprintln(a.out, a.styles.OK.Render(line)+a.styles.Muted.Render(since(r.LastSyncedAt)))
				case engine.StatusFailed, "missing":
					fmt.Fprintln(a.out, a.styles.Fail.Render(line)+"  "+a.styles.Text.Render(r.StatusReason))
				default:
					fmt.Fprintln(a.out, a.styles.Warn.Render(line)+"  "+a.styles.Text.Render(r.StatusReason))
				}
			}
			if !short {
				a.printCounts(len(repos), counts)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&short, "short", "s", false, "one plain line per repo: status and path")
	cmd.Flags().BoolVar(&short, "porcelain", false, "same as --short; the format stays stable for scripts")
	return cmd
}

func (a *app) lsCmd() *cobra.Command {
	var only, status, language, topic string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List repositories in the archive",
		Long: "Lists every repo, sorted by path. --language and --topic use what the forge said\n" +
			"about each repo at the last sync or follow with a token (see `chitragit show`);\n" +
			"repos it said nothing about, such as plain git remotes, never match them.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repos, err := a.repos(cmd.Context(), only, status)
			if err != nil {
				return err
			}
			if language != "" || topic != "" {
				if repos, err = a.filterMeta(cmd.Context(), repos, language, topic); err != nil {
					return err
				}
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
	cmd.Flags().StringVar(&language, "language", "", "only repos whose main language is this, e.g. go")
	cmd.Flags().StringVar(&topic, "topic", "", "only repos with this topic")
	return cmd
}

// filterMeta keeps the repos whose stored forge details match language and
// topic (either may be empty), ignoring case.
func (a *app) filterMeta(ctx context.Context, repos []core.Repo, language, topic string) ([]core.Repo, error) {
	s, err := a.open(ctx, "", false)
	if err != nil {
		return nil, err
	}
	defer s.close()
	meta, err := s.st.AllRepoMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := repos[:0]
	for _, r := range repos {
		m, ok := meta[r.ID]
		hasTopic := slices.ContainsFunc(m.Topics, func(t string) bool { return strings.EqualFold(t, topic) })
		if ok && (language == "" || strings.EqualFold(m.Language, language)) && (topic == "" || hasTopic) {
			out = append(out, r)
		}
	}
	return out, nil
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

// blocked reports whether KodeKavach Shield held back any repo of the run.
func blocked(rep *engine.SyncReport) bool {
	for _, r := range rep.Results {
		if r.Category == string(core.CatBlocked) {
			return true
		}
	}
	return false
}

func frepSources(rep *engine.FollowReport) []engine.SourceReport {
	if rep == nil {
		return nil
	}
	return rep.Sources
}
