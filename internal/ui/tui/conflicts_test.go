package tui

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/conflict"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// fakeConflicts adds a scripted resolve session to fakeBackend: repo 2
// clashes in a text file with two hunks and in a binary file.
type fakeConflicts struct {
	*fakeBackend
	list     []store.Conflict
	st       *ConflictState
	calls    []string
	editorOK bool
}

func newFakeConflicts() *fakeConflicts {
	be := &fakeBackend{repos: sampleRepos()}
	be.repos[1].Status = syncer.StatusConflict
	return &fakeConflicts{fakeBackend: be, list: []store.Conflict{
		{Repo: "charmbracelet/bubbletea", RepoID: "2", Status: "conflict", Files: []string{"main.go", "logo.png"}},
	}}
}

func (f *fakeConflicts) state() *ConflictState {
	cp := *f.st
	cp.Files = append([]conflict.File(nil), f.st.Files...)
	return &cp
}

func (f *fakeConflicts) Conflicts(context.Context) ([]store.Conflict, error) { return f.list, nil }

func (f *fakeConflicts) ConflictOpen(_ context.Context, id string, start bool) (*ConflictState, error) {
	f.calls = append(f.calls, "open "+id)
	if f.st == nil && start {
		f.st = &ConflictState{Repo: f.repos[1],
			Files: []conflict.File{
				{Path: "main.go", Kind: conflict.KindContent, Hunks: 2},
				{Path: "logo.png", Kind: conflict.KindBinary},
			},
			Hunks: map[string][]conflict.Hunk{"main.go": {
				{Line: 3, Theirs: "\tfmt.Println(\"upstream\")\n", Mine: "\tfmt.Println(\"mine\")\n"},
				{Line: 9, Theirs: "return nil\n", Mine: ""},
			}},
		}
	}
	return f.state(), nil
}

func (f *fakeConflicts) ConflictResolve(_ context.Context, id, path string, hunk int, c conflict.Choice) (*ConflictState, error) {
	f.calls = append(f.calls, "resolve "+path+" "+strconv.Itoa(hunk)+" "+string(c))
	for i := range f.st.Files {
		fl := &f.st.Files[i]
		if fl.Path != path {
			continue
		}
		hs := f.st.Hunks[path]
		if hunk >= 0 && hunk < len(hs) {
			hs = append(hs[:hunk:hunk], hs[hunk+1:]...)
		} else {
			hs = nil
		}
		f.st.Hunks[path], fl.Hunks, fl.Resolved = hs, len(hs), len(hs) == 0
	}
	return f.state(), nil
}

func (f *fakeConflicts) ConflictMarkResolved(_ context.Context, id, path string) (*ConflictState, error) {
	f.calls = append(f.calls, "mark "+path)
	return f.ConflictResolve(context.Background(), id, path, -1, conflict.Mine)
}

func (f *fakeConflicts) ConflictFinish(context.Context, string) error {
	f.calls = append(f.calls, "finish")
	f.st, f.list = nil, nil
	return nil
}

func (f *fakeConflicts) ConflictAbort(context.Context, string) error {
	f.calls = append(f.calls, "abort")
	f.st = nil
	return nil
}

func (f *fakeConflicts) ConflictDismiss(context.Context, string) error {
	f.calls = append(f.calls, "dismiss")
	f.list = nil
	return nil
}

func (f *fakeConflicts) ConflictEditor(context.Context, string, string) (*exec.Cmd, error) {
	if !f.editorOK {
		return nil, errors.New("no editor")
	}
	return exec.Command("true"), nil
}

func newConflictHarness(t *testing.T, w, h int) (*harness, *fakeConflicts) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	cfg := config.Defaults()
	cfg.UI.Animations = "off"
	be := newFakeConflicts()
	wg := &sync.WaitGroup{}
	app := New(context.Background(), Options{Backend: be, Config: cfg, Archive: "/archive",
		Terminal: platform.TerminalInfo{IsTTY: true, Term: "xterm-256color"}}, wg)
	app.env.Now = func() time.Time { return now }
	hs := &harness{t: t, app: app, be: be.fakeBackend, wg: wg}
	hs.send(tea.WindowSizeMsg{Width: w, Height: h})
	hs.init()
	return hs, be
}

func (h *harness) conflicts() *ConflictsScreen {
	return h.app.screens[ScreenConflicts].(*ConflictsScreen)
}

func TestConflictScreenOnlyWithBackend(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	if _, ok := h.app.screens[ScreenConflicts]; ok {
		t.Fatal("a backend without conflict support must not get the screen")
	}
	h2, _ := newConflictHarness(t, 120, 24)
	if _, ok := h2.app.screens[ScreenConflicts]; !ok {
		t.Fatal("conflict screen not registered")
	}
}

func TestGoldenConflicts(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 24}, {"compact", 80, 24}} {
		t.Run(size.name, func(t *testing.T) {
			h, _ := newConflictHarness(t, size.w, size.h)
			h.send(NavigateMsg{To: ScreenConflicts})
			golden(t, "conflicts_list_"+size.name, h.view())
			h.key("enter")
			golden(t, "conflicts_session_"+size.name, h.view())
		})
	}
}

func TestConflictSessionFlow(t *testing.T) {
	h, be := newConflictHarness(t, 120, 30)
	h.send(NavigateMsg{To: ScreenConflicts})
	h.key("enter")
	c := h.conflicts()
	if c.st == nil || c.st.Repo.ID != "2" {
		t.Fatalf("enter should open the session: %+v", be.calls)
	}
	v := h.view()
	for _, want := range []string{"upstream", "mine", "Hunk 1/2"} {
		if !strings.Contains(v, want) {
			t.Fatalf("session view lacks %q:\n%s", want, v)
		}
	}
	// Finishing too early is refused.
	h.key("f")
	if c.err == nil || slices.Contains(be.calls, "finish") {
		t.Fatal("finish with conflicts left must be refused")
	}
	h.key("right")
	if c.hunk != 1 {
		t.Fatalf("right: hunk=%d", c.hunk)
	}
	h.key("t") // second hunk: theirs
	h.key("m") // first hunk (cursor clamps back): mine
	if !c.st.Files[0].Resolved || c.file != 1 {
		t.Fatalf("main.go should be resolved and the cursor on logo.png: file=%d %+v", c.file, c.st.Files)
	}
	if !strings.Contains(h.view(), "binary") {
		t.Fatalf("binary file should say to pick a whole side:\n%s", h.view())
	}
	h.key("T") // whole file
	want := []string{"open 2", "resolve main.go 1 theirs", "resolve main.go 0 mine", "resolve logo.png -1 theirs"}
	if strings.Join(be.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", be.calls, want)
	}
	h.key("f")
	if !slices.Contains(be.calls, "finish") || c.st != nil || len(c.list) != 0 {
		t.Fatalf("finish: calls=%q st=%v list=%v", be.calls, c.st, c.list)
	}
	if !strings.Contains(h.view(), "Finished") && c.note == "" {
		t.Fatal("finish should leave a note")
	}
}

func TestConflictAbortAsksFirst(t *testing.T) {
	h, be := newConflictHarness(t, 120, 30)
	h.send(NavigateMsg{To: ScreenConflicts})
	h.key("enter", "a")
	if h.app.dialog == nil {
		t.Fatal("abort should ask")
	}
	h.key("esc") // keep resolving
	if slices.Contains(be.calls, "abort") || h.conflicts().st == nil {
		t.Fatal("cancel must keep the session")
	}
	h.key("a", "left", "enter")
	if !slices.Contains(be.calls, "abort") || h.conflicts().st != nil {
		t.Fatalf("abort: %q", be.calls)
	}
}

func TestConflictLeaveDismissAndEditor(t *testing.T) {
	h, be := newConflictHarness(t, 120, 30)
	h.send(NavigateMsg{To: ScreenConflicts})
	h.key("enter", "e")
	c := h.conflicts()
	if c.err == nil {
		t.Fatal("a missing editor should show an error")
	}
	be.editorOK = true
	cmd := c.key("e", h.app.env)
	if cmd == nil {
		t.Fatal("e should hand the terminal to the editor")
	}
	h.send(editorDoneMsg{path: "main.go"})
	if !slices.Contains(be.calls, "mark main.go") || !c.st.Files[0].Resolved {
		t.Fatalf("after the editor the file is marked resolved: %q", be.calls)
	}
	h.key("esc")
	if c.st != nil || be.st == nil {
		t.Fatal("esc leaves the session open in the repo")
	}
	h.key("x")
	if h.app.dialog == nil {
		t.Fatal("dismiss should ask")
	}
	h.key("left", "enter")
	if !slices.Contains(be.calls, "dismiss") || len(c.list) != 0 {
		t.Fatalf("dismiss: %q", be.calls)
	}
}

func TestConflictClicks(t *testing.T) {
	h, be := newConflictHarness(t, 120, 30)
	h.send(NavigateMsg{To: ScreenConflicts})
	x, y := h.findZone("cf:key:enter")
	h.click(x, y, false)
	if h.conflicts().st == nil {
		t.Fatal("clicking Resolve should open the session")
	}
	x, y = h.findZone("cf:file:1")
	h.click(x, y, false)
	if h.conflicts().file != 1 {
		t.Fatal("clicking a file selects it")
	}
	x, y = h.findZone("cf:key:m")
	h.click(x, y, false)
	if !slices.Contains(be.calls, "resolve logo.png -1 mine") {
		t.Fatalf("clicking Keep mine: %q", be.calls)
	}
}

func TestSyncSummaryOffersResolve(t *testing.T) {
	h, _ := newConflictHarness(t, 100, 30)
	h.key("S")
	h.wg.Wait()
	if !strings.Contains(h.view(), "Resolve conflicts") {
		t.Fatalf("sync summary should offer to resolve:\n%s", h.view())
	}
	h.key("r")
	if h.app.active != ScreenConflicts {
		t.Fatalf("r: active = %s", h.app.active)
	}
	// Without conflict support there is no such button.
	h2 := newHarness(t, 100, 30, "off")
	h2.be.repos[1].Status = syncer.StatusConflict
	h2.init()
	h2.key("S")
	h2.wg.Wait()
	if strings.Contains(h2.view(), "Resolve conflicts") {
		t.Fatal("no resolve button without a conflict backend")
	}
}
