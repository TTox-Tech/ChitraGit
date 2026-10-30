// Package tui is ChitraGit's interactive ledger: a Bubble Tea app shell
// with clickable tabs, a repo browser and a live sync screen (PRD F7, F11,
// F18). It holds no business logic. Everything it shows or changes goes
// through the Backend interface, so the same screens can later talk to a
// remote archive (PRD §7.5).
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// ScreenID names a screen for navigation.
type ScreenID string

// Screens built into the shell. Later milestones register more
// (detail, activity, health, conflicts, settings).
const (
	ScreenBrowser ScreenID = "browser"
	ScreenSync    ScreenID = "sync"
	ScreenDetail  ScreenID = "detail"
)

// Screen is one page of the app. Screens are pointers and change
// themselves in Update. The shell draws the header, the footer and any
// dialog; a screen draws only the body.
type Screen interface {
	// Title is the tab label. Screens with no title get no tab and are
	// reached by navigation only.
	Title() string
	Init(env *Env) tea.Cmd
	// Update receives key and mouse messages while the screen is active,
	// and every other message whether active or not.
	Update(msg tea.Msg, env *Env) tea.Cmd
	// View renders exactly f.Height lines of at most f.Width cells.
	View(f Frame) string
	// Keys are the shortcuts shown in the footer.
	Keys() []KeyHint
}

// Optional screen behaviours, checked with type assertions.
type (
	// capturer screens take every key, e.g. while a search box has focus,
	// so global shortcuts like q do not fire.
	capturer interface{ Capturing() bool }
	// animator screens ask for animation ticks while they return true.
	animator interface{ Animating() bool }
	// busyScreen screens have work that must stop safely before quitting.
	busyScreen interface {
		Busy() bool
		Stop() tea.Cmd
	}
)

// KeyHint is one footer entry, e.g. {"/", "search"}.
type KeyHint struct{ Key, Label string }

// NavigateMsg switches to another screen. The target screen receives the
// message too, so Payload can tell it what to show.
type NavigateMsg struct {
	To      ScreenID
	Payload any
}

// Navigate returns a command that switches screens.
func Navigate(to ScreenID, payload any) tea.Cmd {
	return func() tea.Msg { return NavigateMsg{To: to, Payload: payload} }
}

// ReposChangedMsg tells screens that repo data in the archive changed,
// e.g. after a sync, so they reload it.
type ReposChangedMsg struct{}

// Env is shared by all screens.
type Env struct {
	Ctx     context.Context
	Backend Backend
	Config  *config.Config
	Archive string // archive root, for display
	Now     func() time.Time

	app *App
	// gone is closed when the program has exited, so background work stops
	// waiting for the screen to read its updates.
	gone chan struct{}
}

// Registered reports whether a screen exists, so screens can offer
// buttons only for features that are present.
func (e *Env) Registered(id ScreenID) bool {
	if e.app == nil {
		return false
	}
	_, ok := e.app.screens[id]
	return ok
}

// Backend is everything the TUI needs from an archive.
type Backend interface {
	Repos(ctx context.Context) ([]core.Repo, error)
	// Interrupted returns the last sync if it stopped before finishing,
	// or nil. It returns nil while another process is syncing.
	Interrupted(ctx context.Context) (*Interrupted, error)
	// Discard forgets an interrupted run without syncing anything.
	Discard(ctx context.Context, runID int64) error
	// Sync scans the archive, then syncs it. It holds the archive lock for
	// the whole run and calls listen from worker goroutines, one at a time.
	Sync(ctx context.Context, req SyncStart, listen func(SyncUpdate)) (*engine.SyncReport, error)
}

// Interrupted describes a sync that did not finish.
type Interrupted struct {
	RunID     int64
	StartedAt time.Time
	Total     int
	Pending   int
}

// SyncStart says what to sync.
type SyncStart struct {
	IDs []string // empty = every repo
	// OnInterrupted is "resume" or "fresh"; it matters only when the last
	// run did not finish. The sync screen asks the user before choosing.
	OnInterrupted string
	Gate          func(ctx context.Context) error // pause support; see syncer.Options.Gate
}

// SyncUpdate is one progress report from a running sync. Exactly one
// field is set.
type SyncUpdate struct {
	Scanned *engine.ScanReport
	Planned *int
	Event   *syncer.Event
}
