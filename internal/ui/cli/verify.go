package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) verifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "verify",
		Aliases: []string{"fsck"},
		Short:   "Check that the archive is whole: the ledger's hash chain and more",
		Long: "Runs read-only checks on the archive and reports what is wrong, without repairing\n" +
			"anything:\n\n" +
			"  ledger   recompute the event log's hash chain; an entry edited or removed\n" +
			"           outside ChitraGit breaks it\n" +
			"  pinned   every object a snapshot points at is still in its repo, and pinned\n" +
			"           so git gc cannot remove it\n" +
			"  bundles  every bundle backup passes `git bundle verify`, and every bundle\n" +
			"           a rescue record points at is still there\n\n" +
			"Exit 0 when every check passes, 1 when one fails.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			git, _, err := engine.Git(ctx, 0)
			if err != nil {
				return err
			}
			rep, err := engine.Verify(ctx, s.arc.Root, git, s.st)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if err := a.printJSON(struct {
					Schema string `json:"schema"`
					*engine.VerifyReport
				}{jsonSchema, rep}); err != nil {
					return err
				}
			} else {
				a.printVerify(rep)
			}
			if !rep.OK {
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
}

func (a *app) printVerify(rep *engine.VerifyReport) {
	for _, c := range rep.Checks {
		icon, style := a.icon(iconOK), a.styles.OK
		if !c.OK {
			icon, style = a.icon(iconFail), a.styles.Fail
		}
		fmt.Fprintln(a.out, style.Render(icon)+a.styles.Label.Render(fmt.Sprintf("%-8s", c.Name))+" "+a.styles.Text.Render(c.Summary))
		for _, p := range c.Problems {
			fmt.Fprintln(a.out, "    "+a.styles.Muted.Render(p))
		}
	}
	key := "verify.ok"
	if !rep.OK {
		key = "verify.failed"
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, a.styles.Text.Render(i18n.T(key)))
}
