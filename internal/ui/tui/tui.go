package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
)

// Options configure Run.
type Options struct {
	Backend  Backend
	Config   *config.Config
	Archive  string // archive root, shown in the header
	Terminal platform.TerminalInfo
	Stderr   io.Writer // told when the exit waits for repos still in flight
}

// New builds the app with the standard screens: the repo browser (home),
// the sync screen and the repo detail page, plus the activity feed and
// the health dashboard when the backend serves Insights. wg tracks background work
// that must finish before the program exits.
func New(ctx context.Context, opts Options, wg *sync.WaitGroup) *App {
	t := opts.Terminal
	level, auto := ResolveLevel(opts.Config.UI.Animations, t)
	ascii := t.Term == "dumb"
	s := NewStyles(theme.Get(opts.Config.UI.Theme), ascii)
	env := &Env{Ctx: ctx, Backend: opts.Backend, Config: opts.Config, Archive: opts.Archive}
	a := NewApp(env, s, newAnim(level, auto, ascii))
	a.Register(ScreenBrowser, NewBrowser())
	a.Register(ScreenSync, NewSyncScreen(wg))
	a.Register(ScreenDetail, NewDetail())
	if env.insights() != nil {
		a.Register(ScreenActivity, NewActivity())
		a.Register(ScreenHealth, NewHealth())
	}
	return a
}

// Run shows the TUI until the user quits. It returns only after any
// running sync has stopped and recorded its results.
func Run(ctx context.Context, opts Options) error {
	if opts.Terminal.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	} else {
		// The ledger theme adapts to the terminal's background, which
		// lipgloss asks the terminal for on first use. Ask now, before
		// Bubble Tea reads the keyboard: asked later, the answer and the
		// user's key presses would be read by the wrong reader.
		lipgloss.SetHasDarkBackground(lipgloss.HasDarkBackground())
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	app := New(ctx, opts, &wg)
	popts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if opts.Config.UI.Mouse {
		popts = append(popts, tea.WithMouseCellMotion())
	}
	_, err := tea.NewProgram(app, popts...).Run()
	// A forced quit (second Ctrl+C) can leave a sync running: stop it from
	// starting more repos and let the ones in flight finish safely.
	close(app.env.gone)
	if app.busy() && opts.Stderr != nil {
		fmt.Fprintln(opts.Stderr, i18n.T("tui.exit_waiting"))
	}
	cancel()
	wg.Wait()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
