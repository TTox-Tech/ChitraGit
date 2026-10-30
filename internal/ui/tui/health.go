package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Health is the dashboard (PRD F10): counts of repos that need a look,
// each a clickable card that opens a filtered browser, plus failures,
// disk usage, tags and recent runs.
type Health struct {
	h      *store.Health
	tags   []tagCount
	loaded bool
	err    error

	cursor    int // selected card
	offset    int
	height    int
	perRow    int // cards per row in the last frame, for arrow keys
	measuring bool
	note      string
	noteErr   bool
}

type tagCount struct {
	tag string
	ids []string
}

func NewHealth() *Health { return &Health{} }

type healthLoadedMsg struct {
	h    *store.Health
	tags []tagCount
	err  error
}

type measuredMsg struct{ err error }

func loadHealth(env *Env) tea.Cmd {
	in := env.insights()
	if in == nil {
		return nil
	}
	return func() tea.Msg {
		h, err := in.Health(env.Ctx)
		if err != nil {
			return healthLoadedMsg{err: err}
		}
		byRepo, err := in.RepoTags(env.Ctx)
		if err != nil {
			return healthLoadedMsg{err: err}
		}
		ids := map[string][]string{}
		for id, ts := range byRepo {
			for _, t := range ts {
				ids[t] = append(ids[t], id)
			}
		}
		var tags []tagCount
		for t, list := range ids {
			slices.Sort(list)
			tags = append(tags, tagCount{t, list})
		}
		slices.SortFunc(tags, func(a, b tagCount) int {
			if len(a.ids) != len(b.ids) {
				return len(b.ids) - len(a.ids)
			}
			return strings.Compare(a.tag, b.tag)
		})
		return healthLoadedMsg{h: h, tags: tags}
	}
}

func (h *Health) Title() string         { return i18n.T("tui.tab.health") }
func (h *Health) Init(env *Env) tea.Cmd { return loadHealth(env) }
func (h *Health) Animating() bool       { return !h.loaded || h.measuring }

func (h *Health) Keys() []KeyHint {
	return []KeyHint{{"←→↑↓", i18n.T("tui.health.key.cards")}, {"enter", i18n.T("tui.key.open")},
		{"m", i18n.T("tui.health.key.measure")}, {"r", i18n.T("tui.health.key.refresh")}}
}

func (h *Health) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case healthLoadedMsg:
		h.loaded, h.err = true, msg.err
		if msg.err == nil {
			h.h, h.tags = msg.h, msg.tags
		}
	case ReposChangedMsg:
		return loadHealth(env)
	case measuredMsg:
		h.measuring = false
		if msg.err != nil {
			h.note, h.noteErr = msg.err.Error(), true
		} else {
			h.note, h.noteErr = i18n.T("tui.health.measured"), false
		}
		return loadHealth(env)
	case tea.KeyMsg:
		return h.key(msg, env)
	case ZoneClick:
		return h.click(msg, env)
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			h.offset = max(0, h.offset-3)
		case tea.MouseButtonWheelDown:
			h.offset += 3
		}
	}
	return nil
}

// cardsPerRow is how many cards fit side by side.
func cardsPerRow(w int) int { return max(1, min(4, (w-1)/cardWidth)) }

const cardWidth = 30

func (h *Health) key(k tea.KeyMsg, env *Env) tea.Cmd {
	n := len(store.Cards)
	per := max(1, h.perRow)
	switch k.String() {
	case "left", "h":
		h.cursor = max(0, h.cursor-1)
	case "right", "l":
		h.cursor = min(n-1, h.cursor+1)
	case "up", "k":
		if h.cursor-per >= 0 {
			h.cursor -= per
		} else {
			h.offset = max(0, h.offset-1)
		}
	case "down", "j":
		if h.cursor+per < n {
			h.cursor += per
		} else {
			h.offset++
		}
	case "pgdown", "ctrl+d":
		h.offset += max(1, h.height-1)
	case "pgup", "ctrl+u":
		h.offset = max(0, h.offset-max(1, h.height-1))
	case "enter":
		return h.openCard(store.Cards[h.cursor], env)
	case "m":
		return h.measure(env)
	case "r":
		return loadHealth(env)
	}
	return nil
}

func (h *Health) click(c ZoneClick, env *Env) tea.Cmd {
	id := c.ID
	switch {
	case strings.HasPrefix(id, "card:"):
		card := strings.TrimPrefix(id, "card:")
		h.cursor = max(0, slices.Index(store.Cards, card))
		return h.openCard(card, env)
	case strings.HasPrefix(id, "repo:"):
		return Navigate(ScreenDetail, OpenDetail{ID: strings.TrimPrefix(id, "repo:")})
	case strings.HasPrefix(id, "tag:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "tag:"))
		if i >= 0 && i < len(h.tags) {
			t := h.tags[i]
			return Navigate(ScreenBrowser, BrowserIDs{Label: "#" + t.tag, IDs: t.ids})
		}
	case id == "measure":
		return h.measure(env)
	}
	return nil
}

func (h *Health) openCard(card string, env *Env) tea.Cmd {
	in := env.insights()
	if in == nil {
		return nil
	}
	return func() tea.Msg {
		ids, err := in.CardRepos(env.Ctx, card)
		if err != nil {
			return ackMsg{err: err}
		}
		return NavigateMsg{To: ScreenBrowser, Payload: BrowserIDs{Label: i18n.T("tui.health.card." + card), IDs: ids}}
	}
}

func (h *Health) measure(env *Env) tea.Cmd {
	in := env.insights()
	if in == nil || h.measuring {
		return nil
	}
	h.measuring, h.note = true, ""
	return func() tea.Msg { return measuredMsg{in.MeasureSizes(env.Ctx)} }
}

func (h *Health) View(f Frame) string {
	s := f.S
	h.perRow = cardsPerRow(f.Width)
	switch {
	case f.Env.insights() == nil:
		return "\n  " + s.Muted.Render(i18n.T("tui.activity.unavailable"))
	case !h.loaded:
		return "\n  " + s.Accent.Render(f.Anim.Spinner()) + " " + s.Muted.Render(i18n.T("tui.browser.loading"))
	case h.err != nil:
		return "\n  " + s.Fail.Render(s.G.Fail+" "+h.err.Error())
	}
	d := h.h
	type zoneAt struct {
		id   string
		x, w int
	}
	type line struct {
		text  string
		zones []zoneAt
	}
	var ls []line
	add := func(text string, zones ...zoneAt) { ls = append(ls, line{text, zones}) }

	add(" " + s.Title.Render(i18n.T("tui.health.title", "n", num(d.Total))))
	add("")

	// Cards, cardsPerRow side by side.
	per := h.perRow
	for start := 0; start < len(store.Cards); start += per {
		var row strings.Builder
		var zs []zoneAt
		row.WriteString(" ")
		x := 1
		for i := start; i < min(start+per, len(store.Cards)); i++ {
			c := store.Cards[i]
			n := d.Cards[c]
			st := s.Muted
			switch {
			case n == 0:
			case c == store.CardRescued:
				st = s.Hint
			case c == store.CardFailing || c == store.CardMissing:
				st = s.Fail
			case c == store.CardLocked || c == store.CardFavourites:
				st = s.Text
			default:
				st = s.Warn
			}
			mark := "  "
			if i == h.cursor {
				mark = s.Cursor.Render(s.G.Cursor) + " "
			}
			cell := mark + st.Render(fitRight(num(n), 6, s.G.Ellipsis)+"  "+i18n.T("tui.health.card."+c))
			row.WriteString(fit(cell, cardWidth-1, s.G.Ellipsis) + " ")
			zs = append(zs, zoneAt{"card:" + c, x, cardWidth - 1})
			x += cardWidth
		}
		add(row.String(), zs...)
	}

	allClear := true
	for _, c := range store.Cards {
		if c != store.CardLocked && c != store.CardFavourites && d.Cards[c] > 0 {
			allClear = false
		}
	}
	if allClear {
		add("")
		add("  " + s.OK.Render(s.G.OK+" "+i18n.T("health.all_clear")))
	}

	section := func(title string) {
		add("")
		add(" " + s.Header.Render(title))
	}
	if len(d.Failing) > 0 {
		section(i18n.T("health.failing"))
		for _, r := range d.Failing[:min(5, len(d.Failing))] {
			add("  "+s.Fail.Render(s.G.Fail)+" "+s.Label.Render(r.Path)+"  "+s.Text.Render(r.Reason),
				zoneAt{"repo:" + r.ID, 0, f.Width})
		}
	}

	section(i18n.T("health.disk"))
	btn := s.button(i18n.T("tui.health.measure"), false)
	switch {
	case h.measuring:
		add("  " + s.Accent.Render(f.Anim.Spinner()) + " " + s.Muted.Render(i18n.T("tui.health.measuring")))
	case d.Measured == 0:
		add("  "+s.Muted.Render(i18n.T("tui.health.unmeasured"))+"  "+btn,
			zoneAt{"measure", 2 + width(i18n.T("tui.health.unmeasured")) + 2, width(btn)})
	default:
		total := i18n.T("health.disk_total", "size", humanBytes(d.DiskTotal), "count", num(d.Measured))
		add("  "+s.Text.Render(total)+"  "+btn, zoneAt{"measure", 2 + width(total) + 2, width(btn)})
		for _, r := range d.Largest[:min(5, len(d.Largest))] {
			add("  "+s.Hint.Render(fitRight(humanBytes(r.SizeBytes), 10, s.G.Ellipsis))+"  "+s.Label.Render(r.Path),
				zoneAt{"repo:" + r.ID, 0, f.Width})
		}
	}

	if len(h.tags) > 0 {
		section(i18n.T("tui.health.tags"))
		var row strings.Builder
		var zs []zoneAt
		row.WriteString("  ")
		x := 2
		for i, t := range h.tags {
			chip := "#" + t.tag + " " + num(len(t.ids))
			if x+width(chip)+2 > f.Width {
				add(row.String(), zs...)
				row.Reset()
				row.WriteString("  ")
				zs, x = nil, 2
			}
			row.WriteString(s.Hint.Render(chip) + "  ")
			zs = append(zs, zoneAt{"tag:" + strconv.Itoa(i), x, width(chip)})
			x += width(chip) + 2
		}
		add(row.String(), zs...)
	}

	if len(d.Runs) > 0 {
		section(i18n.T("health.runs"))
		now := f.Env.Now()
		shown := 0
		for _, r := range d.Runs {
			if r.Kind != "sync" || shown == 5 {
				continue
			}
			shown++
			st := s.Text
			if r.Failed > 0 || r.State != "finished" {
				st = s.Warn
			}
			text := "  " + st.Render("#"+strconv.FormatInt(r.ID, 10)+" "+i18n.T("tui.health.run."+runState(r.State))) + "  " +
				s.Muted.Render(i18n.T("health.run_counts", "updated", num(r.Updated), "unchanged", num(r.Unchanged),
					"skipped", num(r.Skipped), "failed", num(r.Failed)))
			if t, err := time.Parse(time.RFC3339Nano, r.StartedAt); err == nil {
				text = spread(text, s.Muted.Render(ago(t, now))+" ", f.Width, s.G.Ellipsis)
			}
			add(text)
		}
	}

	bottom := ""
	if h.note != "" {
		st := s.Muted
		if h.noteErr {
			st = s.Warn
		}
		bottom = " " + st.Render(h.note)
	}
	h.height = f.Height - btoi(bottom != "")
	h.offset = max(0, min(h.offset, len(ls)-h.height))
	var out []string
	for i := h.offset; i < min(len(ls), h.offset+h.height); i++ {
		for _, z := range ls[i].zones {
			f.Zone(z.id, z.x, len(out), z.w, 1)
		}
		out = append(out, ls[i].text)
	}
	if bottom != "" {
		return block(out, f.Width, h.height, s.G.Ellipsis) + "\n" + fit(bottom, f.Width, s.G.Ellipsis)
	}
	return strings.Join(out, "\n")
}

// runState maps stored run states to the labels the dashboard has.
func runState(st string) string {
	switch st {
	case "finished", "interrupted", "running":
		return st
	}
	return "other"
}
