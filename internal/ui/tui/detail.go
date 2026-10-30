package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// Detail shows one repo's record. It is reached from the browser with
// enter or a double click, and has no tab of its own.
type Detail struct {
	repo *core.Repo
}

func NewDetail() *Detail { return &Detail{} }

func (d *Detail) Title() string     { return "" }
func (d *Detail) Init(*Env) tea.Cmd { return nil }

func (d *Detail) Keys() []KeyHint {
	return []KeyHint{{"esc", i18n.T("tui.key.back")}, {"s", i18n.T("tui.key.sync")}}
}

func (d *Detail) Update(msg tea.Msg, _ *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		if r, ok := msg.Payload.(core.Repo); ok {
			d.repo = &r
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "backspace", "b":
			return Navigate(ScreenBrowser, nil)
		case "s":
			if d.repo != nil {
				return Navigate(ScreenSync, SyncStart{IDs: []string{d.repo.ID}})
			}
		}
	case ZoneClick:
		if msg.ID == "back" {
			return Navigate(ScreenBrowser, nil)
		}
	}
	return nil
}

func (d *Detail) View(f Frame) string {
	s := f.S
	if d.repo == nil {
		return ""
	}
	r := d.repo
	back := s.button(i18n.T("tui.key.back"), false)
	f.Zone("back", 1, 0, width(back), 1)
	lines := []string{" " + back, "", " " + s.Title.Render(r.Path)}
	field := func(key, val string) {
		if val != "" {
			lines = append(lines, " "+s.Muted.Render(fit(i18n.T("tui.detail."+key), 16, s.G.Ellipsis))+s.Text.Render(val))
		}
	}
	lines = append(lines, "")
	field("status", (&Browser{}).badge(s, *r, f.Width-20))
	field("reason", r.StatusReason)
	field("synced", synced(*r, f.Env.Now()))
	field("kind", r.Kind)
	field("provider", r.Provider)
	if r.Owner != "" || r.Name != "" {
		field("upstream", strings.Trim(r.Host+"/"+r.Owner+"/"+r.Name, "/"))
	}
	field("id", r.ID)
	return strings.Join(lines, "\n")
}
