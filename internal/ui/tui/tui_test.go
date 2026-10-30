package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/syncer"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
)

var update = flag.Bool("update", false, "rewrite golden files")

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// fakeBackend serves fixed repos and scripted syncs.
type fakeBackend struct {
	repos       []core.Repo
	interrupted *Interrupted
	discarded   int64
	syncs       []SyncStart
	// release, if set, holds the sync until closed.
	release chan struct{}
}

func (f *fakeBackend) Repos(context.Context) ([]core.Repo, error) { return f.repos, nil }
func (f *fakeBackend) Interrupted(context.Context) (*Interrupted, error) {
	return f.interrupted, nil
}
func (f *fakeBackend) Discard(_ context.Context, id int64) error { f.discarded = id; return nil }

func (f *fakeBackend) Sync(ctx context.Context, req SyncStart, listen func(SyncUpdate)) (*engine.SyncReport, error) {
	f.syncs = append(f.syncs, req)
	listen(SyncUpdate{Scanned: &engine.ScanReport{Found: len(f.repos)}})
	n := len(f.repos)
	listen(SyncUpdate{Planned: &n})
	rep := &engine.SyncReport{Mode: "ff", Counts: map[string]int{}}
	for _, r := range f.repos {
		if err := req.Gate(ctx); err != nil {
			rep.Interrupted = true
			break
		}
		listen(SyncUpdate{Event: &syncer.Event{Kind: syncer.RepoStarted, Repo: r}})
		listen(SyncUpdate{Event: &syncer.Event{Kind: syncer.PhaseChanged, Repo: r, Phase: syncer.PhaseFetch}})
		if f.release != nil {
			<-f.release
			f.release = nil
		}
		res := &syncer.Result{Repo: r, Outcome: syncer.Unchanged, Status: syncer.StatusOK}
		if r.Status == syncer.StatusFailed {
			res.Outcome, res.Reason = syncer.Failed, "could not reach the host"
		}
		if r.Status == syncer.StatusConflict {
			res.Outcome, res.Status, res.ConflictFiles = syncer.Skipped, syncer.StatusConflict, []string{"main.go"}
		}
		rep.Counts[res.Outcome]++
		listen(SyncUpdate{Event: &syncer.Event{Kind: syncer.RepoFinished, Repo: r, Result: res}})
	}
	rep.Duration = 3 * time.Second
	return rep, nil
}

func sampleRepos() []core.Repo {
	return []core.Repo{
		{ID: "1", Path: "torvalds/linux", Owner: "torvalds", Name: "linux", Provider: "github", Host: "github.com",
			Status: "ok", LastSyncedAt: now.Add(-3 * time.Hour).Format(time.RFC3339Nano)},
		{ID: "2", Path: "charmbracelet/bubbletea", Owner: "charmbracelet", Name: "bubbletea", Provider: "github",
			Status: "diverged", StatusReason: "you have 1 local commit", LastSyncedAt: now.Add(-50 * time.Hour).Format(time.RFC3339Nano)},
		{ID: "3", Path: "gitlab/some/tool", Owner: "some", Name: "tool", Provider: "gitlab", Status: "failed", Favourite: true},
		{ID: "4", Path: "old/gone", Status: "ok", Missing: true},
		{ID: "5", Path: "notes/local-only", Status: "unknown"},
	}
}

// harness drives an App the way Bubble Tea would, running commands
// synchronously so tests are deterministic.
type harness struct {
	t   *testing.T
	app *App
	be  *fakeBackend
	wg  *sync.WaitGroup
}

func newHarness(t *testing.T, w, h int, anim string) *harness {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	cfg := config.Defaults()
	cfg.UI.Animations = anim
	be := &fakeBackend{repos: sampleRepos()}
	wg := &sync.WaitGroup{}
	app := New(context.Background(), Options{Backend: be, Config: cfg, Archive: "/archive",
		Terminal: platform.TerminalInfo{IsTTY: true, Term: "xterm-256color"}}, wg)
	app.env.Now = func() time.Time { return now }
	hs := &harness{t: t, app: app, be: be, wg: wg}
	hs.send(tea.WindowSizeMsg{Width: w, Height: h})
	return hs
}

func (h *harness) init() { h.run(h.app.Init()) }

// send delivers a message and runs the resulting commands, except ticks
// and quit, until nothing is left.
func (h *harness) send(msg tea.Msg) {
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil, tickMsg:
	case tea.BatchMsg:
		for _, c := range m {
			h.run(c)
		}
	case tea.QuitMsg:
		h.app.quitting = true
	default:
		h.send(msg)
	}
}

func (h *harness) key(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		h.send(msg)
	}
}

func (h *harness) click(x, y int, shift bool) {
	h.send(tea.MouseMsg{X: x, Y: y, Shift: shift, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

func (h *harness) view() string { return h.app.View() }

func (h *harness) browser() *Browser { return h.app.screens[ScreenBrowser].(*Browser) }

// findZone returns the top-left corner of a zone from the last render.
func (h *harness) findZone(id string) (int, int) {
	h.t.Helper()
	h.view()
	for _, z := range h.app.zones.list {
		if z.id == id {
			return z.x, z.y
		}
	}
	h.t.Fatalf("no zone %q", id)
	return 0, 0
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden file; run go test ./internal/ui/tui -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func TestGoldenBrowser(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"wide", 120, 24}, {"compact", 80, 24}, {"too_small", 59, 19},
	} {
		t.Run(size.name, func(t *testing.T) {
			h := newHarness(t, size.w, size.h, "off")
			h.init()
			golden(t, "browser_"+size.name, h.view())
		})
	}
}

func TestGoldenSyncDone(t *testing.T) {
	h := newHarness(t, 100, 30, "off")
	h.init()
	h.key("S")
	h.wg.Wait()
	golden(t, "sync_done", h.view())
}

func TestBrowserSearchFilterSort(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	h.init()
	b := h.browser()
	if len(b.rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(b.rows))
	}
	h.key("/", "l", "n", "x")
	if !b.Capturing() || len(b.rows) != 1 || b.Current().Path != "torvalds/linux" {
		t.Fatalf("search lnx: rows=%d current=%v", len(b.rows), b.Current())
	}
	h.key("q") // typed into the search, not quit
	if h.app.quitting || b.query != "lnxq" {
		t.Fatalf("q while searching: quitting=%v query=%q", h.app.quitting, b.query)
	}
	h.key("esc")
	if b.query != "" || len(b.rows) != 5 {
		t.Fatalf("esc should clear search")
	}
	h.key("f") // needs a look
	if len(b.rows) != 1 || b.Current().ID != "2" {
		t.Fatalf("attention filter: %d rows", len(b.rows))
	}
	h.key("f") // failed
	if len(b.rows) != 1 || b.Current().ID != "3" {
		t.Fatalf("failed filter: %d rows", len(b.rows))
	}
	h.key("esc") // clears the filter
	x, y := h.findZone("col:3")
	h.click(x, y, false) // sort by status
	if b.sortCol != 3 || b.desc {
		t.Fatalf("header click: sortCol=%d desc=%v", b.sortCol, b.desc)
	}
	h.click(x, y, false)
	if !b.desc {
		t.Fatalf("second header click should reverse")
	}
}

func TestBrowserMarkAndSyncSelection(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	h.init()
	b := h.browser()
	h.key("space", "space")
	if got := len(b.Marked()); got != 2 {
		t.Fatalf("marked = %d, want 2", got)
	}
	// Shift-click marks a range from the anchor.
	x, y := h.findZone("row:4")
	h.click(x, y, true)
	if got := len(b.Marked()); got != 5 {
		t.Fatalf("after shift-click marked = %d, want 5", got)
	}
	h.key("esc")
	if len(b.Marked()) != 0 {
		t.Fatal("esc should clear marks")
	}
	h.key("space", "s")
	h.wg.Wait()
	if len(h.be.syncs) != 1 || len(h.be.syncs[0].IDs) != 1 || h.app.active != ScreenSync {
		t.Fatalf("sync selection: %+v active=%s", h.be.syncs, h.app.active)
	}
}

func TestDoubleClickOpensDetail(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	h.init()
	x, y := h.findZone("row:1")
	h.click(x, y, false)
	h.click(x, y, false)
	if h.app.active != ScreenDetail {
		t.Fatalf("active = %s, want detail", h.app.active)
	}
	if !strings.Contains(h.view(), h.browser().Current().Path) {
		t.Fatal("detail should show the repo path")
	}
	h.key("esc")
	if h.app.active != ScreenBrowser {
		t.Fatal("esc should go back")
	}
}

func TestTabsByClickAndKey(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	h.init()
	x, y := h.findZone("tab:sync")
	h.click(x, y, false)
	if h.app.active != ScreenSync {
		t.Fatal("tab click")
	}
	h.key("1")
	if h.app.active != ScreenBrowser {
		t.Fatal("key 1")
	}
}

func TestResumeDialogOnStart(t *testing.T) {
	h := newHarness(t, 100, 30, "off")
	h.be.interrupted = &Interrupted{RunID: 7, StartedAt: now.Add(-2 * time.Hour), Total: 5000, Pending: 1800}
	h.init()
	v := h.view()
	if h.app.dialog == nil || !strings.Contains(v, "3,200 / 5,000") || !strings.Contains(v, "2h ago") {
		t.Fatalf("resume dialog not shown:\n%s", v)
	}
	h.key("d") // Discard
	if h.be.discarded != 7 || h.app.dialog != nil {
		t.Fatalf("discard: %d", h.be.discarded)
	}
}

func TestSyncAsksAboutInterruptedRun(t *testing.T) {
	h := newHarness(t, 100, 30, "off")
	h.init()
	h.be.interrupted = &Interrupted{RunID: 3, StartedAt: now, Total: 10, Pending: 4}
	h.key("space", "s")
	if h.app.dialog == nil || len(h.be.syncs) != 0 {
		t.Fatal("starting a sync over an interrupted run must ask first")
	}
	h.key("enter") // Resume
	h.wg.Wait()
	if len(h.be.syncs) != 1 || h.be.syncs[0].OnInterrupted != "resume" || len(h.be.syncs[0].IDs) != 0 {
		t.Fatalf("resume: %+v", h.be.syncs)
	}
}

func TestPauseAndQuitStopSafely(t *testing.T) {
	h := newHarness(t, 100, 30, "off")
	h.init()
	h.be.release = make(chan struct{})
	h.app.screens[ScreenSync].(*SyncScreen).request(SyncStart{OnInterrupted: "fresh"}, h.app.env)
	h.send(NavigateMsg{To: ScreenSync})
	sc := h.app.screens[ScreenSync].(*SyncScreen)
	if !sc.Busy() {
		t.Fatal("sync should be running")
	}
	h.key("p")
	if !sc.paused() {
		t.Fatal("p should pause")
	}
	// Quit asks first, then stops the run instead of killing it.
	h.key("q")
	if h.app.dialog == nil {
		t.Fatal("quit while syncing should ask")
	}
	h.key("enter")
	if sc.state != syncStopping || !h.app.quitting {
		t.Fatalf("state=%d quitting=%v", sc.state, h.app.quitting)
	}
	close(h.be.release)
	h.wg.Wait()
}

func TestAnimationLevels(t *testing.T) {
	cases := []struct {
		setting string
		term    platform.TerminalInfo
		want    Level
	}{
		{"auto", platform.TerminalInfo{IsTTY: true, Term: "xterm"}, LevelFull},
		{"auto", platform.TerminalInfo{IsTTY: true, Term: "xterm", SSH: true}, LevelLight},
		{"auto", platform.TerminalInfo{IsTTY: true, Term: "dumb"}, LevelOff},
		{"auto", platform.TerminalInfo{IsTTY: true, NoColor: true}, LevelOff},
		{"full", platform.TerminalInfo{SSH: true}, LevelFull},
		{"off", platform.TerminalInfo{IsTTY: true}, LevelOff},
	}
	for _, c := range cases {
		if got, _ := ResolveLevel(c.setting, c.term); got != c.want {
			t.Errorf("ResolveLevel(%q, %+v) = %v, want %v", c.setting, c.term, got, c.want)
		}
	}
	a := newAnim(LevelFull, true, false)
	for range 30 {
		a.observe(40 * time.Millisecond)
	}
	if a.Level != LevelLight {
		t.Error("slow frames should drop auto Full to Light")
	}
}

func TestFullAnimationRenders(t *testing.T) {
	h := newHarness(t, 100, 30, "full")
	if h.app.splash == 0 || !strings.Contains(h.view(), "") {
		t.Fatal("full mode starts with the logo reveal")
	}
	h.init()
	for range splashTicks + 2 {
		h.send(tickMsg{})
	}
	h.key("S")
	h.wg.Wait()
	for range 20 {
		h.send(tickMsg{})
		_ = h.view()
	}
	if !strings.Contains(h.view(), "Sync finished") {
		t.Fatal("sync screen should finish under full animation")
	}
}

func TestFuzzyRanking(t *testing.T) {
	a, ok1 := fuzzyScore("linux", "torvalds/linux")
	b, ok2 := fuzzyScore("linux", "l_i_n_u_x")
	if !ok1 || !ok2 || a <= b {
		t.Fatalf("linux: %d vs %d", a, b)
	}
	if _, ok := fuzzyScore("xyz", "torvalds/linux"); ok {
		t.Fatal("non-subsequence matched")
	}
}

func TestLightThemePaintsBackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	s := NewStyles(themeGet("ledger-light"), false)
	out := s.paint(s.OK.Render("x") + " y")
	if s.bg == "" || !strings.HasPrefix(out, s.bg) || strings.Count(out, s.bg) < 2 {
		t.Fatalf("background not repainted after resets: %q", out)
	}
}

func themeGet(name string) theme.Palette { return theme.Get(name) }
