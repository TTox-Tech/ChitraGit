package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
)

// Screens added by M7.
const (
	ScreenWizard   ScreenID = "wizard"
	ScreenSettings ScreenID = "settings"
)

// Setup is what the first-run wizard and the Settings screen need
// (PRD F13, F14, F16). It is separate from Backend so that a backend
// without it simply has no wizard or Settings screen.
type Setup interface {
	// ArchiveRoot is the open archive, or "" before the wizard creates one.
	ArchiveRoot() string
	// InitArchive sets up path as an archive (idempotent) and opens it.
	InitArchive(ctx context.Context, path string) (root string, err error)
	// Scan finds the repos in the open archive.
	Scan(ctx context.Context) (ScanSummary, error)
	// Settings returns the effective configuration and any warnings.
	Settings() (*config.Config, []string, error)
	// SaveSetting validates and writes one value to the archive config,
	// or to the user config when user is set. It returns the file written.
	SaveSetting(key, value string, user bool) (file string, err error)
	// Schedule describes the archive's scheduled sync.
	Schedule(ctx context.Context) (ScheduleInfo, error)
	// SetSchedule installs, replaces or (with Frequency "") removes it.
	SetSchedule(ctx context.Context, c ScheduleChoice) (ScheduleInfo, error)
}

// ScanSummary is the wizard's "Found 3,412 repos across 287 owners".
type ScanSummary struct{ Found, Owners int }

// ScheduleInfo describes a scheduled sync.
type ScheduleInfo struct {
	On      bool
	When    string // e.g. "daily at 02:00"
	Backend string // e.g. "cron"
	Next    time.Time
	Missing bool // saved, but the OS scheduler no longer has the job
}

// ScheduleChoice is what the user picked: Frequency "" (off), "daily" or
// "weekly".
type ScheduleChoice struct {
	Frequency    string
	Hour, Minute int
	Weekday      time.Weekday
}

// restyleMsg switches theme and animation level at once, for the
// wizard's live preview and for changes saved in Settings.
type restyleMsg struct{ theme, animations string }

func restyle(themeName, animations string) tea.Cmd {
	return func() tea.Msg { return restyleMsg{themeName, animations} }
}

func (a *App) restyle(m restyleMsg) {
	ascii := a.s.G.Rule == asciiGlyphs.Rule
	a.s = NewStyles(theme.Get(m.theme), ascii)
	if a.accessible {
		return // accessible mode never animates
	}
	level, auto := ResolveLevel(m.animations, a.term)
	a.anim.Level, a.anim.auto = level, auto
}

// field is a one-line text input: type, backspace, ctrl+u clears.
type field struct {
	value []rune
}

func (f *field) set(s string)   { f.value = []rune(s) }
func (f *field) String() string { return string(f.value) }

// key edits the field and reports whether it used the key.
func (f *field) key(k tea.KeyMsg) bool {
	switch k.Type {
	case tea.KeyBackspace:
		if len(f.value) > 0 {
			f.value = f.value[:len(f.value)-1]
		}
	case tea.KeyCtrlU:
		f.value = nil
	case tea.KeyRunes, tea.KeySpace:
		f.value = append(f.value, k.Runes...)
		if k.Type == tea.KeySpace && len(k.Runes) == 0 {
			f.value = append(f.value, ' ')
		}
	default:
		return false
	}
	return true
}

// view draws the field with a cursor at the end, w cells wide.
func (f *field) view(s *Styles, w int) string {
	text := truncateLeft(string(f.value), max(1, w-1))
	return s.Label.Render(text) + s.Cursor.Render(s.G.Cursor)
}

// truncateLeft keeps the end of s, which is the part being typed.
func truncateLeft(s string, w int) string {
	r := []rune(s)
	for len(r) > 0 && width(string(r)) > w {
		r = r[1:]
	}
	return string(r)
}
