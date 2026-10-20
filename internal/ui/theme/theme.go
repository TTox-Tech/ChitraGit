// Package theme defines ChitraGit's colour palettes. The default "ledger"
// theme is inspired by a traditional bahi-khata (account book): ink-brown
// pages and parchment text, with kesariya (saffron) as the single accent.
// Status colours are muted so the accent stays the one thing that stands
// out. The README artwork in docs/assets uses the same hex values; see
// docs/brand.md before changing any of them.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the set of semantic colours every UI element uses.
type Palette struct {
	Name       string
	Background lipgloss.TerminalColor // page: ink (dark) or parchment (light); nil = leave the terminal's own
	Primary    lipgloss.TerminalColor // kesariya, the one accent: actions, headings, focus
	Highlight  lipgloss.TerminalColor // brass: hints and the Rescued badge
	Success    lipgloss.TerminalColor // sage
	Warning    lipgloss.TerminalColor // amber
	Error      lipgloss.TerminalColor // sindoor
	Text       lipgloss.TerminalColor // parchment (dark) or ink (light)
	Muted      lipgloss.TerminalColor // secondary text
	Rule       lipgloss.TerminalColor // ledger lines and borders (decorative)
}

// Hex values for the two ledger variants. Every foreground colour keeps a
// contrast ratio of at least 4.5:1 (WCAG AA for normal text) against its
// background; theme_test.go checks this.
var (
	ledgerDark = Palette{
		Background: lipgloss.Color("#16120E"),
		Primary:    lipgloss.Color("#E58A45"),
		Highlight:  lipgloss.Color("#CDB27A"),
		Success:    lipgloss.Color("#8DB58A"),
		Warning:    lipgloss.Color("#DDA654"),
		Error:      lipgloss.Color("#E07A66"),
		Text:       lipgloss.Color("#EFE6D6"),
		Muted:      lipgloss.Color("#A39684"),
		Rule:       lipgloss.Color("#3D342A"),
	}
	ledgerLight = Palette{
		Background: lipgloss.Color("#FBF8F2"),
		Primary:    lipgloss.Color("#B0531A"),
		Highlight:  lipgloss.Color("#76612F"),
		Success:    lipgloss.Color("#46704A"),
		Warning:    lipgloss.Color("#8C5B0E"),
		Error:      lipgloss.Color("#A63D30"),
		Text:       lipgloss.Color("#2B231B"),
		Muted:      lipgloss.Color("#6E6355"),
		Rule:       lipgloss.Color("#E2D8C6"),
	}
)

// adaptive picks the light or dark value at render time, from the
// terminal's own background colour.
func adaptive(light, dark lipgloss.TerminalColor) lipgloss.TerminalColor {
	return lipgloss.AdaptiveColor{Light: string(light.(lipgloss.Color)), Dark: string(dark.(lipgloss.Color))}
}

var palettes = map[string]Palette{
	"ledger": {
		Name: "ledger",
		// Background stays nil: the adaptive theme follows the terminal's
		// own background instead of painting over it.
		Primary:   adaptive(ledgerLight.Primary, ledgerDark.Primary),
		Highlight: adaptive(ledgerLight.Highlight, ledgerDark.Highlight),
		Success:   adaptive(ledgerLight.Success, ledgerDark.Success),
		Warning:   adaptive(ledgerLight.Warning, ledgerDark.Warning),
		Error:     adaptive(ledgerLight.Error, ledgerDark.Error),
		Text:      adaptive(ledgerLight.Text, ledgerDark.Text),
		Muted:     adaptive(ledgerLight.Muted, ledgerDark.Muted),
		Rule:      adaptive(ledgerLight.Rule, ledgerDark.Rule),
	},
	"ledger-light": withName(ledgerLight, "ledger-light"),
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

func withName(p Palette, name string) Palette {
	p.Name = name
	return p
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
