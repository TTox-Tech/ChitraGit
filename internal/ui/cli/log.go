package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) logCmd() *cobra.Command {
	var sinceFlag, repo string
	var limit int
	var verify bool
	cmd := &cobra.Command{
		Use:   "log [repo]",
		Short: "Show the operation ledger: every change made to the archive",
		Long: "Lists the archive's event log, newest first: syncs, scans, tags, rescues,\n" +
			"bundles, resolved conflicts and every other change ChitraGit made. Each entry\n" +
			"carries a hash of itself and the entry before it, so editing or deleting an\n" +
			"entry breaks the chain. --verify recomputes the chain and fails if it is broken.\n" +
			"As with `git log <path>`, `log <repo>` shows what happened to that repo: the\n" +
			"same as `history <repo>`.",
		Args: argsN(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				h := a.historyCmd()
				if err := h.Flags().Set("limit", strconv.Itoa(limit)); err != nil {
					return err
				}
				return h.RunE(cmd, args)
			}
			ctx := cmd.Context()
			f := engine.LedgerFilter{Limit: limit}
			if sinceFlag != "" {
				d, err := config.ParseDuration(sinceFlag)
				if err != nil || d <= 0 {
					return &usageError{fmt.Errorf("invalid --since %q; use e.g. 24h or 7d", sinceFlag)}
				}
				f.Since = time.Now().Add(-d)
			}
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			if repo != "" {
				r, err := s.st.FindRepo(ctx, repo)
				if err != nil {
					return err
				}
				f.RepoID = r.ID
			}
			entries, err := s.st.Ledger(ctx, f)
			if err != nil {
				return err
			}
			var check *engine.LedgerCheck
			if verify {
				c, err := s.st.VerifyLedger(ctx)
				if err != nil {
					return err
				}
				check = &c
			}
			if a.jsonOut {
				if entries == nil {
					entries = []engine.LedgerEntry{}
				}
				out := map[string]any{"schema": jsonSchema, "events": entries}
				if check != nil {
					out["verify"] = check
				}
				if err := a.printJSON(out); err != nil {
					return err
				}
			} else {
				a.printLedger(entries, check)
			}
			if check != nil && !check.OK {
				return &core.Error{Category: core.CatCorrupt,
					Message: i18n.T("log.broken", "seq", check.Seq, "reason", check.Reason),
					Hint:    "the archive database was changed outside ChitraGit; restore it from a backup (archive.db.bak-v*) or keep it and note the break"}
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&sinceFlag, "since", "", "only entries newer than this, e.g. 24h or 7d")
	fl.StringVar(&repo, "repo", "", "only entries about this repo")
	fl.IntVarP(&limit, "limit", "n", 200, "show at most this many entries")
	fl.IntVar(&limit, "max-count", 200, "same as --limit")
	fl.BoolVar(&verify, "verify", false, "recompute the hash chain and fail if it is broken")
	return cmd
}

func (a *app) printLedger(entries []engine.LedgerEntry, check *engine.LedgerCheck) {
	st := a.styles
	if len(entries) == 0 {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("log.empty")))
	}
	for _, e := range entries {
		at := e.At
		if t, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
			at = t.Local().Format("2006-01-02 15:04:05")
		}
		short := e.Hash
		if len(short) > 10 {
			short = short[:10]
		}
		fmt.Fprintf(a.out, "%s  %s  %s  %s  %s\n", st.Muted.Render(fmt.Sprintf("%6d", e.Seq)), st.Text.Render(at),
			st.Muted.Render(short), st.Label.Render(fmt.Sprintf("%-18s", e.Type)), st.Text.Render(strings.TrimSpace(e.Path+" "+string(e.Payload))))
	}
	if check != nil && check.OK {
		fmt.Fprintln(a.out, st.OK.Render(i18n.T("log.ok", "events", check.Events)))
	}
}
