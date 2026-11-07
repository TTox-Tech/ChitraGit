// Package theme defines ChitraGit's colour palettes. The default "ledger"
// theme is inspired by a traditional bahi-khata (account book) written in
// blue ink: syahi (ink-black) pages, raakh (ash-grey) rules and light text,
// with neel (blue) as the single accent.
// Status colours are muted so the accent stays the one thing that stands
// out. The README artwork in docs/assets uses the same hex values; see
// docs/brand.md before changing any of them.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the set of semantic colours every UI element uses.
type Palette struct {
	Name       string
	Background lipgloss.TerminalColor // page: ink-black (dark) or paper-white (light); nil = leave the terminal's own
	Primary    lipgloss.TerminalColor // neel (blue), the one accent: actions, headings, focus
	Highlight  lipgloss.TerminalColor // aasmani (sky blue): hints and the Rescued badge
	Success    lipgloss.TerminalColor // sage
	Warning    lipgloss.TerminalColor // amber
	Error      lipgloss.TerminalColor // sindoor
	Text       lipgloss.TerminalColor // light grey (dark) or ink-black (light)
	Muted      lipgloss.TerminalColor // secondary text
	Rule       lipgloss.TerminalColor // ledger lines and borders (decorative)
}

// Hex values for the two ledger variants. Every foreground colour keeps a
// contrast ratio of at least 4.5:1 (WCAG AA for normal text) against its
// background; theme_test.go checks this.
var (
	ledgerDark = Palette{
		Background: lipgloss.Color("#0A0C10"),
		Primary:    lipgloss.Color("#3B82F6"),
		Highlight:  lipgloss.Color("#7DD3FC"),
		Success:    lipgloss.Color("#72C795"),
		Warning:    lipgloss.Color("#E3B341"),
		Error:      lipgloss.Color("#F47067"),
		Text:       lipgloss.Color("#E6EDF3"),
		Muted:      lipgloss.Color("#8B949E"),
		Rule:       lipgloss.Color("#2B313A"),
	}
	ledgerLight = Palette{
		Background: lipgloss.Color("#F6F8FA"),
		Primary:    lipgloss.Color("#1D4ED8"),
		Highlight:  lipgloss.Color("#0E6A8C"),
		Success:    lipgloss.Color("#1A7F37"),
		Warning:    lipgloss.Color("#9A6700"),
		Error:      lipgloss.Color("#CF222E"),
		Text:       lipgloss.Color("#0D1117"),
		Muted:      lipgloss.Color("#57606A"),
		Rule:       lipgloss.Color("#D0D7DE"),
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
		Primary:   lipgloss.ANSIColor(14),
		Highlight: lipgloss.ANSIColor(12),
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
