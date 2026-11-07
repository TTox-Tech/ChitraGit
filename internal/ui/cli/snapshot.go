package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "List, show and take snapshots of your repos' refs",
		Long: "A snapshot is the full ref state of one repo at one moment: every branch,\n" +
			"remote-tracking branch and tag with its commit. Every sync takes one before and\n" +
			"one after it touches a repo, and stores nothing when the repo did not change.\n" +
			"The objects a snapshot points at are kept under refs/chitragit/snap/ so git gc\n" +
			"never removes them.",
	}
	cmd.AddCommand(a.snapshotLsCmd(), a.snapshotShowCmd(), a.snapshotTakeCmd())
	return cmd
}

func (a *app) snapshotLsCmd() *cobra.Command {
	var repo string
	var limit int
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List snapshots, newest first",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			id := ""
			if repo != "" {
				r, err := s.st.FindRepo(ctx, repo)
				if err != nil {
					return err
				}
				id = r.ID
			}
			list, err := s.st.Snapshots(ctx, id, limit)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if list == nil {
					list = []engine.Snapshot{}
				}
				return a.printJSON(map[string]any{"schema": jsonSchema, "snapshots": list})
			}
			if len(list) == 0 {
				fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("snapshot.none")))
				return nil
			}
			for _, sn := range list {
				fmt.Fprintf(a.out, "%s  %s  %s  %s  %s\n", a.styles.Label.Render(fmt.Sprintf("%6d", sn.ID)),
					a.styles.Text.Render(localTime(sn.At)), a.styles.Muted.Render(fmt.Sprintf("%-6s", sn.Kind)),
					a.styles.Text.Render(sn.Path), a.styles.Muted.Render(i18n.T("snapshot.refs", "count", sn.Count)))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "only snapshots of this repo")
	cmd.Flags().IntVar(&limit, "limit", 200, "show at most this many snapshots")
	return cmd
}

func (a *app) snapshotShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show every ref a snapshot recorded",
		Args:  argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return &usageError{fmt.Errorf("%q is not a snapshot ID", args[0])}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			sn, err := s.st.Snapshot(ctx, id)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "snapshot": sn})
			}
			fmt.Fprintf(a.out, "%s  %s  %s  %s\n", a.styles.Title.Render(fmt.Sprintf("#%d", sn.ID)), a.styles.Text.Render(sn.Path),
				a.styles.Muted.Render(sn.Kind), a.styles.Text.Render(localTime(sn.At)))
			for _, ref := range slices.Sorted(maps.Keys(sn.Refs)) {
				fmt.Fprintf(a.out, "  %s  %s\n", a.styles.Muted.Render(sn.Refs[ref][:min(12, len(sn.Refs[ref]))]), a.styles.Text.Render(ref))
			}
			return nil
		},
	}
}

func (a *app) snapshotTakeCmd() *cobra.Command {
	var only string
	cmd := &cobra.Command{
		Use:   "take [repo…]",
		Short: "Record a snapshot of repos now",
		Args:  argsN(0, 1<<20),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit snapshot take", true)
			if err != nil {
				return err
			}
			defer s.close()
			repos, err := pickRepos(ctx, s.st, args, only)
			if err != nil {
				return err
			}
			taken := engine.TakeSnapshots(ctx, s.arc.Root, repos, s.git, s.st)
			failed := 0
			for _, t := range taken {
				if t.Error != "" {
					failed++
				}
			}
			if a.jsonOut {
				if err := a.printJSON(map[string]any{"schema": jsonSchema, "snapshots": taken}); err != nil {
					return err
				}
			} else {
				for _, t := range taken {
					switch {
					case t.Error != "":
						a.warn(t.Path + ": " + t.Error)
					case t.Created:
						fmt.Fprintln(a.out, a.styles.OK.Render(i18n.T("snapshot.taken", "id", t.ID, "path", t.Path)))
					default:
						fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("snapshot.same", "id", t.ID, "path", t.Path)))
					}
				}
			}
			if failed > 0 {
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "every repo whose path matches this glob, e.g. 'github/me/*'")
	return cmd
}

// localTime shows a stored RFC 3339 time in the user's time zone.
func localTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
