package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// fakeSetup records what the wizard and Settings ask for.
type fakeSetup struct {
	root     string
	cfg      *config.Config
	saved    []string // "key=value user"
	schedule *ScheduleChoice
	scans    int
}

func (f *fakeSetup) ArchiveRoot() string { return f.root }
func (f *fakeSetup) InitArchive(_ context.Context, path string) (string, error) {
	if path == "/bad" {
		return "", errors.New("/bad is not a folder")
	}
	f.root = path
	return path, nil
}
func (f *fakeSetup) Scan(context.Context) (ScanSummary, error) {
	f.scans++
	return ScanSummary{Found: 3412, Owners: 287}, nil
}
func (f *fakeSetup) Settings() (*config.Config, []string, error) { return f.cfg, nil, nil }
func (f *fakeSetup) SaveSetting(key, value string, user bool) (string, error) {
	c := *f.cfg
	if err := c.Set(key, value); err != nil {
		return "", err
	}
	if err := c.Validate(); err != nil {
		return "", errors.New(key + " = " + value + " is not allowed")
	}
	*f.cfg = c
	scope := "archive"
	if user {
		scope = "user"
	}
	f.saved = append(f.saved, key+"="+value+" "+scope)
	return "/cfg/" + scope + ".toml", nil
}
func (f *fakeSetup) Schedule(context.Context) (ScheduleInfo, error) {
	if f.schedule == nil {
		return ScheduleInfo{Backend: "cron"}, nil
	}
	return ScheduleInfo{On: true, When: "daily at 02:00", Backend: "cron"}, nil
}
func (f *fakeSetup) SetSchedule(_ context.Context, c ScheduleChoice) (ScheduleInfo, error) {
	if c.Frequency == "" {
		f.schedule = nil
		return ScheduleInfo{Backend: "cron"}, nil
	}
	f.schedule = &c
	return ScheduleInfo{On: true, When: c.Frequency, Backend: "cron"}, nil
}

func newSetupHarness(t *testing.T, root string, firstRun, accessible bool) (*harness, *fakeSetup) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	cfg := config.Defaults()
	cfg.UI.Animations = "off"
	be := &fakeBackend{repos: sampleRepos()}
	fs := &fakeSetup{root: root, cfg: cfg}
	wg := &sync.WaitGroup{}
	app := New(context.Background(), Options{Backend: be, Setup: fs, Config: cfg, Archive: root,
		Terminal: platform.TerminalInfo{IsTTY: true, Term: "xterm-256color"},
		FirstRun: firstRun, StartDir: "/home/dev/repos", Accessible: accessible}, wg)
	app.env.Now = func() time.Time { return now }
	h := &harness{t: t, app: app, be: be, wg: wg}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h, fs
}

func TestWizardFullRun(t *testing.T) {
	h, fs := newSetupHarness(t, "", true, false)
	h.init()
	if h.app.active != ScreenWizard {
		t.Fatalf("first run should open the wizard, got %s", h.app.active)
	}
	golden(t, "wizard_archive", h.view())

	// q and tab are typed into the path, not global shortcuts.
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("/", "b", "a", "d", "enter")
	if !strings.Contains(h.view(), "/bad is not a folder") {
		t.Fatal("init error should be shown")
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("/", "a", "r", "c", "q", "enter")
	if fs.root != "/arcq" || fs.scans != 1 || h.app.env.Archive != "/arcq" {
		t.Fatalf("archive not set up and scanned: %+v archive=%q", fs, h.app.env.Archive)
	}
	if !strings.Contains(h.view(), "Found 3,412 repos across 287 owners") {
		t.Fatalf("scan summary missing:\n%s", h.view())
	}

	h.key("enter") // → look
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	if h.app.s.Palette != themeGet("ledger-light") {
		t.Error("choosing a theme should restyle at once")
	}
	golden(t, "wizard_look", h.view())
	h.key("enter")
	if strings.Join(fs.saved, ",") != "ui.theme=ledger-light user,ui.animations=off user" {
		t.Fatalf("saved = %v", fs.saved)
	}

	// schedule: weekly, 03:30, Friday
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	h.send(tea.KeyMsg{Type: tea.KeyDown})
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("0", "3", ":", "3", "0")
	x, y := h.findZone("wz:day:5")
	h.click(x, y, false)
	golden(t, "wizard_schedule", h.view())
	h.key("enter")
	if c := fs.schedule; c == nil || c.Frequency != "weekly" || c.Hour != 3 || c.Minute != 30 || c.Weekday != time.Friday {
		t.Fatalf("schedule = %+v", fs.schedule)
	}

	// sync now
	h.key("y")
	if h.app.active != ScreenSync || len(h.be.syncs) != 1 {
		t.Fatalf("sync now should start a sync: active=%s syncs=%d", h.app.active, len(h.be.syncs))
	}
}

func TestWizardSkipKeepsDefaults(t *testing.T) {
	h, fs := newSetupHarness(t, "/arc", true, false)
	h.init()
	if fs.scans != 1 {
		t.Fatal("with an archive the wizard starts by scanning it")
	}
	if !strings.Contains(h.view(), "step 1 of 4") {
		t.Errorf("archive step should not be counted:\n%s", h.view())
	}
	h.key("enter")                         // scan → look
	h.send(tea.KeyMsg{Type: tea.KeyRight}) // preview a theme…
	h.key("esc")                           // …then skip: the preview is undone
	if h.app.s.Palette != themeGet("ledger") {
		t.Error("skipping the look step should restore the theme")
	}
	h.key("esc") // skip schedule
	if fs.schedule != nil || len(fs.saved) != 0 {
		t.Fatalf("skipping must not change anything: %+v %v", fs.schedule, fs.saved)
	}
	h.key("n")
	if h.app.active != ScreenBrowser || len(h.be.syncs) != 0 {
		t.Fatalf("Later should open the browser: %s", h.app.active)
	}
}

func TestWizardBadTime(t *testing.T) {
	h, fs := newSetupHarness(t, "/arc", true, false)
	h.init()
	h.key("enter", "enter") // scan, look
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	h.send(tea.KeyMsg{Type: tea.KeyDown})
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("2", "5", ":", "0", "0", "enter")
	if fs.schedule != nil || !strings.Contains(h.view(), "invalid time") {
		t.Fatalf("bad time should be refused:\n%s", h.view())
	}
}

func TestSettingsEditToggleAndRefuse(t *testing.T) {
	h, fs := newSetupHarness(t, "/arc", false, false)
	h.init()
	if h.app.active != ScreenBrowser {
		t.Fatalf("not first run: active = %s", h.app.active)
	}
	h.send(NavigateMsg{To: ScreenSettings})
	st := h.app.screens[ScreenSettings].(*Settings)
	golden(t, "settings", h.view())

	// Edit sync.workers.
	st.cursor = indexOf(st.keys, "sync.workers")
	h.key("enter")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("1", "2", "enter")
	if fs.cfg.Sync.Workers != 12 || !strings.Contains(h.view(), "saved to /cfg/archive.toml") {
		t.Fatalf("workers = %d\n%s", fs.cfg.Sync.Workers, h.view())
	}

	// A bad value is refused and nothing changes.
	st.cursor = indexOf(st.keys, "sync.mode")
	h.key("enter")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("y", "o", "l", "o", "enter")
	if fs.cfg.Sync.Mode != "ff" || !strings.Contains(h.view(), "not allowed") {
		t.Fatalf("bad value should be refused: mode=%s\n%s", fs.cfg.Sync.Mode, h.view())
	}

	// Booleans toggle; u switches to the personal config.
	h.key("u")
	st.cursor = indexOf(st.keys, "ui.mouse")
	h.key("enter")
	if fs.cfg.UI.Mouse || fs.saved[len(fs.saved)-1] != "ui.mouse=false user" {
		t.Fatalf("toggle: %v", fs.saved)
	}

	// Changing the theme restyles the app.
	st.cursor = indexOf(st.keys, "ui.theme")
	h.key("enter")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	h.key("h", "i", "g", "h", "-", "c", "o", "n", "t", "r", "a", "s", "t", "enter")
	if h.app.s.Palette != themeGet("high-contrast") {
		t.Error("theme change should apply at once")
	}

	// t opens the schedule step and comes back to Settings.
	h.key("t")
	if h.app.active != ScreenWizard {
		t.Fatalf("t should open the schedule step: %s", h.app.active)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRight})
	h.key("enter")
	if h.app.active != ScreenSettings || fs.schedule == nil || fs.schedule.Frequency != "daily" {
		t.Fatalf("schedule from settings: active=%s %+v", h.app.active, fs.schedule)
	}
}

func TestAccessibleTUI(t *testing.T) {
	h, _ := newSetupHarness(t, "/arc", false, true)
	h.app.env.Config.UI.Animations = "full"
	h.init()
	if h.app.anim.Level != LevelOff || h.app.s.G.Rule != "-" {
		t.Fatalf("accessible mode must not animate and must use ASCII: %v %q", h.app.anim.Level, h.app.s.G.Rule)
	}
	h.send(restyleMsg{"ledger", "full"})
	if h.app.anim.Level != LevelOff {
		t.Error("settings changes must not turn animation back on")
	}
}
