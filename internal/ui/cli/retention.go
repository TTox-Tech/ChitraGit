package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) retentionCmd() *cobra.Command {
	var apply, all bool
	cmd := &cobra.Command{
		Use:     "retention",
		Aliases: []string{"gc"},
		Short:   "Remove old snapshots the [retention] policy no longer keeps",
		Long: "Plans which snapshots to remove under the [retention] settings, and with --apply\n" +
			"removes them and their pins under refs/chitragit/snap/. Without --apply nothing\n" +
			"is changed.\n\n" +
			"Always kept: the newest snapshot of every repo, every named snapshot, and any\n" +
			"snapshot that holds a commit or tag nothing else reaches, such as history a\n" +
			"force-push removed upstream. Retention never makes a commit unreachable, so git\n" +
			"gc can only drop objects that a branch, a tag or a kept snapshot still covers.",
		Example: "  chitragit retention            # show what would be removed\n" +
			"  chitragit retention --apply    # remove it",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "retention", apply)
			if err != nil {
				return err
			}
			defer s.close()
			git := s.git
			if git == nil {
				if git, _, err = engine.Git(ctx, 0); err != nil {
					return err
				}
			}
			rc := s.cfg.Retention
			p := engine.RetentionPolicy{KeepLast: rc.KeepLast, KeepDaily: rc.KeepDaily, KeepWeekly: rc.KeepWeekly}
			rep, err := engine.Retention(ctx, s.arc.Root, git, s.st, p, apply)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if err := a.printJSON(struct {
					Schema string `json:"schema"`
					*engine.RetentionReport
				}{jsonSchema, rep}); err != nil {
					return err
				}
			} else {
				a.printRetention(rep, all)
			}
			if len(rep.Errors) > 0 {
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "remove the snapshots and pins (without it, only show the plan)")
	cmd.Flags().BoolVar(&all, "all", false, "also list the snapshots that are kept, with why")
	return cmd
}

func (a *app) printRetention(rep *engine.RetentionReport, all bool) {
	for _, it := range rep.Items {
		if it.Keep && !all {
			continue
		}
		mark, why := a.styles.Fail.Render(fmt.Sprintf("%-6s", i18n.T("retention.remove"))), ""
		if it.Keep {
			mark, why = a.styles.OK.Render(fmt.Sprintf("%-6s", i18n.T("retention.keep"))), it.Reason
		}
		fmt.Fprintf(a.out, "%s %s  %s  %s  %s\n", mark, a.styles.Label.Render(fmt.Sprintf("%6d", it.Snapshot)),
			a.styles.Text.Render(localTime(it.At)), a.styles.Text.Render(it.Path), a.styles.Muted.Render(why))
	}
	for _, e := range rep.Errors {
		a.warn(e)
	}
	key := "retention.plan"
	if rep.Applied {
		key = "retention.done"
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, a.styles.Text.Render(i18n.T(key, "removed", rep.Removed, "kept", rep.Kept, "unpinned", rep.Unpinned)))
}
