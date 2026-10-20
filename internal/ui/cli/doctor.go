package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/store"
)

type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

type check struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
	Hint   string      `json:"hint,omitempty"`
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that your system and archive are ready",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := a.runChecks(cmd.Context())
			failed := 0
			for _, c := range checks {
				if c.Status == statusFail {
					failed++
				}
			}
			if a.jsonOut {
				if err := a.printJSON(map[string]any{"checks": checks, "problems": failed}); err != nil {
					return err
				}
			} else {
				a.renderChecks(checks, failed)
			}
			if failed > 0 {
				return &silentError{code: core.ExitEnvironment}
			}
			return nil
		},
	}
}

// silentError ends the command with an exit code without printing more,
// because the command already reported the problem itself.
type silentError struct{ code int }

func (e *silentError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func (a *app) runChecks(ctx context.Context) []check {
	var checks []check
	add := func(name string, st checkStatus, detail, hint string) {
		checks = append(checks, check{Name: name, Status: st, Detail: detail, Hint: hint})
	}
	fromErr := func(name string, err error) {
		var ce *core.Error
		if errors.As(err, &ce) {
			add(name, statusFail, ce.Message, ce.Hint)
			return
		}
		add(name, statusFail, err.Error(), "")
	}

	// Git
	hooks, err := platform.EmptyHooksDir()
	if err != nil {
		fromErr(i18n.T("doctor.dirs"), err)
	}
	if runner, err := gitx.New(gitx.Options{HooksDir: hooks, Timeout: 10 * time.Second}); err != nil {
		fromErr(i18n.T("doctor.git"), err)
	} else if v, err := runner.CheckVersion(ctx); err != nil {
		fromErr(i18n.T("doctor.git"), err)
	} else {
		add(i18n.T("doctor.git"), statusOK, fmt.Sprintf("%s (%s)", v, runner.Bin()), "")
	}

	// Git LFS
	if out, err := exec.CommandContext(ctx, "git", "lfs", "version").Output(); err != nil {
		add(i18n.T("doctor.git_lfs"), statusWarn, i18n.T("doctor.git_lfs_missing"), "see https://git-lfs.com")
	} else {
		v := strings.TrimSpace(string(out))
		v, _, _ = strings.Cut(v, " (")
		add(i18n.T("doctor.git_lfs"), statusOK, v, "")
	}

	// Archive, database, lock
	arc, arcErr := a.findArchive()
	switch {
	case arcErr == nil:
		add(i18n.T("doctor.archive"), statusOK, arc.Root, "")
		if s, err := store.Open(ctx, arc.DBFile()); err != nil {
			fromErr(i18n.T("doctor.database"), err)
		} else {
			v, _ := s.SchemaVersion(ctx)
			s.Close()
			add(i18n.T("doctor.database"), statusOK, i18n.T("doctor.database_schema", "version", v), "")
		}
		if l, err := arc.Acquire("chitragit doctor"); err != nil {
			var locked *archive.LockedError
			if errors.As(err, &locked) {
				add(i18n.T("doctor.lock"), statusWarn, locked.Error(), "")
			} else {
				fromErr(i18n.T("doctor.lock"), err)
			}
		} else if err := l.Release(); err != nil {
			fromErr(i18n.T("doctor.lock"), err)
		} else {
			add(i18n.T("doctor.lock"), statusOK, i18n.T("doctor.lock_free"), "")
		}
	case a.archivePath != "":
		fromErr(i18n.T("doctor.archive"), arcErr)
	default:
		add(i18n.T("doctor.archive"), statusWarn, i18n.T("doctor.archive_none"), "run `chitragit init` in your archive folder")
		arc = nil
	}

	// Configuration
	if _, warnings, err := a.loadConfig(arc); err != nil {
		var ce *core.Error
		if errors.As(err, &ce) {
			add(i18n.T("doctor.config"), statusFail, ce.Message+"\n"+ce.Detail, ce.Hint)
		} else {
			fromErr(i18n.T("doctor.config"), err)
		}
	} else if len(warnings) > 0 {
		msgs := make([]string, len(warnings))
		for i, w := range warnings {
			msgs[i] = w.String()
		}
		add(i18n.T("doctor.config"), statusWarn, strings.Join(msgs, "\n"), "")
	} else {
		add(i18n.T("doctor.config"), statusOK, i18n.T("doctor.config_ok"), "")
	}

	// Terminal
	t := platform.DetectTerminal()
	desc := "not a terminal (output is piped)"
	if t.IsTTY {
		desc = fmt.Sprintf("%d×%d", t.Width, t.Height)
		if t.Term != "" {
			desc += ", TERM=" + t.Term
		}
		if t.SSH {
			desc += ", over SSH (animations will be light)"
		}
		if t.NoColor {
			desc += ", no colour"
		}
	}
	st := statusOK
	hint := ""
	if t.IsTTY && (t.Width < 80 || t.Height < 24) {
		st, hint = statusWarn, "the interactive ledger needs at least 80×24"
	}
	add(i18n.T("doctor.terminal"), st, desc, hint)

	return checks
}

func (a *app) renderChecks(checks []check, failed int) {
	fmt.Fprintln(a.out, a.styles.Title.Render("📜 "+i18n.T("doctor.title")))
	fmt.Fprintln(a.out)
	width := 0
	for _, c := range checks {
		width = max(width, len([]rune(c.Name)))
	}
	for _, c := range checks {
		icon := a.styles.OK.Render("✓")
		switch c.Status {
		case statusWarn:
			icon = a.styles.Warn.Render("!")
		case statusFail:
			icon = a.styles.Fail.Render("✗")
		}
		name := c.Name + strings.Repeat(" ", width-len([]rune(c.Name)))
		lines := strings.Split(c.Detail, "\n")
		fmt.Fprintf(a.out, " %s %s  %s\n", icon, a.styles.Label.Render(name), a.styles.Text.Render(lines[0]))
		pad := strings.Repeat(" ", width+5)
		for _, l := range lines[1:] {
			fmt.Fprintln(a.out, pad+a.styles.Muted.Render(l))
		}
		if c.Hint != "" {
			fmt.Fprintln(a.out, pad+a.styles.Accent.Render("→ "+c.Hint))
		}
	}
	fmt.Fprintln(a.out)
	if failed == 0 {
		fmt.Fprintln(a.out, a.styles.OK.Render(i18n.T("doctor.summary_ok")))
	} else {
		fmt.Fprintln(a.out, a.styles.Fail.Render(i18n.T("doctor.summary_problems", "count", failed)))
	}
}
