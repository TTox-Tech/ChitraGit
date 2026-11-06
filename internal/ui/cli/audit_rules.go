package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// rulesUpdateEvery is how often audit.rules_update = "weekly" looks.
const rulesUpdateEvery = 7 * 24 * time.Hour

func rulesDir() (string, error) {
	d, err := platform.CacheDir()
	if err != nil {
		return "", &core.Error{Category: core.CatEnvironment, Message: "cannot find the user cache folder", Err: err}
	}
	return audit.RulesDir(d), nil
}

// auditSetup loads the rules pack (refreshing it first when a weekly
// check is due) and finds the external tools the config asks for.
func (a *app) auditSetup(ctx context.Context, ac config.AuditConfig, checks []string) (*audit.RulesPack, *audit.Tool, *audit.Tool, error) {
	wants := func(c string) bool { return len(checks) == 0 || oneOfStr(c, checks...) }
	if !wants(audit.CheckActions) && !wants(audit.CheckAgents) {
		return nil, nil, nil, nil
	}
	dir, err := rulesDir()
	if err != nil {
		return nil, nil, nil, err
	}
	if ac.RulesUpdate == "weekly" && audit.RulesUpdateDue(dir, rulesUpdateEvery) {
		uctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := audit.UpdateRules(uctx, dir, "", &http.Client{Timeout: 20 * time.Second})
		cancel()
		audit.MarkRulesChecked(dir)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, nil, nil, ctx.Err()
		case err != nil:
			a.warn("Could not refresh the KodeKavach rules pack, using the one already here: " + err.Error())
		case res.Updated && !a.jsonOut:
			fmt.Fprintln(a.errOut, a.styles.Muted.Render(fmt.Sprintf("KodeKavach rules updated: %s → %s", res.Previous, res.Current)))
		}
	}
	pack, warn := audit.LoadRules(dir)
	if pack == nil {
		return nil, nil, nil, warn
	}
	if warn != nil {
		a.warn(warn.Error())
	}
	var zz, ss *audit.Tool
	if wants(audit.CheckActions) {
		if zz, err = audit.FindTool(ctx, audit.ToolZizmor, ac.Zizmor); err != nil {
			return nil, nil, nil, &core.Error{Category: core.CatConfig, Message: err.Error(),
				Hint: "install zizmor (https://docs.zizmor.sh) or set audit.zizmor = \"off\""}
		}
	}
	if wants(audit.CheckAgents) {
		if ss, err = audit.FindTool(ctx, audit.ToolSkillSpector, ac.SkillSpector); err != nil {
			return nil, nil, nil, &core.Error{Category: core.CatConfig, Message: err.Error(),
				Hint: "install SkillSpector (https://github.com/NVIDIA/skillspector) or set audit.skillspector = \"off\""}
		}
	}
	return pack, zz, ss, nil
}

func (a *app) auditRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Show the rules pack the actions and agents checks use",
		Long: "The actions and agents checks read their agent file names, content patterns and\n" +
			"rule settings from a rules pack. One is built into ChitraGit; `audit rules update`\n" +
			"fetches a newer one from ChitraGit's own GitHub Releases. A downloaded pack is used\n" +
			"only if its Ed25519 signature checks out and it is newer than the built-in one.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := rulesDir()
			if err != nil {
				return err
			}
			pack, warn := audit.LoadRules(dir)
			if pack == nil {
				return warn
			}
			last := audit.RulesLastChecked(dir)
			lastStr := ""
			if !last.IsZero() {
				lastStr = last.UTC().Format(time.RFC3339)
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{
					"schema": jsonSchema, "kind": "rules", "version": pack.Version, "source": pack.Source,
					"pack_schema": pack.Schema, "agent_file_patterns": pack.FileSpecCount(),
					"content_rules": pack.ContentRuleCount(), "last_checked": lastStr,
					"signed_updates": audit.RulesPublicKey != "",
				})
			}
			if warn != nil {
				a.warn(warn.Error())
			}
			fmt.Fprintln(a.out, a.styles.Text.Render(fmt.Sprintf("KodeKavach rules %s (%s)", pack.Version, pack.Source)))
			fmt.Fprintln(a.out, a.styles.Muted.Render(fmt.Sprintf("  %d agent file patterns, %d content rules", pack.FileSpecCount(), pack.ContentRuleCount())))
			switch {
			case audit.RulesPublicKey == "":
				fmt.Fprintln(a.out, a.styles.Muted.Render("  This build has no signing key, so it uses only its built-in rules."))
			case last.IsZero():
				fmt.Fprintln(a.out, a.styles.Muted.Render("  Never checked for updates; run `chitragit audit rules update`."))
			default:
				fmt.Fprintln(a.out, a.styles.Muted.Render("  Last checked for updates "+last.Local().Format("2006-01-02 15:04")))
			}
			return nil
		},
	}
	cmd.AddCommand(a.auditRulesUpdateCmd())
	return cmd
}

func (a *app) auditRulesUpdateCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Download a newer signed rules pack",
		Long: "Downloads the latest rules pack from ChitraGit's GitHub Releases and installs it\n" +
			"if its signature is valid and it is newer than the pack in use. It sends no data\n" +
			"about your archive. If anything fails, the current pack stays in use.\n\n" +
			"For a machine without internet, download kodekavach-rules.json and\n" +
			"kodekavach-rules.json.sig elsewhere and use --from with the folder that has them.",
		Example: "  chitragit audit rules update\n  chitragit audit rules update --from ./usb/kodekavach",
		Args:    argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := rulesDir()
			if err != nil {
				return err
			}
			var res *audit.RulesUpdate
			if from != "" {
				pack, err1 := os.ReadFile(filepath.Join(from, audit.RulesAsset))
				sig, err2 := os.ReadFile(filepath.Join(from, audit.RulesSigAsset))
				if err := errors.Join(err1, err2); err != nil {
					return &core.Error{Category: core.CatConfig, Message: "cannot read the rules pack files in " + from, Err: err,
						Hint: "the folder needs " + audit.RulesAsset + " and " + audit.RulesSigAsset}
				}
				res, err = audit.InstallRules(dir, pack, sig)
			} else {
				res, err = audit.UpdateRules(cmd.Context(), dir, "", nil)
			}
			if err != nil {
				if errors.Is(err, audit.ErrNoRulesKey) {
					return &core.Error{Category: core.CatEnvironment, Message: err.Error(),
						Hint: "install a release build of ChitraGit to get signed rule updates"}
				}
				cat := core.CatNetwork
				if from != "" {
					cat = core.CatConfig
				}
				if errors.Is(err, audit.ErrBadSignature) {
					cat = core.CatCorrupt
				}
				return &core.Error{Category: cat, Message: err.Error()}
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "kind": "rules_update", "result": res})
			}
			msg := "KodeKavach rules are up to date (" + res.Current + ")."
			if res.Updated {
				msg = fmt.Sprintf("KodeKavach rules updated: %s → %s.", res.Previous, res.Current)
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(msg))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "install from this folder instead of downloading")
	return cmd
}
