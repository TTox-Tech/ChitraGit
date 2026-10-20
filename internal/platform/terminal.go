package platform

import (
	"os"

	"golang.org/x/term"
)

// TerminalInfo describes the capabilities of the attached terminal.
type TerminalInfo struct {
	IsTTY   bool
	Width   int
	Height  int
	SSH     bool // running inside an SSH session
	NoColor bool // NO_COLOR is set or TERM=dumb
	Term    string
}

// DetectTerminal inspects stdout and the environment.
func DetectTerminal() TerminalInfo {
	fd := int(os.Stdout.Fd())
	t := TerminalInfo{
		IsTTY: term.IsTerminal(fd),
		Term:  os.Getenv("TERM"),
		SSH:   os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "",
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	t.NoColor = noColor || t.Term == "dumb"
	if t.IsTTY {
		if w, h, err := term.GetSize(fd); err == nil {
			t.Width, t.Height = w, h
		}
	}
	return t
}
