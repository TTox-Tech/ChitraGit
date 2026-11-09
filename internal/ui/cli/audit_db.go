package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/version"
)

// osvBaseURL serves the OSV exports; tests point it at a local server.
var osvBaseURL = engine.OSVBaseURL

// vulnDBStaleAfter is when `audit` starts suggesting `audit db update`.
const vulnDBStaleAfter = 7 * 24 * time.Hour

// vulnDBDir is where the offline vulnerability database lives: the
// per-user cache, shared by every archive on the machine.
func vulnDBDir() (string, error) {
	d, err := platform.CacheDir()
	if err != nil {
		return "", &core.Error{Category: core.CatEnvironment, Message: "cannot find the cache folder", Err: err}
	}
	return filepath.Join(d, "osv"), nil
}

func openVulnDB() (*engine.VulnDB, error) {
	dir, err := vulnDBDir()
	if err != nil {
		return nil, err
	}
	db, err := engine.OpenVulnDB(dir)
	if err != nil {
		return nil, &core.Error{Category: core.CatEnvironment, Message: "cannot open the vulnerability database", Err: err}
	}
	return db, nil
}

func (a *app) auditDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage the offline vulnerability database for the deps check",
		Long: "The deps check matches the versions pinned in each repo's lockfiles against a local\n" +
			"copy of the OSV database (osv.dev). `audit db update` downloads it, about 300 MB\n" +
			"in total, into ChitraGit's cache folder. The download is the same whatever your\n" +
			"repos hold, so it tells nobody what you depend on; matching happens on this machine.",
	}
	cmd.AddCommand(a.auditDBUpdateCmd(), a.auditDBStatusCmd())
	return cmd
}

func (a *app) auditDBUpdateCmd() *cobra.Command {
	var ecos string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Download or refresh the vulnerability database",
		Long: "Downloads the OSV export of every ecosystem the deps check reads (" + strings.Join(engine.Ecosystems, ", ") + ")\n" +
			"and indexes it locally. Each ecosystem is replaced in one step only after its download\n" +
			"is complete and its checksum matches, so a failed or interrupted update keeps the\n" +
			"previous data. Run it again any time; unchanged exports are not re-indexed.",
		Example: "  chitragit audit db update\n  chitragit audit db update --ecosystems npm,PyPI",
		RunE: func(cmd *cobra.Command, args []string) error {
			want := splitList(ecos)
			for _, e := range want {
				if !slices.Contains(engine.Ecosystems, e) {
					return &usageError{fmt.Errorf("unknown ecosystem %q; choose from: %s", e, strings.Join(engine.Ecosystems, ", "))}
				}
			}
			db, err := openVulnDB()
			if err != nil {
				return err
			}
			defer db.Close()
			results, err := a.updateVulnDB(cmd.Context(), db, want)
			if a.jsonOut {
				if jerr := a.printJSON(map[string]any{"schema": jsonSchema, "kind": "vulndb_update", "path": db.Path(), "ecosystems": results}); jerr != nil {
					return jerr
				}
			} else {
				for _, r := range results {
					switch {
					case r.Error != "":
						a.warn(r.Ecosystem + ": " + r.Error + " (" + i18n.T("audit.db_kept") + ")")
					case r.Unchanged:
						fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(fmt.Sprintf("%-10s ", r.Ecosystem)+i18n.T("audit.db_up_to_date", "count", r.Advisories)))
					default:
						fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(fmt.Sprintf("%-10s ", r.Ecosystem)+i18n.T("audit.db_updated", "count", r.Advisories, "size", humanBytes(r.Bytes))))
					}
				}
			}
			if err != nil {
				return &core.Error{Category: core.CatNetwork, Message: core.Message(err),
					Hint: "check the network connection; the deps check keeps using the previous data", Err: err}
			}
			for _, r := range results {
				if r.Error != "" {
					return &silentError{code: core.ExitPartial}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ecos, "ecosystems", "", "only these ecosystems, comma-separated (default: all)")
	return cmd
}

// updateVulnDB downloads the database, showing progress on a terminal.
func (a *app) updateVulnDB(ctx context.Context, db *engine.VulnDB, ecos []string) ([]engine.VulnDBUpdate, error) {
	showProgress := !a.jsonOut && !a.headless && !a.accessible && term.IsTerminal(int(os.Stderr.Fd()))
	var progress engine.VulnDBProgress
	if showProgress {
		progress = func(eco, stage string, done, total int64) {
			msg := i18n.T("audit.db_indexing", "ecosystem", eco)
			if stage == "download" {
				msg = i18n.T("audit.db_downloading", "ecosystem", eco, "done", humanBytes(done))
				if total > 0 {
					msg = i18n.T("audit.db_downloading_of", "ecosystem", eco, "done", humanBytes(done), "total", humanBytes(total))
				}
			}
			fmt.Fprintf(a.errOut, "\r\033[K%s", a.styles.Muted.Render(msg))
		}
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	res, err := db.Update(ctx, client, osvBaseURL, "chitragit/"+version.Version, ecos, progress)
	if showProgress {
		fmt.Fprint(a.errOut, "\r\033[K")
	}
	return res, err
}

func (a *app) auditDBStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what the vulnerability database holds and how old it is",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openVulnDB()
			if err != nil {
				return err
			}
			defer db.Close()
			st, err := db.Status(cmd.Context())
			if err != nil {
				return &core.Error{Category: core.CatEnvironment, Message: i18n.T("audit.db_unreadable"), Err: err}
			}
			if st == nil {
				st = []engine.EcosystemStatus{}
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "kind": "vulndb_status", "path": db.Path(), "ecosystems": st})
			}
			if len(st) == 0 {
				fmt.Fprintln(a.out, a.styles.Warn.Render(a.icon(iconWarn))+a.styles.Text.Render(i18n.T("audit.db_missing")))
				return nil
			}
			fmt.Fprintln(a.out, a.styles.Label.Render(db.Path()))
			for _, s := range st {
				fmt.Fprintln(a.out, "  "+a.styles.Text.Render(fmt.Sprintf("%-10s %7d ", s.Ecosystem, s.Advisories)+i18n.T("audit.db_status", "age", ageText(s.UpdatedAt))))
			}
			var missing []string
			for _, e := range engine.Ecosystems {
				if !slices.ContainsFunc(st, func(s engine.EcosystemStatus) bool { return s.Ecosystem == e }) {
					missing = append(missing, e)
				}
			}
			if len(missing) > 0 {
				fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.db_not_downloaded", "list", strings.Join(missing, ", "))))
			}
			return nil
		},
	}
}

// vulnDBForAudit opens the database for an audit that includes the deps
// check. It returns nil (and a note to show) when the check should be
// skipped; explicit means the user asked for deps with --checks, which
// turns a missing database into an error instead.
func (a *app) vulnDBForAudit(ctx context.Context, mode string, explicit bool) (*engine.VulnDB, string, error) {
	if mode == "off" {
		if explicit {
			return nil, "", &usageError{fmt.Errorf("the deps check is turned off (audit.osv_db = \"off\")")}
		}
		return nil, "", nil
	}
	db, err := openVulnDB()
	if err != nil {
		return nil, "", err
	}
	st, err := db.Status(ctx)
	if err != nil {
		db.Close()
		return nil, "", &core.Error{Category: core.CatEnvironment, Message: i18n.T("audit.db_unreadable"), Err: err}
	}
	if len(st) == 0 {
		db.Close()
		if explicit {
			return nil, "", &core.Error{Category: core.CatConfig, Message: i18n.T("audit.db_needed"),
				Hint: i18n.T("audit.db_needed_hint")}
		}
		return nil, i18n.T("audit.db_skipped"), nil
	}
	note := ""
	oldest := st[0].UpdatedAt
	for _, s := range st {
		oldest = min(oldest, s.UpdatedAt)
	}
	if t, err := time.Parse(time.RFC3339, oldest); err == nil && time.Since(t) > vulnDBStaleAfter {
		note = i18n.T("audit.db_stale", "age", ageText(oldest))
	}
	return db, note, nil
}

func ageText(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return i18n.T("audit.age_now")
	case d < 48*time.Hour:
		return i18n.T("audit.age_hours", "count", int(d.Hours()))
	}
	return i18n.T("audit.age_days", "count", int(d.Hours()/24))
}
