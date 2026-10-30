// Package cli implements the headless command-line interface. It contains
// no business logic: every command calls into the engine packages and
// renders the result as text or JSON.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
	"github.com/TTox-Tech/chitragit/internal/version"
)

// app carries global flags and lazily-loaded state shared by commands.
type app struct {
	out, errOut io.Writer

	archivePath string
	jsonOut     bool
	noColor     bool
	verbose     bool

	styles theme.Styles
	styled bool
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	a := &app{out: stdout, errOut: stderr}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return core.ExitOK
	}
	if errors.Is(err, context.Canceled) {
		return core.ExitInterrupted
	}
	var silent *silentError
	if errors.As(err, &silent) {
		return silent.code
	}
	a.printError(err)
	var locked *archive.LockedError
	if errors.As(err, &locked) {
		return core.ExitLocked
	}
	var flagErr *usageError
	if errors.As(err, &flagErr) {
		return core.ExitUsage
	}
	return core.ExitCodeFor(err)
}

// usageError marks bad flags/arguments so they map to ExitUsage.
type usageError struct{ error }

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "chitragit",
		Short:         i18n.T("app.tagline"),
		Long:          banner() + "\n" + i18n.T("app.tagline"),
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			a.setupOutput()
			return nil
		},
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &usageError{fmt.Errorf("unknown command %q; see `chitragit --help`", args[0])}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.interactive() {
				return a.runTUI(cmd.Context())
			}
			fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("app.tui_needs_terminal")))
			fmt.Fprintln(a.out)
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })

	pf := root.PersistentFlags()
	pf.StringVar(&a.archivePath, "archive", "", "path to the archive (default: search upward from the current folder)")
	pf.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON output")
	pf.BoolVar(&a.noColor, "no-color", false, "disable colours")
	pf.BoolVarP(&a.verbose, "verbose", "v", false, "show technical details")

	root.AddCommand(a.initCmd(), a.scanCmd(), a.syncCmd(), a.statusCmd(), a.lsCmd(),
		a.showCmd(), a.activityCmd(), a.healthCmd(), a.tagCmd(), a.favCmd(true), a.favCmd(false),
		a.doctorCmd(), a.configCmd(), a.versionCmd())
	return root
}

func (a *app) setupOutput() {
	term := platform.DetectTerminal()
	if a.noColor || term.NoColor || a.jsonOut {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	a.styles = theme.NewStyles(theme.Get("ledger"))
	a.styled = true
}

// findArchive resolves --archive or searches upward from the working dir.
func (a *app) findArchive() (*archive.Archive, error) {
	if a.archivePath != "" {
		return archive.Open(a.archivePath)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return archive.Find(wd)
}

// loadConfig merges user and (if found) archive configuration.
// The archive may be nil.
func (a *app) loadConfig(arc *archive.Archive) (*config.Config, []config.Warning, error) {
	src := config.Sources{}
	if f, err := platform.UserConfigFile(); err == nil {
		src.UserFile = f
	}
	if arc != nil {
		src.ArchiveFile = arc.ConfigFile()
	}
	cfg, warnings, err := config.Load(src)
	if err == nil {
		i18n.SetLanguage(cfg.UI.Language)
		a.styles = theme.NewStyles(theme.Get(cfg.UI.Theme))
	}
	return cfg, warnings, err
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) warn(msg string) {
	fmt.Fprintln(a.errOut, a.styles.Warn.Render("! "+msg))
}

// printError renders an error with its hint and, in verbose mode, details.
func (a *app) printError(err error) {
	if !a.styled {
		a.setupOutput()
	}
	if a.jsonOut {
		out := map[string]any{"error": err.Error(), "category": string(core.CategoryOf(err))}
		var ce *core.Error
		if errors.As(err, &ce) {
			out["message"], out["hint"], out["detail"] = ce.Message, ce.Hint, ce.Detail
		}
		enc := json.NewEncoder(a.errOut)
		_ = enc.Encode(out)
		return
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		fmt.Fprintln(a.errOut, a.styles.Fail.Render("✗ "+err.Error()))
		return
	}
	fmt.Fprintln(a.errOut, a.styles.Fail.Render("✗ "+ce.Error()))
	if ce.Detail != "" && (a.verbose || ce.Category == core.CatConfig) {
		fmt.Fprintln(a.errOut, a.styles.Muted.Render(indent(ce.Detail)))
	}
	if ce.Hint != "" {
		fmt.Fprintln(a.errOut, a.styles.Accent.Render("  "+i18n.T("error.hint")+": ")+a.styles.Text.Render(ce.Hint))
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func banner() string {
	return `   ___ _    _ _             ___ _ _
  / __| |_ (_) |_ _ _ __ _ / __(_) |_
 | (__| ' \| |  _| '_/ _' | (_ | |  _|
  \___|_||_|_|\__|_| \__,_|\___|_|\__|`
}

// argsN returns a cobra.PositionalArgs that requires between min and max
// arguments and reports violations as usage errors.
func argsN(min, max int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < min || len(args) > max {
			if min == max {
				return &usageError{fmt.Errorf("expected %d argument(s), got %d", min, len(args))}
			}
			return &usageError{fmt.Errorf("expected %d to %d argument(s), got %d", min, max, len(args))}
		}
		return nil
	}
}
