package tui

import (
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/platform"
)

// Level is how much the TUI animates (PRD F18).
type Level int

const (
	LevelOff   Level = iota // nothing moves; spinners are static
	LevelLight              // spinners and bars only
	LevelFull               // logo reveal, diya flame, kalam, page turns, rangoli
)

func (l Level) String() string {
	return [...]string{"off", "light", "full"}[l]
}

// ResolveLevel turns the ui.animations setting into a level. "auto" picks
// Off for dumb or colourless terminals, Light over SSH, and Full otherwise;
// Full may still drop to Light later if frames render slowly.
func ResolveLevel(setting string, t platform.TerminalInfo) (Level, bool) {
	switch setting {
	case "full":
		return LevelFull, false
	case "light":
		return LevelLight, false
	case "off":
		return LevelOff, false
	}
	switch {
	case t.Term == "dumb" || t.NoColor || !t.IsTTY:
		return LevelOff, true
	case t.SSH:
		return LevelLight, true
	}
	return LevelFull, true
}

// tickMsg drives every animation. One clock for the whole app keeps
// screens in step and costs nothing while nothing animates.
type tickMsg struct{}

// Anim is the shared animation clock and frame library.
type Anim struct {
	Level Level
	Tick  int // advances only while something animates

	auto  bool
	ascii bool
	// slow-frame detection for "auto": average render time of recent frames.
	frames int
	total  time.Duration
}

func newAnim(level Level, auto, ascii bool) *Anim {
	return &Anim{Level: level, auto: auto, ascii: ascii}
}

func (a *Anim) interval() time.Duration {
	if a.Level == LevelFull {
		return 90 * time.Millisecond
	}
	return 140 * time.Millisecond
}

// observe records how long a frame took to render. In auto mode, Full
// drops to Light when frames are consistently slower than 60 fps.
func (a *Anim) observe(d time.Duration) {
	if !a.auto || a.Level != LevelFull {
		return
	}
	a.frames++
	a.total += d
	if a.frames < 30 {
		return
	}
	if a.total/time.Duration(a.frames) > 16*time.Millisecond {
		a.Level = LevelLight
	}
	a.frames, a.total = 0, 0
}

var (
	// The diya: a flame that flickers over a lamp.
	diyaFlame      = []string{"⁎", "✱", "✲", "✳", "✲", "✱"}
	diyaFlameASCII = []string{".", "*", "+", "*"}
	lightSpin      = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	lightSpinASCII = []string{"|", "/", "-", "\\"}
)

// Spinner is a two-cell busy indicator.
func (a *Anim) Spinner() string {
	switch {
	case a.Level == LevelFull && a.ascii:
		return diyaFlameASCII[a.Tick%len(diyaFlameASCII)] + "u"
	case a.Level == LevelFull:
		return diyaFlame[a.Tick%len(diyaFlame)] + "◡"
	case a.Level == LevelLight && a.ascii:
		return lightSpinASCII[a.Tick%len(lightSpinASCII)] + " "
	case a.Level == LevelLight:
		return lightSpin[a.Tick%len(lightSpin)] + " "
	}
	return "* "
}

// bar draws a progress bar w cells wide. In Full mode a kalam (pen) writes
// the leading edge.
func (s *Styles) bar(a *Anim, done, total, w int) string {
	if w <= 0 {
		return ""
	}
	filled := 0
	if total > 0 {
		filled = min(w, done*w/total)
	}
	head := ""
	if a.Level == LevelFull && filled < w && total > 0 {
		head = s.Accent.Render(s.G.Pen)
		filled = min(filled, w-1)
	}
	empty := w - filled - width(head)
	return s.Accent.Render(strings.Repeat(s.G.BarFull, filled)) + head + s.Rule.Render(strings.Repeat(s.G.BarEmpty, max(empty, 0)))
}

// logo is the startup ledger mark, revealed line by line in Full mode.
var logo = []string{
	`   ___ _    _ _             ___ _ _   `,
	`  / __| |_ (_) |_ _ _ __ _ / __(_) |_ `,
	` | (__| ' \| |  _| '_/ _' | (_ | |  _|`,
	`  \___|_||_|_|\__|_| \__,_|\___|_|\__|`,
}

// splashTicks is how long the startup reveal lasts.
const splashTicks = 14

// rangoli frames burst out from the centre after a fully clean run.
var rangoli = [][]string{
	{"", "", "  ✿  ", "", ""},
	{"", "  ·  ", "· ✿ ·", "  ·  ", ""},
	{"  ·  ", " \\|/ ", "-─✿─-", " /|\\ ", "  ·  "},
	{"· * ·", "*\\|/*", "─✿❀✿─", "*/|\\*", "· * ·"},
}

var rangoliASCII = [][]string{
	{"", "", "  *  ", "", ""},
	{"", "  .  ", ". * .", "  .  ", ""},
	{"  .  ", " \\|/ ", "--*--", " /|\\ ", "  .  "},
	{". * .", "*\\|/*", "-*o*-", "*/|\\*", ". * ."},
}

// rangoliTicks is how long the burst animates before it holds still.
const rangoliTicks = 12
