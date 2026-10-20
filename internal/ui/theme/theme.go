// Package theme defines ChitraGit's colour palettes. The default "ledger"
// theme is inspired by a traditional bahi-khata: ink-brown pages,
// parchment text, saffron and gold accents, sindoor red for errors.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the set of semantic colours every UI element uses.
type Palette struct {
	Name      string
	Primary   lipgloss.TerminalColor // kesariya: actions, headings
	Highlight lipgloss.TerminalColor // haldi gold: highlights, Rescued badge
	Success   lipgloss.TerminalColor
	Warning   lipgloss.TerminalColor
	Error     lipgloss.TerminalColor // sindoor
	Text      lipgloss.TerminalColor // parchment
	Muted     lipgloss.TerminalColor
	Rule      lipgloss.TerminalColor // ledger lines
}

var palettes = map[string]Palette{
	"ledger": {
		Name:      "ledger",
		Primary:   lipgloss.AdaptiveColor{Light: "#C2571A", Dark: "#FF9933"},
		Highlight: lipgloss.AdaptiveColor{Light: "#9A7B0A", Dark: "#E6B422"},
		Success:   lipgloss.AdaptiveColor{Light: "#3B7A3B", Dark: "#8DBF6A"},
		Warning:   lipgloss.AdaptiveColor{Light: "#B26B00", Dark: "#F2B45A"},
		Error:     lipgloss.AdaptiveColor{Light: "#A11D1D", Dark: "#E5484D"},
		Text:      lipgloss.AdaptiveColor{Light: "#3B2A1A", Dark: "#F5E6C8"},
		Muted:     lipgloss.AdaptiveColor{Light: "#7A6650", Dark: "#A89478"},
		Rule:      lipgloss.AdaptiveColor{Light: "#C9B28F", Dark: "#5C4630"},
	},
	"ledger-light": {
		Name:      "ledger-light",
		Primary:   lipgloss.Color("#C2571A"),
		Highlight: lipgloss.Color("#9A7B0A"),
		Success:   lipgloss.Color("#3B7A3B"),
		Warning:   lipgloss.Color("#B26B00"),
		Error:     lipgloss.Color("#A11D1D"),
		Text:      lipgloss.Color("#3B2A1A"),
		Muted:     lipgloss.Color("#7A6650"),
		Rule:      lipgloss.Color("#C9B28F"),
	},
	"high-contrast": {
		Name:      "high-contrast",
		Primary:   lipgloss.ANSIColor(11),
		Highlight: lipgloss.ANSIColor(14),
		Success:   lipgloss.ANSIColor(10),
		Warning:   lipgloss.ANSIColor(11),
		Error:     lipgloss.ANSIColor(9),
		Text:      lipgloss.ANSIColor(15),
		Muted:     lipgloss.ANSIColor(7),
		Rule:      lipgloss.ANSIColor(15),
	},
}

// Get returns the named palette, falling back to "ledger".
func Get(name string) Palette {
	if p, ok := palettes[name]; ok {
		return p
	}
	return palettes["ledger"]
}

// Styles are ready-made lipgloss styles for CLI output.
type Styles struct {
	Title, Label, Text, Muted, OK, Warn, Fail, Accent lipgloss.Style
}

// NewStyles builds styles from a palette.
func NewStyles(p Palette) Styles {
	s := lipgloss.NewStyle()
	return Styles{
		Title:  s.Foreground(p.Primary).Bold(true),
		Label:  s.Foreground(p.Text).Bold(true),
		Text:   s.Foreground(p.Text),
		Muted:  s.Foreground(p.Muted),
		OK:     s.Foreground(p.Success),
		Warn:   s.Foreground(p.Warning),
		Fail:   s.Foreground(p.Error).Bold(true),
		Accent: s.Foreground(p.Highlight),
	}
}
