package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/TTox-Tech/chitragit/internal/syncer"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
)

// Glyphs are the symbols the TUI draws with. Legacy consoles and dumb
// terminals get plain ASCII (PRD F18 "graceful degradation").
type Glyphs struct {
	Rule, Cursor, Mark, Fav, Bullet, Ellipsis, Sep string
	OK, Unchanged, Skip, Fail, Rescued, Running    string
	BarFull, BarEmpty, Pen                         string
	Border                                         lipgloss.Border
}

var (
	unicodeGlyphs = Glyphs{
		Rule: "─", Cursor: "▌", Mark: "●", Fav: "★", Bullet: "▸", Ellipsis: "…", Sep: " · ",
		OK: "✓", Unchanged: "·", Skip: "!", Fail: "✗", Rescued: "★", Running: "▸",
		BarFull: "━", BarEmpty: "─", Pen: "✎",
		Border: lipgloss.RoundedBorder(),
	}
	asciiGlyphs = Glyphs{
		Rule: "-", Cursor: ">", Mark: "*", Fav: "*", Bullet: ">", Ellipsis: "~", Sep: " | ",
		OK: "+", Unchanged: ".", Skip: "!", Fail: "x", Rescued: "*", Running: ">",
		BarFull: "=", BarEmpty: "-", Pen: ">",
		Border: lipgloss.Border{Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"},
	}
)

// Styles are the TUI's lipgloss styles, built from a theme palette.
type Styles struct {
	Palette theme.Palette
	G       Glyphs

	Brand, Title, Text, Label, Muted, Hint, Accent lipgloss.Style
	OK, Warn, Fail, Rule                           lipgloss.Style
	Tab, TabActive, Key                            lipgloss.Style
	Cursor, Selected, Marked, Header               lipgloss.Style
	Button, ButtonFocus                            lipgloss.Style

	// bg is the escape sequence that paints the page background, or ""
	// when the theme keeps the terminal's own background.
	bg string
}

// NewStyles builds styles for a palette.
func NewStyles(p theme.Palette, ascii bool) *Styles {
	n := lipgloss.NewStyle()
	s := &Styles{
		Palette:     p,
		G:           unicodeGlyphs,
		Brand:       n.Foreground(p.Primary).Bold(true),
		Title:       n.Foreground(p.Primary).Bold(true),
		Text:        n.Foreground(p.Text),
		Label:       n.Foreground(p.Text).Bold(true),
		Muted:       n.Foreground(p.Muted),
		Hint:        n.Foreground(p.Highlight),
		Accent:      n.Foreground(p.Primary),
		OK:          n.Foreground(p.Success),
		Warn:        n.Foreground(p.Warning),
		Fail:        n.Foreground(p.Error).Bold(true),
		Rule:        n.Foreground(p.Rule),
		Tab:         n.Foreground(p.Muted),
		TabActive:   n.Foreground(p.Primary).Bold(true).Underline(true),
		Key:         n.Foreground(p.Highlight).Bold(true),
		Cursor:      n.Foreground(p.Primary).Bold(true),
		Selected:    n.Foreground(p.Primary).Bold(true),
		Marked:      n.Foreground(p.Highlight),
		Header:      n.Foreground(p.Muted).Bold(true),
		Button:      n.Foreground(p.Text),
		ButtonFocus: n.Foreground(p.Primary).Bold(true),
	}
	if ascii {
		s.G = asciiGlyphs
	}
	if p.Background != nil {
		// Render a marker with only the background set and keep what comes
		// before it: that is the escape sequence for this colour profile.
		out := n.Background(p.Background).Render("\x00")
		if i := strings.IndexByte(out, 0); i > 0 {
			s.bg = out[:i]
		}
	}
	return s
}

// paint gives every cell of a finished frame the page background. Styled
// segments end with a full reset, which would also clear the background,
// so the background is switched back on after each reset.
func (s *Styles) paint(frame string) string {
	if s.bg == "" {
		return frame
	}
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		lines[i] = s.bg + strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+s.bg) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// rule draws a ledger line w cells wide.
func (s *Styles) rule(w int) string {
	return s.Rule.Render(strings.Repeat(s.G.Rule, max(w, 0)))
}

// button renders a clickable label such as "[ Pause ]".
func (s *Styles) button(label string, focus bool) string {
	if focus {
		return s.ButtonFocus.Render("[ " + label + " ]")
	}
	return s.Button.Render("[ ") + s.Accent.Render(label) + s.Button.Render(" ]")
}

// choice renders one option of a row, with a mark on the chosen one so
// the choice shows without colour too: "[ ● Daily ]".
func (s *Styles) choice(label string, chosen bool) string {
	if chosen {
		return s.ButtonFocus.Render("[ " + s.G.Mark + " " + label + " ]")
	}
	return s.button(label, false)
}

// outcome returns the ledger glyph and style for a sync outcome.
func (s *Styles) outcome(o string) (string, lipgloss.Style) {
	switch o {
	case syncer.Updated:
		return s.G.OK, s.OK
	case syncer.Skipped:
		return s.G.Skip, s.Warn
	case syncer.Failed:
		return s.G.Fail, s.Fail
	}
	return s.G.Unchanged, s.Muted
}
