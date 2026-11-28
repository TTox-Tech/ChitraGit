package theme

import (
	"bytes"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Highlight colours source code for a terminal, picking the language from
// the file name or the text. Without colour it returns the text as it is.
func Highlight(name, text string) string {
	if lipgloss.ColorProfile() == termenv.Ascii || len(text) > 1<<20 {
		return text
	}
	format := "terminal256"
	if lipgloss.ColorProfile() == termenv.TrueColor {
		format = "terminal16m"
	}
	var b bytes.Buffer
	if err := quick.Highlight(&b, text, lexerName(name, text), format, "github-dark"); err != nil {
		return text
	}
	return b.String()
}

// lexerName picks a highlighter by file name, then by content.
func lexerName(name, text string) string {
	if l := lexers.Match(name); l != nil {
		return l.Config().Name
	}
	if l := lexers.Analyse(text); l != nil {
		return l.Config().Name
	}
	return "plaintext"
}
