package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// Frame is the area a screen draws into.
type Frame struct {
	Width, Height int
	Compact       bool // under 100 columns: fewer columns, shorter labels
	S             *Styles
	Anim          *Anim
	Env           *Env
	zones         *Zones
	x0, y0        int // where the body sits on the terminal
}

// Zone registers a clickable rectangle, in frame coordinates. Clicks on it
// come back to the screen as a ZoneClick with the same id.
func (f Frame) Zone(id string, x, y, w, h int) {
	if f.zones != nil {
		f.zones.add(id, f.x0+x, f.y0+y, w, h)
	}
}

// ZoneClick is a left click on a registered zone. X and Y are relative to
// the zone's top-left corner.
type ZoneClick struct {
	ID            string
	X, Y          int
	Double, Shift bool
}

type zone struct {
	id         string
	x, y, w, h int
}

// Zones are the clickable regions of the last rendered frame. Later zones
// win, so a dialog drawn over the body takes the click.
type Zones struct{ list []zone }

func (z *Zones) reset() { z.list = z.list[:0] }

func (z *Zones) add(id string, x, y, w, h int) {
	if w > 0 && h > 0 {
		z.list = append(z.list, zone{id, x, y, w, h})
	}
}

func (z *Zones) hit(x, y int) (zone, bool) {
	for i := len(z.list) - 1; i >= 0; i-- {
		r := z.list[i]
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return r, true
		}
	}
	return zone{}, false
}

// Text helpers. All widths are terminal cells, so wide characters and
// styled strings measure correctly.

func width(s string) int { return lipgloss.Width(s) }

func truncate(s string, w int, ellipsis string) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, ellipsis)
}

// fit truncates or pads s to exactly w cells.
func fit(s string, w int, ellipsis string) string {
	s = truncate(s, w, ellipsis)
	if d := w - width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// fitRight right-aligns s in w cells.
func fitRight(s string, w int, ellipsis string) string {
	s = truncate(s, w, ellipsis)
	if d := w - width(s); d > 0 {
		s = strings.Repeat(" ", d) + s
	}
	return s
}

func center(s string, w int) string {
	d := w - width(s)
	if d <= 0 {
		return s
	}
	return strings.Repeat(" ", d/2) + s
}

// block makes s exactly h lines of exactly w cells.
func block(lines []string, w, h int, ellipsis string) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w, ellipsis)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

// spread puts left and right on one line of w cells.
func spread(left, right string, w int, ellipsis string) string {
	gap := w - width(left) - width(right)
	if gap < 1 {
		return fit(left, w, ellipsis)
	}
	return left + strings.Repeat(" ", gap) + right
}

// num formats an integer with thousands separators: 5,012.
func num(n int) string {
	s := []byte(strings.TrimPrefix(strconv.Itoa(n), "-"))
	var b strings.Builder
	if n < 0 {
		b.WriteByte('-')
	}
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(i18n.T("tui.thousands_sep"))
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ago renders a time as "just now", "5m ago", "3h ago", "12d ago".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return i18n.T("tui.ago.now")
	case d < time.Hour:
		return i18n.T("tui.ago.minutes", "n", int(d.Minutes()))
	case d < 48*time.Hour:
		return i18n.T("tui.ago.hours", "n", int(d.Hours()))
	}
	return i18n.T("tui.ago.days", "n", int(d.Hours()/24))
}

// clock renders a duration as "45s", "3m 12s" or "1h 05m".
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return strconv.Itoa(h) + "h " + pad2(m) + "m"
	case m > 0:
		return strconv.Itoa(m) + "m " + pad2(s) + "s"
	}
	return strconv.Itoa(s) + "s"
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
