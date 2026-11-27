package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) diffCmd() *cobra.Command {
	var run int64
	var stat bool
	cmd := &cobra.Command{
		Use:   "diff [snapshot] [snapshot] [repo]",
		Short: "Show what changed between two snapshots, or in the last sync",
		Long: "With no snapshot, shows what the last sync run (or --run N) changed in every\n" +
			"repo it touched; a repo narrows that to one. With one snapshot, compares it with\n" +
			"the repo's refs now; with two, compares them (both of the same repo, see\n" +
			"`chitragit history <repo>` for IDs). Every changed branch, remote-tracking branch\n" +
			"and tag is listed as added, deleted, forward, back or rewritten, with the commits\n" +
			"it gained and dropped. Commits no ref reaches any more are still kept by the\n" +
			"snapshots, so `chitragit restore` can bring them back. Changes nothing.",
		Args: argsN(0, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var ids []int64
			repo := ""
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				switch {
				case err != nil && repo == "":
					repo = arg
				case err != nil:
					return &usageError{fmt.Errorf("give at most one repo, got %q and %q", repo, arg)}
				case id <= 0 || len(ids) == 2:
					return &usageError{fmt.Errorf("%q is not a snapshot ID", arg)}
				default:
					ids = append(ids, id)
				}
			}
			if run != 0 && len(ids) > 0 {
				return &usageError{fmt.Errorf("--run compares a sync run; leave out the snapshot IDs")}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			if s.git, _, err = engine.Git(ctx, 0); err != nil {
				return err
			}
			var diffs []engine.RepoDiff
			var errs []string
			if len(ids) > 0 {
				d, err := a.diffSnapshots(ctx, s, ids, repo)
				if err != nil {
					return err
				}
				diffs = []engine.RepoDiff{*d}
			} else {
				if run == 0 {
					last, err := s.st.LastRun(ctx)
					if err != nil {
						return err
					}
					if last == nil {
						return &core.Error{Category: core.CatConfig, Message: "no sync has run yet",
							Hint: "run `chitragit sync`, or compare snapshots: `chitragit diff <id> [id]`"}
					}
					run = last.ID
				}
				if diffs, errs, err = engine.DiffRun(ctx, s.arc.Root, run, s.git, s.st); err != nil {
					return err
				}
				if repo != "" {
					r, err := s.st.FindRepo(ctx, repo)
					if err != nil {
						return err
					}
					var only []engine.RepoDiff
					for _, d := range diffs {
						if d.RepoID == r.ID {
							only = append(only, d)
						}
					}
					diffs = only
				}
			}
			for _, e := range errs {
				a.warn(e)
			}
			if a.jsonOut {
				if diffs == nil {
					diffs = []engine.RepoDiff{}
				}
				err = a.printJSON(map[string]any{"schema": jsonSchema, "run": run, "diffs": diffs, "errors": errs})
			} else {
				a.printDiffs(diffs, stat)
			}
			if err == nil && len(errs) > 0 {
				err = &silentError{code: core.ExitPartial}
			}
			return err
		},
	}
	cmd.Flags().Int64Var(&run, "run", 0, "show what this sync run changed (see `chitragit log`)")
	cmd.Flags().BoolVar(&stat, "stat", false, "one line per repo, without the refs")
	return cmd
}

// diffSnapshots compares one snapshot with the refs now, or two snapshots.
// A repo given as well must be the one the snapshots are of.
func (a *app) diffSnapshots(ctx context.Context, s *session, ids []int64, repo string) (*engine.RepoDiff, error) {
	sn, err := s.st.Snapshot(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	r, err := s.st.FindRepo(ctx, sn.RepoID)
	if err != nil {
		return nil, err
	}
	if repo != "" {
		if want, err := s.st.FindRepo(ctx, repo); err != nil {
			return nil, err
		} else if want.ID != r.ID {
			return nil, &usageError{fmt.Errorf("snapshot %d is of %s, not %s", ids[0], r.Path, want.Path)}
		}
	}
	to := int64(0)
	if len(ids) == 2 {
		to = ids[1]
	}
	return engine.DiffSnapshots(ctx, s.arc.Root, r, ids[0], to, s.git, s.st)
}

func (a *app) printDiffs(diffs []engine.RepoDiff, stat bool) {
	st := a.styles
	short := func(sha string) string { return sha[:min(12, len(sha))] }
	changed, added, removed := 0, 0, 0
	for _, d := range diffs {
		if len(d.Changes) == 0 {
			continue
		}
		changed++
		added += d.New
		removed += d.Removed
		sum := i18n.T("diff.repo", "refs", len(d.Changes), "new", d.New)
		if d.Removed > 0 {
			sum += ", " + i18n.T("diff.removed", "count", d.Removed)
		}
		fmt.Fprintf(a.out, "%s  %s\n", st.Title.Render(d.Path), st.Muted.Render(sum))
		if stat {
			continue
		}
		for _, c := range d.Changes {
			style, what := st.OK, ""
			switch c.Kind {
			case engine.DiffAdded:
				what = short(c.To)
			case engine.DiffDeleted:
				style, what = st.Warn, short(c.From)
			case engine.DiffBack, engine.DiffRewritten, engine.DiffMoved:
				style, what = st.Warn, short(c.From)+" → "+short(c.To)
			default:
				what = short(c.From) + " → " + short(c.To)
			}
			if c.New > 0 || c.Dropped > 0 {
				what += "  " + i18n.T("diff.ref", "new", c.New, "dropped", c.Dropped)
			}
			fmt.Fprintf(a.out, "  %s  %s  %s\n", style.Render(fmt.Sprintf("%-9s", c.Kind)), st.Text.Render(c.Ref), st.Muted.Render(what))
		}
	}
	if changed == 0 {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("diff.none")))
		return
	}
	total := i18n.T("diff.total", "repos", changed, "new", added)
	if removed > 0 {
		total += ", " + i18n.T("diff.removed", "count", removed)
	}
	fmt.Fprintln(a.out, st.Text.Render(total))
}
