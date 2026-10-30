package tui

import (
	"context"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Screens added by M4 (PRD F8–F10).
const (
	ScreenActivity ScreenID = "activity"
	ScreenHealth   ScreenID = "health"
)

// Insights is what the detail, activity and health screens read, plus the
// small writes they offer (favourites, tags). A Backend that also
// implements it gets those screens; none of it touches a working tree.
type Insights interface {
	RepoInfo(ctx context.Context, id string) (*store.RepoInfo, error)
	// RepoGit reads commits, refs and the README. It may fail for a broken
	// or missing repo while RepoInfo still works.
	RepoGit(ctx context.Context, r core.Repo) (*RepoGit, error)
	RepoHistory(ctx context.Context, id string) ([]store.RepoRun, error)
	Activity(ctx context.Context, f store.ActivityFilter) ([]store.Activity, error)
	Health(ctx context.Context) (*store.Health, error)
	// CardRepos lists the repo IDs behind one health card.
	CardRepos(ctx context.Context, card string) ([]string, error)
	// MeasureSizes measures every repo on disk for the disk usage card.
	MeasureSizes(ctx context.Context) error
	SetFavourite(ctx context.Context, ids []string, fav bool) error
	AddTags(ctx context.Context, ids, tags []string) error
	RemoveTags(ctx context.Context, ids, tags []string) error
	RepoTags(ctx context.Context) (map[string][]string, error)
	// Open opens a folder or a URL with the system's default app.
	Open(target string) error
	// RepoDir is the repo's folder on disk.
	RepoDir(r core.Repo) string
}

// RepoGit is what git says about a repo.
type RepoGit struct {
	Commits []engine.Commit
	Refs    []engine.Ref
	Readme  *engine.Readme
}

// insights returns the backend's Insights, or nil.
func (e *Env) insights() Insights {
	in, _ := e.Backend.(Insights)
	return in
}

// BrowserIDs, sent as a Navigate payload to the browser, shows only these
// repos, e.g. the ones behind a health card or carrying a tag.
type BrowserIDs struct {
	Label string
	IDs   []string
}

// OpenDetail, sent as a Navigate payload to the detail page, opens a repo
// by ID at a given tab (see detailTabs).
type OpenDetail struct {
	ID  string
	Tab string
}

// ackMsg reports a finished write, so the screen that asked can show it.
type ackMsg struct {
	text string
	err  error
}

// changed runs a write in the background, then reloads every screen.
func changed(text string, write func() error) tea.Cmd {
	return func() tea.Msg {
		if err := write(); err != nil {
			return ackMsg{err: err}
		}
		return tea.BatchMsg{
			func() tea.Msg { return ReposChangedMsg{} },
			func() tea.Msg { return ackMsg{text: text} },
		}
	}
}

// lineInput is a one-line text field drawn by the screen that owns it.
// While active, the screen captures every key.
type lineInput struct {
	active bool
	label  string
	value  string
	// done runs with the typed text on enter; esc cancels.
	done func(string) tea.Cmd
}

func (in *lineInput) start(label string, done func(string) tea.Cmd) {
	*in = lineInput{active: true, label: label, done: done}
}

// key edits the field. It returns the command from done on enter.
func (in *lineInput) key(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEnter:
		in.active = false
		if v := strings.TrimSpace(in.value); v != "" && in.done != nil {
			return in.done(v)
		}
	case tea.KeyEsc:
		in.active = false
	case tea.KeyBackspace:
		if in.value != "" {
			_, n := utf8.DecodeLastRuneInString(in.value)
			in.value = in.value[:len(in.value)-n]
		}
	case tea.KeyRunes, tea.KeySpace:
		in.value += string(k.Runes)
	}
	return nil
}

func (in *lineInput) view(s *Styles, w int) string {
	line := " " + s.Accent.Render(in.label+": ") + s.Text.Render(in.value) + s.Accent.Render("▏")
	return fit(line, w, s.G.Ellipsis)
}

// splitTags turns "work, fonts" into ["work" "fonts"].
func splitTags(v string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t = store.NormalizeTag(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
