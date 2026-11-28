package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
			"`chitragit history <repo>` for IDs). A snapshot can be a name from `tag`: with no\n" +
			"repo, a name compares every repo it was given to. Every changed branch, remote-tracking branch\n" +
			"and tag is listed as added, deleted, forward, back or rewritten, with the commits\n" +
			"it gained and dropped. Commits no ref reaches any more are still kept by the\n" +
			"snapshots, so `chitragit restore` can bring them back. Changes nothing.",
		Args: argsN(0, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				switch {
				case err != nil:
				case id <= 0:
					return &usageError{fmt.Errorf("%q is not a snapshot ID", arg)}
				case run != 0:
					return &usageError{fmt.Errorf("--run compares a sync run; leave out the snapshots")}
				}
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
			repo, snaps, err := splitDiffArgs(ctx, s, args)
			if err != nil {
				return err
			}
			if run != 0 && len(snaps) > 0 {
				return &usageError{fmt.Errorf("--run compares a sync run; leave out the snapshots")}
			}
			if len(snaps) > 0 {
				diffs, errs, err = a.diffSnapshots(ctx, s, snaps, repo)
				if err != nil {
					return err
				}
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
				if repo != nil {
					var only []engine.RepoDiff
					for _, d := range diffs {
						if d.RepoID == repo.ID {
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

// splitDiffArgs sorts diff's arguments into the repo, if one is named, and
// the snapshots, as IDs or names from `tag`. An argument that names a repo
// is the repo; one that cannot be a snapshot name must be a repo.
func splitDiffArgs(ctx context.Context, s *session, args []string) (*core.Repo, []string, error) {
	var repo *core.Repo
	var snaps []string
	for _, arg := range args {
		if _, err := strconv.ParseInt(arg, 10, 64); err == nil {
			snaps = append(snaps, arg)
			continue
		}
		r, err := s.st.FindRepo(ctx, arg)
		switch {
		case err == nil && repo == nil:
			repo = &r
			continue
		case err == nil:
			return nil, nil, &usageError{fmt.Errorf("give at most one repo, got %s and %s", repo.Path, r.Path)}
		case engine.CheckSnapshotName(arg) != nil:
			return nil, nil, err
		}
		snaps = append(snaps, arg)
	}
	if len(snaps) > 2 {
		return nil, nil, &usageError{fmt.Errorf("give at most two snapshots, got %d", len(snaps))}
	}
	return repo, snaps, nil
}

// diffSnapshots compares one snapshot with the refs now, or two snapshots.
// With a repo, or an ID that says which repo, it compares that repo's
// snapshots. With only names it compares every repo the names were given
// to, so `diff pre-move` shows what changed everywhere since that tag.
func (a *app) diffSnapshots(ctx context.Context, s *session, snaps []string, repo *core.Repo) ([]engine.RepoDiff, []string, error) {
	if repo == nil {
		for _, arg := range snaps {
			id, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				continue
			}
			sn, err := s.st.Snapshot(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			r, err := s.st.FindRepo(ctx, sn.RepoID)
			if err != nil {
				return nil, nil, err
			}
			repo = &r
			break
		}
	}
	if repo == nil {
		return a.diffNamed(ctx, s, snaps)
	}
	ids := make([]int64, 2)
	for i, arg := range snaps {
		id, err := engine.SnapshotID(ctx, s.st, *repo, arg)
		if err != nil {
			return nil, nil, err
		}
		ids[i] = id
	}
	d, err := engine.DiffSnapshots(ctx, s.arc.Root, *repo, ids[0], ids[1], s.git, s.st)
	if err != nil {
		return nil, nil, err
	}
	return []engine.RepoDiff{*d}, nil, nil
}

// diffNamed compares, in every repo that has the names, the snapshot the
// first name marks with the second's, or with the refs now. A repo that
// fails is reported and the rest carry on.
func (a *app) diffNamed(ctx context.Context, s *session, names []string) ([]engine.RepoDiff, []string, error) {
	sets := make([]map[string]int64, len(names))
	for i, name := range names {
		named, err := s.st.NamedSnapshots(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		if len(named) == 0 {
			return nil, nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no repo or snapshot is named %q", name),
				Hint: "list the names with `chitragit tag -l`"}
		}
		sets[i] = named
	}
	var repos []core.Repo
	for id := range sets[0] {
		if _, ok := sets[len(sets)-1][id]; !ok {
			continue
		}
		r, err := s.st.FindRepo(ctx, id)
		if err != nil {
			continue // removed from the archive since it was named
		}
		repos = append(repos, r)
	}
	if len(repos) == 0 {
		return nil, nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no repo has both %q and %q", names[0], names[1]),
			Hint: "list the names with `chitragit tag -l`"}
	}
	slices.SortFunc(repos, func(x, y core.Repo) int { return strings.Compare(x.Path, y.Path) })
	var diffs []engine.RepoDiff
	var errs []string
	for _, r := range repos {
		to := int64(0)
		if len(names) == 2 {
			to = sets[1][r.ID]
		}
		d, err := engine.DiffSnapshots(ctx, s.arc.Root, r, sets[0][r.ID], to, s.git, s.st)
		if err != nil {
			errs = append(errs, r.Path+": "+core.Message(err))
			continue
		}
		diffs = append(diffs, *d)
	}
	return diffs, errs, nil
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
