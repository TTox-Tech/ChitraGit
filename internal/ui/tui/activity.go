package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// activityKinds are the feed filters, cycled with f.
var activityKinds = []string{"all", store.ActivityRefs, "rescued", "renamed"}

// Activity is the timeline of what changed across the archive (PRD F9).
// It reads only the archive database, so it works offline.
type Activity struct {
	all    []store.Activity
	loaded bool
	err    error

	kind      int
	query     string
	searching bool

	rows           []actRow
	cursor, offset int // cursor indexes rows; it always sits on an entry
	height         int
}

// actRow is a day heading (entry < 0) or one entry of all.
type actRow struct {
	day   string
	entry int
}

func NewActivity() *Activity { return &Activity{} }

type activityLoadedMsg struct {
	feed []store.Activity
	err  error
}

func loadActivity(env *Env) tea.Cmd {
	in := env.insights()
	if in == nil {
		return nil
	}
	return func() tea.Msg {
		feed, err := in.Activity(env.Ctx, store.ActivityFilter{Limit: 2000})
		return activityLoadedMsg{feed, err}
	}
}

func (a *Activity) Title() string         { return i18n.T("tui.tab.activity") }
func (a *Activity) Init(env *Env) tea.Cmd { return loadActivity(env) }
func (a *Activity) Capturing() bool       { return a.searching }
func (a *Activity) Animating() bool       { return !a.loaded }

func (a *Activity) Keys() []KeyHint {
	if a.searching {
		return []KeyHint{{"enter", i18n.T("tui.key.keep_search")}, {"esc", i18n.T("tui.key.clear_search")}}
	}
	return []KeyHint{{"↑↓", i18n.T("tui.key.move")}, {"/", i18n.T("tui.key.search")},
		{"f", i18n.T("tui.key.filter")}, {"enter", i18n.T("tui.key.open")}}
}

func (a *Activity) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case activityLoadedMsg:
		a.loaded, a.err = true, msg.err
		if msg.err == nil {
			a.all = msg.feed
			a.refresh(env.Now())
		}
	case ReposChangedMsg:
		return loadActivity(env)
	case tea.KeyMsg:
		return a.key(msg, env)
	case ZoneClick:
		return a.click(msg, env)
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			a.offset = max(0, a.offset-3)
		case tea.MouseButtonWheelDown:
			a.offset = max(0, min(a.offset+3, len(a.rows)-a.height))
		}
	}
	return nil
}

func (a *Activity) key(k tea.KeyMsg, env *Env) tea.Cmd {
	if a.searching {
		switch k.Type {
		case tea.KeyEnter:
			a.searching = false
		case tea.KeyEsc:
			a.searching, a.query = false, ""
			a.refresh(env.Now())
		case tea.KeyBackspace:
			if r := []rune(a.query); len(r) > 0 {
				a.query = string(r[:len(r)-1])
				a.refresh(env.Now())
			}
		case tea.KeyRunes, tea.KeySpace:
			a.query += string(k.Runes)
			a.refresh(env.Now())
		}
		return nil
	}
	page := max(1, a.height-1)
	switch k.String() {
	case "/":
		a.searching = true
	case "esc":
		if a.query != "" || a.kind != 0 {
			a.query, a.kind = "", 0
			a.refresh(env.Now())
		}
	case "f":
		a.kind = (a.kind + 1) % len(activityKinds)
		a.refresh(env.Now())
	case "F":
		a.kind = (a.kind + len(activityKinds) - 1) % len(activityKinds)
		a.refresh(env.Now())
	case "up", "k":
		a.move(-1)
	case "down", "j":
		a.move(1)
	case "pgup", "ctrl+u":
		a.move(-page)
	case "pgdown", "ctrl+d":
		a.move(page)
	case "home", "g":
		a.cursor = -1
		a.move(1)
	case "end", "G":
		a.cursor = len(a.rows)
		a.move(-1)
	case "enter":
		return a.open(a.cursor)
	}
	return nil
}

func (a *Activity) click(c ZoneClick, env *Env) tea.Cmd {
	switch {
	case c.ID == "afilter":
		a.kind = (a.kind + 1) % len(activityKinds)
		a.refresh(env.Now())
	case c.ID == "asearch":
		a.searching = true
	case strings.HasPrefix(c.ID, "arow:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(c.ID, "arow:"))
		if i < 0 || i >= len(a.rows) || a.rows[i].entry < 0 {
			return nil
		}
		a.cursor = i
		if c.Double {
			return a.open(i)
		}
	}
	return nil
}

// open goes to the repo's detail page, at its activity tab.
func (a *Activity) open(i int) tea.Cmd {
	if i < 0 || i >= len(a.rows) || a.rows[i].entry < 0 {
		return nil
	}
	e := a.all[a.rows[i].entry]
	if e.RepoID == "" {
		return nil
	}
	return Navigate(ScreenDetail, OpenDetail{ID: e.RepoID, Tab: "activity"})
}

// move steps the cursor by n entries' worth of rows, skipping headings.
func (a *Activity) move(n int) {
	if len(a.rows) == 0 {
		return
	}
	step := 1
	if n < 0 {
		step = -1
	}
	target := max(-1, min(a.cursor+n, len(a.rows)))
	i := target
	for i >= 0 && i < len(a.rows) && a.rows[i].entry < 0 {
		i += step
	}
	if i < 0 || i >= len(a.rows) {
		// Ran off the end: take the nearest entry the other way.
		i = max(0, min(target, len(a.rows)-1))
		for i >= 0 && i < len(a.rows) && a.rows[i].entry < 0 {
			i -= step
		}
	}
	if i >= 0 && i < len(a.rows) {
		a.cursor = i
	}
	a.clamp()
}

func (a *Activity) clamp() {
	if a.height <= 0 {
		return
	}
	// Keep the heading above the first entry visible.
	if a.cursor-1 < a.offset {
		a.offset = max(0, a.cursor-1)
	}
	if a.cursor >= a.offset+a.height {
		a.offset = a.cursor - a.height + 1
	}
	a.offset = max(0, min(a.offset, max(0, len(a.rows)-a.height)))
}

func (a *Activity) refresh(now time.Time) {
	kind := activityKinds[a.kind]
	q := strings.ToLower(a.query)
	a.rows = a.rows[:0]
	day := ""
	for i, e := range a.all {
		if kind != "all" && e.Type != kind {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Path), q) {
			continue
		}
		if d := dayHeading(e.At, now); d != day {
			day = d
			a.rows = append(a.rows, actRow{day: d, entry: -1})
		}
		a.rows = append(a.rows, actRow{entry: i})
	}
	a.cursor, a.offset = -1, 0
	a.move(1)
}

// dayHeading groups the feed: Today, Yesterday, then dates.
func dayHeading(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	t = t.In(now.Location())
	switch t.Format(time.DateOnly) {
	case now.Format(time.DateOnly):
		return i18n.T("activity.today")
	case now.AddDate(0, 0, -1).Format(time.DateOnly):
		return i18n.T("activity.yesterday")
	}
	return t.Format("Mon 2 Jan 2006")
}

func (a *Activity) View(f Frame) string {
	s := f.S
	lines := []string{a.toolbar(f)}
	switch {
	case f.Env.insights() == nil:
		return strings.Join(append(lines, "", "  "+s.Muted.Render(i18n.T("tui.activity.unavailable"))), "\n")
	case !a.loaded:
		return strings.Join(append(lines, "", "  "+s.Accent.Render(f.Anim.Spinner())+" "+s.Muted.Render(i18n.T("tui.browser.loading"))), "\n")
	case a.err != nil:
		return strings.Join(append(lines, "", "  "+s.Fail.Render(s.G.Fail+" "+a.err.Error())), "\n")
	case len(a.rows) == 0:
		msg := i18n.T("activity.empty")
		if len(a.all) > 0 {
			msg = i18n.T("tui.browser.no_match")
		}
		return strings.Join(append(lines, "", "  "+s.Muted.Render(msg)), "\n")
	}
	a.height = f.Height - 1
	a.clamp()
	now := f.Env.Now()
	for i := a.offset; i < min(len(a.rows), a.offset+a.height); i++ {
		r := a.rows[i]
		if r.entry < 0 {
			lines = append(lines, " "+s.Header.Render(r.day))
			continue
		}
		f.Zone("arow:"+strconv.Itoa(i), 0, len(lines), f.Width, 1)
		line := activityLine(f, a.all[r.entry], now, true)
		if i == a.cursor {
			line = s.Cursor.Render(s.G.Cursor) + strings.TrimPrefix(line, " ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (a *Activity) toolbar(f Frame) string {
	s := f.S
	var search string
	switch {
	case a.searching:
		search = s.Accent.Render("/ ") + s.Text.Render(a.query) + s.Accent.Render("▏")
	case a.query != "":
		search = s.Accent.Render("/ ") + s.Text.Render(a.query)
	default:
		search = s.Muted.Render("/ " + i18n.T("tui.activity.search"))
	}
	searchW := min(max(20, f.Width/3), width(search)+2)
	f.Zone("asearch", 0, 0, searchW+1, 1)
	chip := s.Hint.Render("[" + i18n.T("tui.activity.kind."+activityKinds[a.kind]) + "]")
	f.Zone("afilter", 1+searchW+1, 0, width(chip), 1)
	left := " " + fit(search, searchW, s.G.Ellipsis) + " " + chip
	entries := 0
	for _, r := range a.rows {
		if r.entry >= 0 {
			entries++
		}
	}
	return spread(left, s.Muted.Render(i18n.T("tui.activity.count", "n", num(entries)))+" ", f.Width, s.G.Ellipsis)
}
