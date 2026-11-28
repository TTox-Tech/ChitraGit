package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestHighlight(t *testing.T) {
	src := "package main\n\nfunc main() {}\n"
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := Highlight("main.go", src); got != src {
		t.Errorf("no colour changed the text: %q", got)
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
	got := Highlight("main.go", src)
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "func") {
		t.Errorf("not coloured: %q", got)
	}
	if plain := Highlight("notes", "just words\n"); !strings.Contains(plain, "just words") {
		t.Errorf("plain text lost: %q", plain)
	}
}
