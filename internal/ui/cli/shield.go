package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) shieldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shield",
		Short: "KodeKavach Shield: judge repos before their files reach your disk",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check <url | provider:owner/repo>",
		Short: "Judge a repo from its provider's API, without cloning it",
		Long: "Reads the file list and the files that matter (workflows, agent files, editor tasks,\n" +
			".gitmodules, programs) of the repo's default branch through the GitHub, GitLab or\n" +
			"Gitea API and runs Shield's checks on them. Nothing is cloned or written.\n\n" +
			"Exit 6 means a clone would be blocked. When the API cannot answer in full (a host\n" +
			"with no API, a tree too large for it, a rate limit), the exit is 1: a clone is then\n" +
			"judged from its own objects instead. `clone` and `follow` run this check first.",
		Args: argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			opts, err := shieldOptions()
			if err != nil {
				return err
			}
			rep, err := engine.PreCheck(ctx, args[0], registry(ctx, cfg), opts)
			var skip *engine.PreCheckSkipped
			if errors.As(err, &skip) {
				if a.jsonOut {
					if jerr := a.printJSON(map[string]string{"schema": jsonSchema, "verdict": "unknown", "reason": skip.Reason}); jerr != nil {
						return jerr
					}
				} else {
					a.warn(i18n.T("shield.skipped", "reason", skip.Reason))
				}
				return &silentError{code: core.ExitPartial}
			}
			if err != nil {
				return err
			}
			if a.jsonOut {
				if err := a.printJSON(struct {
					Schema string `json:"schema"`
					*engine.ShieldReport
				}{jsonSchema, rep}); err != nil {
					return err
				}
			} else {
				a.printShieldReport(rep)
			}
			if rep.Verdict == engine.VerdictBlock {
				return &silentError{code: core.ExitBlocked}
			}
			return nil
		},
	})
	return cmd
}

func (a *app) printShieldReport(rep *engine.ShieldReport) {
	for _, f := range rep.Findings {
		a.printFinding(f)
	}
	line := i18n.T("shield.verdict_"+string(rep.Verdict), "repo", rep.Repo, "count", len(rep.Findings), "commit", shortSHA(rep.Commit))
	switch rep.Verdict {
	case engine.VerdictBlock:
		fmt.Fprintln(a.out, a.styles.Fail.Render(a.icon(iconFail))+a.styles.Text.Render(line))
	case engine.VerdictWarn:
		fmt.Fprintln(a.out, a.styles.Warn.Render(a.icon(iconWarn))+a.styles.Text.Render(line))
	default:
		fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(line))
	}
}
