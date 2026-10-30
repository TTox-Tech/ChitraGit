package tui

import (
	"context"
	"slices"
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
	"github.com/TTox-Tech/chitragit/internal/store"
)

// insightBackend is fakeBackend plus Insights, with fixed data.
type insightBackend struct {
	*fakeBackend
	tags   map[string][]string
	opened []string
}

func (f *insightBackend) find(id string) *core.Repo {
	for i := range f.repos {
		if f.repos[i].ID == id {
			return &f.repos[i]
		}
	}
	return nil
}

func (f *insightBackend) RepoInfo(_ context.Context, id string) (*store.RepoInfo, error) {
	r := f.find(id)
	if r == nil {
		return nil, store.ErrNoRepo
	}
	tags := f.tags[id]
	if tags == nil {
		tags = []string{}
	}
	return &store.RepoInfo{Repo: *r, Remotes: map[string]string{"origin": "https://" + r.Host + "/" + r.Owner + "/" + r.Name + ".git"},
		Tags: tags, SizeBytes: 3 << 20, FirstSeenAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano),
		LastUpstreamActivityAt: now.Add(-5 * time.Hour).Format(time.RFC3339Nano)}, nil
}

func (f *insightBackend) RepoGit(context.Context, core.Repo) (*RepoGit, error) {
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	return &RepoGit{
		Commits: []engine.Commit{
			{Graph: "*", SHA: "a1b2c3d4e5f6", Author: "Linus", Date: at(2 * time.Hour), Subject: "Merge branch 'fixes'", Refs: []string{"HEAD", "master", "origin/master"}},
			{Graph: "|\\"},
			{Graph: "| *", SHA: "b2c3d4e5f6a1", Author: "Greg", Date: at(26 * time.Hour), Subject: "Fix a typo"},
			{Graph: "* |", SHA: "c3d4e5f6a1b2", Author: "Linus", Date: at(30 * time.Hour), Subject: "Linux 6.18-rc3", Refs: []string{"v6.18-rc3"}},
			{Graph: "|/"},
			{Graph: "*", SHA: "d4e5f6a1b2c3", Author: "Linus", Date: at(80 * time.Hour), Subject: "Start of the cycle"},
		},
		Refs: []engine.Ref{
			{Kind: engine.RefBranch, Name: "master", SHA: "a1b2c3d4e5f6", Date: at(2 * time.Hour), Upstream: "origin/master", Current: true},
			{Kind: engine.RefRemote, Name: "origin/master", SHA: "a1b2c3d4e5f6", Date: at(2 * time.Hour)},
			{Kind: engine.RefTag, Name: "v6.18-rc3", SHA: "c3d4e5f6a1b2", Date: at(30 * time.Hour)},
			{Kind: engine.RefArchived, Name: "origin/old-feature", SHA: "d4e5f6a1b2c3", Date: at(80 * time.Hour)},
		},
		Readme: &engine.Readme{Name: "README.md", Markdown: true, Text: "# Linux kernel\n\nThere are several guides for kernel developers.\n\n- Read the docs\n- Be nice\n"},
	}, nil
}

func (f *insightBackend) RepoHistory(_ context.Context, id string) ([]store.RepoRun, error) {
	return []store.RepoRun{{RunID: 7, Outcome: "updated", FinishedAt: now.Add(-3 * time.Hour).Format(time.RFC3339Nano)}}, nil
}

func (f *insightBackend) feed() []store.Activity {
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	return []store.Activity{
		{Type: "rescued", At: at(time.Hour), RepoID: "3", Path: "gitlab/some/tool", Message: "upstream returned 404 (2nd confirmation)"},
		{Type: store.ActivityRefs, At: at(3 * time.Hour), RepoID: "1", Path: "torvalds/linux", Changes: []core.RefChange{
			{Ref: "refs/remotes/origin/master", OldSHA: "1", NewSHA: "2", Commits: 142, Kind: "update"},
			{Ref: "refs/tags/v6.18-rc3", NewSHA: "2", Kind: "create"}}},
		{Type: store.ActivityRefs, At: at(27 * time.Hour), RepoID: "2", Path: "charmbracelet/bubbletea", Changes: []core.RefChange{
			{Ref: "refs/remotes/origin/main", OldSHA: "1", NewSHA: "2", Commits: 3, Kind: "update"}}},
	}
}

func (f *insightBackend) Activity(_ context.Context, flt store.ActivityFilter) ([]store.Activity, error) {
	var out []store.Activity
	for _, a := range f.feed() {
		if flt.RepoID == "" || a.RepoID == flt.RepoID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *insightBackend) Health(context.Context) (*store.Health, error) {
	return &store.Health{Total: 4,
		Cards:     map[string]int{store.CardFailing: 1, store.CardDiverged: 1, store.CardMissing: 1, store.CardFavourites: 1, store.CardNeverSync: 2},
		Failing:   []store.RepoNote{{ID: "3", Path: "gitlab/some/tool", Reason: "could not reach the host"}},
		DiskTotal: 5 << 30, Measured: 4,
		Largest: []store.RepoNote{{ID: "1", Path: "torvalds/linux", SizeBytes: 4 << 30}, {ID: "2", Path: "charmbracelet/bubbletea", SizeBytes: 12 << 20}},
		Runs: []store.RunSummary{{ID: 7, Kind: "sync", State: "finished", StartedAt: now.Add(-3 * time.Hour).Format(time.RFC3339Nano),
			Total: 4, Updated: 1, Unchanged: 2, Failed: 1}},
	}, nil
}

func (f *insightBackend) CardRepos(_ context.Context, card string) ([]string, error) {
	switch card {
	case store.CardFailing:
		return []string{"3"}, nil
	case store.CardNeverSync:
		return []string{"3", "5"}, nil
	}
	return nil, nil
}

func (f *insightBackend) MeasureSizes(context.Context) error { return nil }

func (f *insightBackend) SetFavourite(_ context.Context, ids []string, fav bool) error {
	for _, id := range ids {
		f.find(id).Favourite = fav
	}
	return nil
}

func (f *insightBackend) AddTags(_ context.Context, ids, tags []string) error {
	for _, id := range ids {
		f.tags[id] = append(f.tags[id], tags...)
	}
	return nil
}

func (f *insightBackend) RemoveTags(_ context.Context, ids, tags []string) error {
	for _, id := range ids {
		f.tags[id] = slices.DeleteFunc(f.tags[id], func(t string) bool { return slices.Contains(tags, t) })
	}
	return nil
}

func (f *insightBackend) RepoTags(context.Context) (map[string][]string, error) { return f.tags, nil }
func (f *insightBackend) Open(target string) error                              { f.opened = append(f.opened, target); return nil }
func (f *insightBackend) RepoDir(r core.Repo) string                            { return "/archive/" + r.Path }

func newInsightHarness(t *testing.T, w, h int) (*harness, *insightBackend) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	cfg := config.Defaults()
	cfg.UI.Animations = "off"
	fb := &fakeBackend{repos: sampleRepos()}
	be := &insightBackend{fakeBackend: fb, tags: map[string][]string{"1": {"kernel", "c"}, "2": {"go"}}}
	wg := &sync.WaitGroup{}
	app := New(context.Background(), Options{Backend: be, Config: cfg, Archive: "/archive",
		Terminal: platform.TerminalInfo{IsTTY: true, Term: "xterm-256color"}}, wg)
	app.env.Now = func() time.Time { return now }
	hs := &harness{t: t, app: app, be: fb, wg: wg}
	hs.send(tea.WindowSizeMsg{Width: w, Height: h})
	hs.init()
	return hs, be
}

func TestInsightScreensRegistered(t *testing.T) {
	h := newHarness(t, 120, 24, "off")
	if h.app.env.Registered(ScreenActivity) || h.app.env.Registered(ScreenHealth) {
		t.Error("activity and health need a backend with Insights")
	}
	h, _ = newInsightHarness(t, 120, 24)
	if !h.app.env.Registered(ScreenActivity) || !h.app.env.Registered(ScreenHealth) {
		t.Error("activity and health not registered")
	}
}

func TestGoldenDetailTabs(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 30}, {"compact", 80, 24}} {
		h, _ := newInsightHarness(t, size.w, size.h)
		h.key("enter") // first row: bubbletea... sorted by name
		if h.app.active != ScreenDetail {
			t.Fatalf("active = %s", h.app.active)
		}
		for _, tab := range detailTabs {
			golden(t, "detail_"+tab+"_"+size.name, h.view())
			h.key("right")
		}
	}
}

func TestDetailActions(t *testing.T) {
	h, be := newInsightHarness(t, 120, 30)
	h.app.update(NavigateMsg{To: ScreenDetail, Payload: OpenDetail{ID: "1", Tab: "info"}})
	h.run(Navigate(ScreenDetail, OpenDetail{ID: "1", Tab: "info"}))
	d := h.app.screens[ScreenDetail].(*Detail)
	if d.repo == nil || d.repo.ID != "1" || detailTabs[d.tab] != "info" {
		t.Fatalf("open by id: repo=%v tab=%d", d.repo, d.tab)
	}
	h.key("*")
	if !be.find("1").Favourite || !d.repo.Favourite {
		t.Error("favourite not toggled and reloaded")
	}
	h.key("t")
	if !d.Capturing() {
		t.Fatal("tag prompt should capture keys")
	}
	h.key("q", "u", "e", "u", "e", ",", " ", "W", "o", "r", "k", "enter")
	if h.app.quitting || !slices.Contains(be.tags["1"], "queue") || !slices.Contains(be.tags["1"], "work") {
		t.Errorf("tags = %v quitting=%v", be.tags["1"], h.app.quitting)
	}
	if !strings.Contains(h.view(), "queue") {
		t.Error("info tab should show the new tag")
	}
	h.key("o", "u")
	if !slices.Equal(be.opened, []string{"/archive/torvalds/linux", "https://github.com/torvalds/linux"}) {
		t.Errorf("opened = %v", be.opened)
	}
	h.key("s")
	h.wg.Wait()
	if h.app.active != ScreenSync || len(be.syncs) != 1 || !slices.Equal(be.syncs[0].IDs, []string{"1"}) {
		t.Errorf("sync now: active=%s syncs=%+v", h.app.active, be.syncs)
	}
}

func TestGoldenActivityAndOpen(t *testing.T) {
	h, _ := newInsightHarness(t, 120, 24)
	h.run(Navigate(ScreenActivity, nil))
	golden(t, "activity_wide", h.view())
	a := h.app.screens[ScreenActivity].(*Activity)
	h.key("down") // skip the "Yesterday" heading: lands on the next entry
	if e := a.all[a.rows[a.cursor].entry]; e.RepoID != "1" {
		t.Fatalf("cursor on %+v", e)
	}
	h.key("f")                    // only new commits & tags
	if n := len(a.rows); n != 4 { // Today, linux, Yesterday, bubbletea
		t.Fatalf("refs filter rows = %d", n)
	}
	h.key("enter")
	d := h.app.screens[ScreenDetail].(*Detail)
	if h.app.active != ScreenDetail || d.repo == nil || d.repo.ID != "1" || detailTabs[d.tab] != "activity" {
		t.Fatalf("enter: active=%s repo=%v tab=%d", h.app.active, d.repo, d.tab)
	}
}

func TestGoldenHealthAndCards(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 30}, {"compact", 80, 24}} {
		h, _ := newInsightHarness(t, size.w, size.h)
		h.run(Navigate(ScreenHealth, nil))
		golden(t, "health_"+size.name, h.view())
	}

	h, _ := newInsightHarness(t, 120, 30)
	h.run(Navigate(ScreenHealth, nil))
	x, y := h.findZone("card:" + store.CardNeverSync)
	h.click(x, y, false)
	b := h.browser()
	if h.app.active != ScreenBrowser || len(b.rows) != 2 {
		t.Fatalf("card click: active=%s rows=%d", h.app.active, len(b.rows))
	}
	if !strings.Contains(h.view(), "[never synced]") {
		t.Error("browser chip should name the card")
	}
	h.key("esc")
	if len(b.rows) != 5 {
		t.Errorf("esc should clear the card filter, rows=%d", len(b.rows))
	}

	h.run(Navigate(ScreenHealth, nil))
	x, y = h.findZone("tag:0") // "#c" and "#go" and "#kernel" have one repo each; sorted by name
	h.click(x, y, false)
	if len(b.rows) != 1 || b.Current().ID != "1" {
		t.Errorf("tag click: rows=%d", len(b.rows))
	}
}

func TestBrowserBulkFavouriteAndTag(t *testing.T) {
	h, be := newInsightHarness(t, 120, 24)
	b := h.browser()
	h.key("a") // mark all five
	if !strings.Contains(h.view(), "favourite") {
		t.Error("footer should offer bulk actions while repos are marked")
	}
	h.key("*")
	for _, r := range be.repos {
		if !r.Favourite {
			t.Fatalf("%s not favourite", r.Path)
		}
	}
	h.key("*") // all are favourites now, so this removes them
	for _, r := range be.repos {
		if r.Favourite {
			t.Fatalf("%s still favourite", r.Path)
		}
	}
	h.key("t", "a", "r", "c", "h", "i", "v", "e", "enter")
	for _, r := range be.repos {
		if !slices.Contains(be.tags[r.ID], "archive") {
			t.Fatalf("%s not tagged: %v", r.Path, be.tags[r.ID])
		}
	}
	if !strings.Contains(h.view(), "Tagged 5 repo(s)") {
		t.Error("browser should confirm the bulk tag")
	}
	h.key("esc")
	h.key("T", "a", "r", "c", "h", "i", "v", "e", "enter") // only the current repo
	if n := 0; true {
		for _, r := range be.repos {
			if slices.Contains(be.tags[r.ID], "archive") {
				n++
			}
		}
		if n != 4 {
			t.Errorf("untag current: %d still tagged", n)
		}
	}
	_ = b
}
