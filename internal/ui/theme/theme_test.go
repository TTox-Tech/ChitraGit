package theme

import (
	"math"
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// luminance is the WCAG relative luminance of a "#RRGGBB" colour.
func luminance(t *testing.T, hex lipgloss.TerminalColor) float64 {
	t.Helper()
	s := string(hex.(lipgloss.Color))
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil || len(s) != 7 {
		t.Fatalf("bad hex colour %q", s)
	}
	lin := func(c uint64) float64 {
		x := float64(c) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(v>>16) + 0.7152*lin(v>>8&0xff) + 0.0722*lin(v&0xff)
}

// TestLedgerContrast keeps every text colour readable: WCAG AA asks for a
// contrast ratio of at least 4.5:1 for normal-size text.
func TestLedgerContrast(t *testing.T) {
	for name, p := range map[string]Palette{"dark": ledgerDark, "light": ledgerLight} {
		bg := luminance(t, p.Background)
		for role, fg := range map[string]lipgloss.TerminalColor{
			"Primary": p.Primary, "Highlight": p.Highlight, "Success": p.Success, "Warning": p.Warning,
			"Error": p.Error, "Text": p.Text, "Muted": p.Muted,
		} {
			l := luminance(t, fg)
			ratio := (math.Max(l, bg) + 0.05) / (math.Min(l, bg) + 0.05)
			if ratio < 4.5 {
				t.Errorf("%s %s: contrast %.2f:1, want at least 4.5:1", name, role, ratio)
			}
		}
	}
}

func TestGetFallsBackToLedger(t *testing.T) {
	if got := Get("no-such-theme").Name; got != "ledger" {
		t.Errorf("Get(unknown).Name = %q, want ledger", got)
	}
	if got := Get("ledger-light").Name; got != "ledger-light" {
		t.Errorf("Get(ledger-light).Name = %q", got)
	}
}
