package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) historyCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "history <repo>",
		Short: "Show everything that happened to one repo, newest first",
		Long: "Every ref a sync created, moved, rewrote or saw deleted upstream, every\n" +
			"snapshot, and events such as a rescue or a rename, in time order. Snapshot\n" +
			"IDs can be passed to `chitragit snapshot show`. Reads only the archive\n" +
			"database, so it works offline.",
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
			list, err := s.st.Timeline(ctx, r.ID, limit)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if list == nil {
					list = []engine.TimelineEntry{}
				}
				return a.printJSON(map[string]any{"schema": jsonSchema, "repo": r.Path, "history": list})
			}
			if len(list) == 0 {
				fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("history.empty")))
				return nil
			}
			fmt.Fprintln(a.out, a.styles.Title.Render(r.Path))
			for _, e := range list {
				a.printTimelineEntry(e)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 200, "show at most this many entries")
	return cmd
}

func (a *app) printTimelineEntry(e engine.TimelineEntry) {
	st := a.styles
	short := func(sha string) string { return sha[:min(12, len(sha))] }
	style, what := st.Muted, e.Message
	switch e.Kind {
	case engine.TimelineSnapshot:
		what = i18n.T("history.snapshot", "id", e.Snapshot, "kind", e.SnapshotKind)
	case "create":
		style, what = st.OK, e.Ref+"  "+short(e.NewSHA)
	case "update":
		style, what = st.OK, e.Ref+"  "+short(e.OldSHA)+" → "+short(e.NewSHA)
		if e.Commits > 0 {
			what += "  " + i18n.T("history.commits", "count", e.Commits)
		}
	case "rewrite":
		style, what = st.Warn, e.Ref+"  "+short(e.OldSHA)+" → "+short(e.NewSHA)+"  "+i18n.T("history.kept")
	case "delete":
		style, what = st.Warn, e.Ref+"  "+short(e.OldSHA)+"  "+i18n.T("history.deleted")
	case "rescued", "renamed":
		style = st.Accent
	}
	fmt.Fprintf(a.out, "  %s  %s  %s\n", st.Text.Render(localTime(e.At)),
		style.Render(fmt.Sprintf("%-8s", e.Kind)), st.Text.Render(what))
}
