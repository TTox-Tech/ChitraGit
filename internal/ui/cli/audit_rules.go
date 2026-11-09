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

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// rulesUpdateEvery is how often audit.rules_update = "weekly" looks.
const rulesUpdateEvery = 7 * 24 * time.Hour

func rulesDir() (string, error) {
	d, err := platform.CacheDir()
	if err != nil {
		return "", &core.Error{Category: core.CatEnvironment, Message: "cannot find the user cache folder", Err: err}
	}
	return engine.RulesDir(d), nil
}

// auditSetup loads the rules pack (refreshing it first when a weekly
// check is due) and finds the external tools the config asks for.
func (a *app) auditSetup(ctx context.Context, ac config.AuditConfig, checks []string) (*engine.RulesPack, *engine.AuditTool, *engine.AuditTool, error) {
	wants := func(c string) bool { return len(checks) == 0 || oneOfStr(c, checks...) }
	if !wants(engine.CheckActions) && !wants(engine.CheckAgents) {
		return nil, nil, nil, nil
	}
	dir, err := rulesDir()
	if err != nil {
		return nil, nil, nil, err
	}
	if ac.RulesUpdate == "weekly" && engine.RulesUpdateDue(dir, rulesUpdateEvery) {
		uctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := engine.UpdateRules(uctx, dir, "", &http.Client{Timeout: 20 * time.Second})
		cancel()
		engine.MarkRulesChecked(dir)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, nil, nil, ctx.Err()
		case err != nil:
			a.warn(i18n.T("audit.rules_refresh_failed", "error", err.Error()))
		case res.Updated && !a.jsonOut:
			fmt.Fprintln(a.errOut, a.styles.Muted.Render(i18n.T("audit.rules_updated", "from", res.Previous, "to", res.Current)))
		}
	}
	pack, warn := engine.LoadRules(dir)
	if pack == nil {
		return nil, nil, nil, warn
	}
	if warn != nil {
		a.warn(warn.Error())
	}
	var zz, ss *engine.AuditTool
	if wants(engine.CheckActions) {
		if zz, err = engine.FindAuditTool(ctx, engine.ToolZizmor, ac.Zizmor); err != nil {
			return nil, nil, nil, &core.Error{Category: core.CatConfig, Message: err.Error(),
				Hint: "install zizmor (https://docs.zizmor.sh) or set audit.zizmor = \"off\""}
		}
	}
	if wants(engine.CheckAgents) {
		if ss, err = engine.FindAuditTool(ctx, engine.ToolSkillSpector, ac.SkillSpector); err != nil {
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
			pack, warn := engine.LoadRules(dir)
			if pack == nil {
				return warn
			}
			last := engine.RulesLastChecked(dir)
			lastStr := ""
			if !last.IsZero() {
				lastStr = last.UTC().Format(time.RFC3339)
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{
					"schema": jsonSchema, "kind": "rules", "version": pack.Version, "source": pack.Source,
					"pack_schema": pack.Schema, "agent_file_patterns": pack.FileSpecCount(),
					"content_rules": pack.ContentRuleCount(), "last_checked": lastStr,
					"signed_updates": engine.SignedRulesUpdates(),
				})
			}
			if warn != nil {
				a.warn(warn.Error())
			}
			fmt.Fprintln(a.out, a.styles.Text.Render(i18n.T("audit.rules_status", "version", pack.Version, "source", pack.Source)))
			fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.rules_counts", "files", pack.FileSpecCount(), "rules", pack.ContentRuleCount())))
			switch {
			case !engine.SignedRulesUpdates():
				fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.rules_unsigned")))
			case last.IsZero():
				fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.rules_never_checked")))
			default:
				fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.rules_last_checked", "time", last.Local().Format("2006-01-02 15:04"))))
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
			var res *engine.RulesUpdate
			if from != "" {
				pack, err1 := os.ReadFile(filepath.Join(from, engine.RulesAsset))
				sig, err2 := os.ReadFile(filepath.Join(from, engine.RulesSigAsset))
				if err := errors.Join(err1, err2); err != nil {
					return &core.Error{Category: core.CatConfig, Message: "cannot read the rules pack files in " + from, Err: err,
						Hint: "the folder needs " + engine.RulesAsset + " and " + engine.RulesSigAsset}
				}
				res, err = engine.InstallRules(dir, pack, sig)
			} else {
				res, err = engine.UpdateRules(cmd.Context(), dir, "", nil)
			}
			if err != nil {
				if errors.Is(err, engine.ErrNoRulesKey) {
					return &core.Error{Category: core.CatEnvironment, Message: err.Error(),
						Hint: "install a release build of ChitraGit to get signed rule updates"}
				}
				cat := core.CatNetwork
				if from != "" {
					cat = core.CatConfig
				}
				if errors.Is(err, engine.ErrBadSignature) {
					cat = core.CatCorrupt
				}
				return &core.Error{Category: cat, Message: err.Error()}
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "kind": "rules_update", "result": res})
			}
			msg := i18n.T("audit.rules_up_to_date", "version", res.Current)
			if res.Updated {
				msg = i18n.T("audit.rules_updated", "from", res.Previous, "to", res.Current) + "."
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(msg))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "install from this folder instead of downloading")
	return cmd
}
