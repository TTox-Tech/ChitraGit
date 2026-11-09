package cli

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/version"
)

// auditCmd is KodeKavach, the read-only security audit.
func (a *app) auditCmd() *cobra.Command {
	var (
		checks  string
		history string
		only    string
		sarif   string
		failOn  string
		workers int
	)
	cmd := &cobra.Command{
		Use:   "audit [repo…]",
		Short: "Check the archive for leaked secrets, vulnerable dependencies, risky workflows and poisoned agent files (KodeKavach)",
		Long: "KodeKavach reads every repo in the archive and reports leaked secrets such as API\n" +
			"keys, tokens and private keys, in the files and in the whole git history, so a key\n" +
			"that was committed and later deleted is still found. Bare and mirror repos work too.\n\n" +
			"Checks:  secrets  keys and tokens in all history\n" +
			"         deps     lockfiles at HEAD (go.mod, package-lock.json, yarn.lock, pnpm-lock.yaml,\n" +
			"                  Cargo.lock, poetry.lock, uv.lock, Pipfile.lock, requirements.txt,\n" +
			"                  Gemfile.lock, composer.lock, gradle.lockfile, packages.lock.json,\n" +
			"                  pubspec.lock) matched against the offline OSV database; download it\n" +
			"                  once with `chitragit audit db update`. Upgraded ones are marked fixed.\n" +
			"         actions  GitHub Actions workflows at HEAD: unpinned actions, script injection,\n" +
			"                  pull_request_target checkouts, write-all tokens and more\n" +
			"         agents   AI agent files at HEAD (agent instructions, skills, rules, MCP configs,\n" +
			"                  hook settings): hidden characters, download-and-run, prompt overrides,\n" +
			"                  exfiltration endpoints, auto-approve settings and more\n" +
			"         Problems removed from HEAD since an earlier audit are marked fixed.\n" +
			"zizmor and SkillSpector can also run as an opt-in extra (audit.zizmor, audit.skillspector,\n" +
			"off by default), offline and without the repo's own ignore settings.\n\n" +
			"The audit only reads. It never changes a repo, never runs a repo's own diff drivers\n" +
			"or hooks, and sends nothing about your repos over the network (the only downloads are\n" +
			"the OSV database, see `audit db`, and a weekly signed rules pack, see `audit rules`). A secret is never stored or printed\n" +
			"in full: findings show a redacted preview such as \"ghp_****\".\n\n" +
			"By default only commits added since the last audit are read, so a daily audit of a\n" +
			"large archive stays fast. The first audit reads all history.\n\n" +
			"History:  incremental (default)  new commits only · full  all history again · head  files at HEAD only\n" +
			"Exit code 5 means an open finding is at least as severe as --fail-on (default: high).",
		Example: "  chitragit audit\n" +
			"  chitragit audit --only 'torvalds/*' --history full\n" +
			"  chitragit audit --sarif kodekavach.sarif --fail-on critical\n" +
			"  chitragit audit --checks deps",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit audit", true)
			if err != nil {
				return err
			}
			defer s.close()
			ac := s.cfg.Audit
			if cmd.Flags().Changed("history") {
				ac.History = history
			}
			if cmd.Flags().Changed("fail-on") {
				ac.FailOn = failOn
			}
			if cmd.Flags().Changed("workers") {
				ac.Workers = workers
			}
			if cmd.Flags().Changed("checks") {
				ac.Checks = splitList(checks)
			}
			if !oneOfStr(ac.History, engine.HistoryHead, engine.HistoryIncremental, engine.HistoryFull) {
				return &usageError{fmt.Errorf("--history must be head, incremental or full, not %q", ac.History)}
			}
			min, ok := engine.ParseSeverity(ac.FailOn)
			if !ok {
				return &usageError{fmt.Errorf("--fail-on must be low, medium, high or critical, not %q", ac.FailOn)}
			}
			for _, c := range ac.Checks {
				if !oneOfStr(c, engine.AllAuditChecks...) {
					return &usageError{fmt.Errorf("unknown check %q; this version has: %s", c, strings.Join(engine.AllAuditChecks, ", "))}
				}
			}
			if only != "" {
				if _, err := path.Match(only, ""); err != nil {
					return &usageError{fmt.Errorf("invalid --only pattern %q", only)}
				}
			}
			var vdb *engine.VulnDB
			if slices.Contains(ac.Checks, engine.CheckDeps) {
				db, note, err := a.vulnDBForAudit(ctx, ac.OSVDB, cmd.Flags().Changed("checks"))
				if err != nil {
					return err
				}
				if note != "" {
					a.warn(note)
				}
				if db == nil {
					ac.Checks = slices.DeleteFunc(slices.Clone(ac.Checks), func(c string) bool { return c == engine.CheckDeps })
					if len(ac.Checks) == 0 {
						return &usageError{fmt.Errorf("no checks left to run")}
					}
				} else {
					vdb = db
					defer db.Close()
				}
			}
			var paths []string
			for _, q := range args {
				r, err := s.st.FindRepo(ctx, q)
				if err != nil {
					return err
				}
				paths = append(paths, r.Path)
			}

			pack, zizmor, skillspector, err := a.auditSetup(ctx, ac, ac.Checks)
			if err != nil {
				return err
			}

			showProgress := !a.jsonOut && !a.headless && !a.accessible && term.IsTerminal(int(os.Stderr.Fd()))
			req := engine.AuditRequest{
				Root: s.arc.Root, Repos: paths, Only: only, Checks: ac.Checks, History: ac.History,
				Workers: ac.Workers, MaxFileSize: int64(ac.MaxFileKB) * 1024, VulnDB: vdb,
				Rules: pack, Zizmor: zizmor, SkillSpector: skillspector,
			}
			if showProgress {
				req.Progress = func(done, total int, repo string) {
					fmt.Fprintf(a.errOut, "\r\033[K%s", a.styles.Muted.Render(i18n.T("audit.progress", "done", done, "total", total, "repo", repo)))
				}
			}
			rep, err := engine.Audit(ctx, req, s.git, s.st)
			if showProgress {
				fmt.Fprint(a.errOut, "\r\033[K")
			}
			if err != nil && rep == nil {
				return err
			}
			if sarif != "" && rep != nil {
				if werr := writeSARIFFile(sarif, rep.Findings); werr != nil {
					return werr
				}
			}
			if a.jsonOut {
				if jerr := a.printJSON(struct {
					Schema string `json:"schema"`
					Kind   string `json:"kind"`
					*engine.AuditReport
				}{jsonSchema, "audit", rep}); jerr != nil {
					return jerr
				}
			} else {
				a.printAudit(rep)
			}
			if err != nil {
				return err
			}
			switch {
			case engine.SeverityAtLeast(rep.Findings, min):
				return &silentError{code: core.ExitFindings}
			case len(rep.Failed) > 0:
				return &silentError{code: core.ExitPartial}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&checks, "checks", "", "comma-separated checks to run: secrets, deps, actions, agents (default from audit.checks)")
	f.StringVar(&history, "history", "", "head | incremental | full (default from audit.history)")
	f.StringVar(&only, "only", "", "audit only repos whose path matches this glob, e.g. 'torvalds/*'")
	f.StringVar(&sarif, "sarif", "", "also write the findings to this file as SARIF 2.1.0")
	f.StringVar(&failOn, "fail-on", "", "exit with code 5 if an open finding is at least this severe (default from audit.fail_on)")
	f.IntVar(&workers, "workers", 0, "repos audited in parallel (default from audit.workers)")
	cmd.AddCommand(a.auditFindingsCmd(), a.auditIgnoreCmd(), a.auditUnignoreCmd(), a.auditDBCmd(), a.auditRulesCmd())
	return cmd
}

func (a *app) auditFindingsCmd() *cobra.Command {
	var (
		only, check, sev, sarif string
		ignored, fixed          bool
	)
	cmd := &cobra.Command{
		Use:   "findings [repo…]",
		Short: "List what earlier audits found",
		Long:  "Lists stored findings, most severe first, without reading any repo again.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			flt := engine.FindingFilter{Check: check, IncludeIgnored: ignored, IncludeFixed: fixed}
			if sev != "" {
				v, ok := engine.ParseSeverity(sev)
				if !ok {
					return &usageError{fmt.Errorf("--severity must be info, low, medium, high or critical, not %q", sev)}
				}
				flt.MinSeverity = v
			}
			var repos []core.Repo
			if len(args) > 0 || only != "" {
				if repos, err = pickRepos(ctx, s.st, args, only); err != nil {
					return err
				}
			}
			var fs []engine.Finding
			if len(repos) == 0 {
				if fs, err = s.st.Findings(ctx, flt); err != nil {
					return err
				}
			}
			for _, r := range repos {
				flt.RepoID = r.ID
				got, err := s.st.Findings(ctx, flt)
				if err != nil {
					return err
				}
				fs = append(fs, got...)
			}
			if fs == nil {
				fs = []engine.Finding{}
			}
			if sarif != "" {
				if err := writeSARIFFile(sarif, fs); err != nil {
					return err
				}
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "kind": "findings", "findings": fs, "summary": engine.SummariseFindings(fs)})
			}
			if len(fs) == 0 {
				fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(i18n.T("audit.no_findings")))
				return nil
			}
			for _, f := range fs {
				a.printFinding(f)
			}
			return nil
		},
	}
	cf := cmd.Flags()
	cf.StringVar(&only, "only", "", "only repos whose path matches this glob")
	cf.StringVar(&check, "check", "", "only this check: secrets, deps, actions or agents")
	cf.StringVar(&sev, "severity", "", "only findings at least this severe")
	cf.StringVar(&sarif, "sarif", "", "also write them to this file as SARIF 2.1.0")
	cf.BoolVar(&ignored, "ignored", false, "include ignored findings")
	cf.BoolVar(&fixed, "fixed", false, "include fixed findings (upgraded dependencies, workflow and agent file problems gone from HEAD)")
	return cmd
}

func (a *app) auditIgnoreCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "ignore <finding-id>",
		Short: "Accept a finding so it stops being reported",
		Long: "Marks a finding as accepted, for example a test key that was never real. The\n" +
			"finding is kept and shown with --ignored; `audit unignore` brings it back.",
		Args: argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return &usageError{fmt.Errorf("--reason is required, e.g. --reason \"test key, never valid\"")}
			}
			return a.setIgnored(cmd, args[0], reason)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why this finding is safe to ignore (required)")
	return cmd
}

func (a *app) auditUnignoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unignore <finding-id>",
		Short: "Report an ignored finding again",
		Args:  argsN(1, 1),
		RunE:  func(cmd *cobra.Command, args []string) error { return a.setIgnored(cmd, args[0], "") },
	}
}

func (a *app) setIgnored(cmd *cobra.Command, idArg, reason string) error {
	ctx := cmd.Context()
	s, err := a.open(ctx, "", false)
	if err != nil {
		return err
	}
	defer s.close()
	id, err := s.st.ResolveFinding(ctx, idArg)
	if err != nil {
		return err
	}
	if reason == "" {
		err = s.st.UnignoreFinding(ctx, id)
	} else {
		err = s.st.IgnoreFinding(ctx, id, reason)
	}
	if err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "id": id, "ignored": reason != ""})
	}
	msg := i18n.T("audit.reported_again", "id", shortID(id))
	if reason != "" {
		msg = i18n.T("audit.now_ignored", "id", shortID(id))
	}
	fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(msg))
	return nil
}

func (a *app) printAudit(rep *engine.AuditReport) {
	if rep == nil {
		return
	}
	for _, f := range rep.Findings {
		if f.Status == engine.FindingIgnored {
			continue
		}
		a.printFinding(f)
	}
	for _, fl := range rep.Failed {
		a.warn(fl.Repo + ": " + fl.Error)
	}
	for _, t := range rep.Tools {
		for _, e := range t.Errors {
			a.warn(e)
		}
	}
	sum := rep.Summary
	line := i18n.T("audit.summary", "repos", rep.Repos, "history", rep.History, "total", sum.Total, "new", sum.New)
	var parts []string
	for i := len(engine.Severities) - 1; i >= 0; i-- {
		if n := sum.BySeverity[engine.Severities[i]]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, engine.Severities[i]))
		}
	}
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	icon, style := a.icon(iconOK), a.styles.OK
	if sum.Total > 0 {
		icon, style = a.icon(iconWarn), a.styles.Warn
	}
	fmt.Fprintln(a.out, style.Render(icon)+a.styles.Text.Render(line))
	if d := rep.Deps; d != nil {
		line := "  " + i18n.T("audit.deps", "packages", d.Packages, "lockfiles", d.Lockfiles)
		switch {
		case d.Fixed == 1:
			line += ", " + i18n.T("audit.fixed_one")
		case d.Fixed > 1:
			line += ", " + i18n.T("audit.fixed", "count", d.Fixed)
		}
		fmt.Fprintln(a.out, a.styles.Muted.Render(line))
		for _, u := range d.Unreadable {
			a.warn(i18n.T("audit.unreadable_lockfile", "file", u))
		}
	}
	if rep.Rules != "" || len(rep.Tools) > 0 {
		info := "  " + i18n.T("audit.rules", "version", rep.Rules)
		for _, t := range rep.Tools {
			info += " · " + i18n.T("audit.tool", "name", t.Name, "version", t.Version, "repos", t.Repos)
		}
		switch {
		case rep.HeadFixed == 1:
			info += " · " + i18n.T("audit.fixed_one")
		case rep.HeadFixed > 1:
			info += " · " + i18n.T("audit.fixed", "count", rep.HeadFixed)
		}
		fmt.Fprintln(a.out, a.styles.Muted.Render(info))
	}
	if rep.Skipped > 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.skipped", "count", rep.Skipped)))
	}
	if sum.Ignored > 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("audit.ignored_count", "count", sum.Ignored)))
	}
}

func (a *app) printFinding(f engine.Finding) {
	sev := strings.ToUpper(string(f.Severity))
	style := a.styles.Warn
	if f.Severity.Rank() >= engine.SevHigh.Rank() {
		style = a.styles.Fail
	}
	head := fmt.Sprintf("%-8s %s  %s", sev, shortID(f.ID), f.Title)
	if f.New {
		head += "  [" + i18n.T("audit.tag_new") + "]"
	}
	if f.Status == engine.FindingIgnored {
		head += "  [" + i18n.T("audit.tag_ignored", "note", f.IgnoreNote) + "]"
	}
	if f.Status == engine.FindingFixed {
		head += "  [" + i18n.T("audit.tag_fixed") + "]"
	}
	fmt.Fprintln(a.out, style.Render(head))
	where := f.RepoPath + "  " + f.File
	if f.Line > 0 {
		where += fmt.Sprintf(":%d", f.Line)
	}
	fmt.Fprintln(a.out, "         "+a.styles.Label.Render(where)+"  "+a.styles.Muted.Render(f.Preview))
	var meta []string
	if f.Commit != "" {
		meta = append(meta, i18n.T("audit.commit", "sha", shortSHA(f.Commit)))
	}
	if f.Author != "" {
		meta = append(meta, f.Author)
	}
	if len(f.Date) >= 10 {
		meta = append(meta, f.Date[:10])
	}
	if f.Occurrences > 1 {
		meta = append(meta, i18n.T("audit.seen", "count", f.Occurrences))
	}
	if f.Confidence == engine.ConfidenceLow {
		meta = append(meta, i18n.T("audit.generic"))
	}
	if len(meta) > 0 {
		fmt.Fprintln(a.out, "         "+a.styles.Muted.Render(strings.Join(meta, " · ")))
	}
	fmt.Fprintln(a.out, "         "+a.styles.Text.Render(f.Hint))
}

func writeSARIFFile(name string, fs []engine.Finding) error {
	out, err := os.Create(name)
	if err != nil {
		return &core.Error{Category: core.CatPermission, Message: "cannot write SARIF file " + name, Err: err}
	}
	if err := engine.WriteSARIF(out, version.String(), fs); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func oneOfStr(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
