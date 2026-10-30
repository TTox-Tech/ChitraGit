package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Dialog is a modal question with buttons, drawn over the body. Keys:
// ←/→ or tab move, enter chooses, esc cancels. Buttons are clickable.
type Dialog struct {
	Title   string
	Lines   []string
	Buttons []string
	Focus   int
	Cancel  int // button chosen by esc; -1 = esc just closes
	// OnChoose runs with the chosen button's index.
	OnChoose func(i int) tea.Cmd
}

type openDialogMsg struct{ d *Dialog }

// OpenDialog returns a command that shows d.
func OpenDialog(d *Dialog) tea.Cmd {
	return func() tea.Msg { return openDialogMsg{d} }
}

// key handles a key press. done reports that the dialog should close.
func (d *Dialog) key(k tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch k.String() {
	case "left", "shift+tab", "h":
		d.Focus = (d.Focus + len(d.Buttons) - 1) % len(d.Buttons)
	case "right", "tab", "l":
		d.Focus = (d.Focus + 1) % len(d.Buttons)
	case "enter", " ":
		return d.choose(d.Focus), true
	case "esc":
		if d.Cancel >= 0 {
			return d.choose(d.Cancel), true
		}
		return nil, true
	default:
		// The first letter of a button chooses it.
		for i, b := range d.Buttons {
			if r := []rune(strings.ToLower(b)); len(r) > 0 && string(r[0]) == k.String() {
				return d.choose(i), true
			}
		}
	}
	return nil, false
}

func (d *Dialog) choose(i int) tea.Cmd {
	if d.OnChoose == nil {
		return nil
	}
	return d.OnChoose(i)
}

// render draws the dialog centred in a w×h body and registers its buttons
// as zones "dlg:<i>". It returns the body with the box drawn over it.
func (d *Dialog) render(body string, f Frame) string {
	s := f.S
	boxW := min(64, f.Width-4)
	inner := boxW - 4
	var content []string
	for _, l := range d.Lines {
		content = append(content, wrap(l, inner)...)
	}
	content = append(content, "")
	var buttons []string
	for i, b := range d.Buttons {
		buttons = append(buttons, s.button(b, i == d.Focus))
	}
	content = append(content, strings.Join(buttons, "  "))
	for i := range content {
		content[i] = fit(content[i], inner, s.G.Ellipsis)
	}
	box := lipgloss.NewStyle().Border(s.G.Border).BorderForeground(s.Palette.Primary).Padding(0, 1).
		Render(strings.Join(content, "\n"))
	boxLines := strings.Split(box, "\n")
	// Title over the top border, like a ledger page heading.
	if d.Title != "" && len(boxLines) > 0 {
		t := truncate(" "+d.Title+" ", boxW-4, s.G.Ellipsis)
		top := s.Accent.Render(s.G.Border.TopLeft+s.G.Border.Top) + s.Title.Render(t)
		boxLines[0] = top + s.Accent.Render(strings.Repeat(s.G.Border.Top, max(0, boxW-3-width(t)))+s.G.Border.TopRight)
	}

	lines := strings.Split(body, "\n")
	top := max(0, (f.Height-len(boxLines))/2)
	left := max(0, (f.Width-boxW)/2)
	for i, bl := range boxLines {
		if top+i < len(lines) {
			lines[top+i] = fit(strings.Repeat(" ", left)+bl, f.Width, s.G.Ellipsis)
		}
	}
	// Button zones: the button row is the last content line.
	row := top + len(boxLines) - 2
	x := left + 2
	for i, b := range buttons {
		f.Zone("dlg:"+strconv.Itoa(i), x, row, width(b), 1)
		x += width(b) + 2
	}
	return strings.Join(lines, "\n")
}

// wrap breaks plain text into lines of at most w cells, on spaces.
func wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	if text == "" {
		return []string{""}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case width(line)+1+width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	return append(out, line)
}
