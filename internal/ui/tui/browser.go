package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// BrowserFilter, sent as a Navigate payload to the browser, selects a
// filter by key, e.g. Navigate(ScreenBrowser, BrowserFilter("failed")).
type BrowserFilter string

type filter struct {
	key   string
	match func(core.Repo) bool
}

// needsLook are statuses where the repo is fine but the user has
// something to decide or finish.
var needsLook = map[string]bool{
	syncer.StatusDirty: true, syncer.StatusDiverged: true, syncer.StatusRewritten: true,
	syncer.StatusConflict: true, syncer.StatusBlocked: true, syncer.StatusBusy: true,
	syncer.StatusUpstreamGon: true, syncer.StatusDetached: true, syncer.StatusNoUpstream: true,
	syncer.StatusRecovered: true,
}

var filters = []filter{
	{"all", func(core.Repo) bool { return true }},
	{"attention", func(r core.Repo) bool { return !r.Missing && needsLook[r.Status] }},
	{"failed", func(r core.Repo) bool { return !r.Missing && r.Status == syncer.StatusFailed }},
	{"never", func(r core.Repo) bool { return !r.Missing && r.LastSyncedAt == "" }},
	{"missing", func(r core.Repo) bool { return r.Missing }},
	{"favourites", func(r core.Repo) bool { return r.Favourite }},
}

// column is one list column. Width 0 means it takes the remaining space.
type column struct {
	key     string
	width   int
	compact bool // shown in the compact layout too
	cmp     func(a, b core.Repo) int
}

var columns = []column{
	{"name", 0, true, func(a, b core.Repo) int { return strings.Compare(a.Path, b.Path) }},
	{"owner", 18, false, func(a, b core.Repo) int { return strings.Compare(a.Owner, b.Owner) }},
	{"provider", 9, false, func(a, b core.Repo) int { return strings.Compare(a.Provider, b.Provider) }},
	{"status", 15, true, func(a, b core.Repo) int { return strings.Compare(statusKey(a), statusKey(b)) }},
	{"synced", 11, true, func(a, b core.Repo) int {
		// Never-synced sorts as oldest.
		return strings.Compare(a.LastSyncedAt, b.LastSyncedAt)
	}},
}

func statusKey(r core.Repo) string {
	if r.Missing {
		return "missing"
	}
	if r.Status == "" {
		return "unknown"
	}
	return r.Status
}

// Browser is the home screen: every repo in the archive, virtualised so
// only visible rows are drawn (PRD F7).
type Browser struct {
	all  []core.Repo
	rows []int // indexes into all, filtered and sorted

	cursor, offset int
	marks          map[string]bool
	anchor         int // where a shift-click range starts

	query     string
	searching bool
	filter    int
	sortCol   int
	desc      bool

	loaded bool
	err    error
	height int // list rows in the last frame, for paging

	// ids, when set, limits the list to these repos (a health card or a
	// tag); idLabel names it in the filter chip.
	ids     map[string]bool
	idLabel string
	input   lineInput // tag prompt for bulk actions
	note    string    // result of the last bulk action
	noteErr bool
}

// NewBrowser returns an empty browser; it loads repos on Init.
func NewBrowser() *Browser { return &Browser{marks: map[string]bool{}} }

type reposLoadedMsg struct {
	repos []core.Repo
	err   error
}

func loadRepos(env *Env) tea.Cmd {
	return func() tea.Msg {
		r, err := env.Backend.Repos(env.Ctx)
		return reposLoadedMsg{r, err}
	}
}

func (b *Browser) Title() string         { return i18n.T("tui.tab.repos") }
func (b *Browser) Init(env *Env) tea.Cmd { return loadRepos(env) }
func (b *Browser) Capturing() bool       { return b.searching || b.input.active }
func (b *Browser) Animating() bool       { return !b.loaded }
func (b *Browser) Current() *core.Repo   { return b.repo(b.cursor) }
func (b *Browser) filterKey() string     { return filters[b.filter].key }

func (b *Browser) repo(i int) *core.Repo {
	if i < 0 || i >= len(b.rows) {
		return nil
	}
	return &b.all[b.rows[i]]
}

// Marked returns the marked repos, in list order.
func (b *Browser) Marked() []core.Repo {
	var out []core.Repo
	for _, i := range b.rows {
		if b.marks[b.all[i].ID] {
			out = append(out, b.all[i])
		}
	}
	return out
}

func (b *Browser) Keys() []KeyHint {
	if b.input.active {
		return []KeyHint{{"enter", i18n.T("tui.detail.key.save")}, {"esc", i18n.T("tui.key.clear_search")}}
	}
	if b.searching {
		return []KeyHint{{"enter", i18n.T("tui.key.keep_search")}, {"esc", i18n.T("tui.key.clear_search")}}
	}
	if len(b.marks) > 0 {
		// With repos marked, show what can be done to all of them.
		return []KeyHint{
			{"space", i18n.T("tui.key.mark")}, {"a", i18n.T("tui.bulk.key.all")}, {"s", i18n.T("tui.key.sync")},
			{"*", i18n.T("tui.detail.key.fav")}, {"t", i18n.T("tui.detail.key.tag")}, {"T", i18n.T("tui.bulk.key.untag")},
			{"esc", i18n.T("tui.bulk.key.clear")},
		}
	}
	return []KeyHint{
		{"↑↓", i18n.T("tui.key.move")}, {"/", i18n.T("tui.key.search")}, {"f", i18n.T("tui.key.filter")},
		{"o", i18n.T("tui.key.sort")}, {"space", i18n.T("tui.key.mark")}, {"enter", i18n.T("tui.key.open")},
		{"s", i18n.T("tui.key.sync")}, {"S", i18n.T("tui.key.sync_all")},
	}
}

func (b *Browser) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case reposLoadedMsg:
		b.loaded, b.err = true, msg.err
		if msg.err == nil {
			b.all = msg.repos
			b.refresh()
		}
	case ReposChangedMsg:
		return loadRepos(env)
	case NavigateMsg:
		switch p := msg.Payload.(type) {
		case BrowserFilter:
			b.ids = nil
			b.setFilter(string(p))
		case BrowserIDs:
			b.ids, b.idLabel = map[string]bool{}, p.Label
			for _, id := range p.IDs {
				b.ids[id] = true
			}
			b.filter = 0
			b.refresh()
		}
	case ackMsg:
		b.note, b.noteErr = msg.text, msg.err != nil
		if msg.err != nil {
			b.note = msg.err.Error()
		}
	case tea.KeyMsg:
		b.note = ""
		if b.input.active {
			return b.input.key(msg)
		}
		return b.key(msg, env)
	case ZoneClick:
		return b.click(msg)
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			b.scroll(-3)
		case tea.MouseButtonWheelDown:
			b.scroll(3)
		}
	}
	return nil
}

func (b *Browser) setFilter(key string) {
	for i, f := range filters {
		if f.key == key {
			b.filter = i
			b.refresh()
		}
	}
}

func (b *Browser) key(k tea.KeyMsg, env *Env) tea.Cmd {
	if b.searching {
		switch k.Type {
		case tea.KeyEnter:
			b.searching = false
		case tea.KeyEsc:
			b.searching, b.query = false, ""
			b.refresh()
		case tea.KeyBackspace:
			if r := []rune(b.query); len(r) > 0 {
				b.query = string(r[:len(r)-1])
				b.refresh()
			}
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			b.move(k.String())
		case tea.KeyRunes, tea.KeySpace:
			b.query += string(k.Runes)
			b.refresh()
		}
		return nil
	}
	switch k.String() {
	case "/":
		b.searching = true
	case "esc":
		switch {
		case len(b.marks) > 0:
			clear(b.marks)
		case b.query != "":
			b.query = ""
			b.refresh()
		case b.ids != nil:
			b.ids = nil
			b.refresh()
		case b.filter != 0:
			b.filter = 0
			b.refresh()
		}
	case "f":
		b.filter = (b.filter + 1) % len(filters)
		b.ids = nil
		b.refresh()
	case "F":
		b.filter = (b.filter + len(filters) - 1) % len(filters)
		b.ids = nil
		b.refresh()
	case "o":
		b.sortBy((b.sortCol + 1) % len(columns))
	case "O":
		b.desc = !b.desc
		b.refresh()
	case " ":
		if r := b.Current(); r != nil {
			b.toggle(*r)
			b.anchor = b.cursor
			b.move("down")
		}
	case "a":
		b.markAll()
	case "enter":
		if r := b.Current(); r != nil {
			return Navigate(ScreenDetail, *r)
		}
	case "s":
		return b.syncSelection()
	case "S":
		return Navigate(ScreenSync, SyncStart{})
	default:
		if cmd, ok := b.bulk(k, env); ok {
			return cmd
		}
		b.move(k.String())
	}
	return nil
}

// bulk is where actions on the marked repos plug in (tag, favourite,
// lock, bundle). Marked returns the set; with nothing marked, actions
// apply to the current repo.
func (b *Browser) bulk(k tea.KeyMsg, env *Env) (tea.Cmd, bool) {
	if env == nil {
		return nil, false
	}
	in := env.insights()
	if in == nil {
		return nil, false
	}
	targets := b.Marked()
	if len(targets) == 0 {
		if r := b.Current(); r != nil {
			targets = []core.Repo{*r}
		}
	}
	if len(targets) == 0 {
		return nil, false
	}
	ids := make([]string, len(targets))
	allFav := true
	for i, r := range targets {
		ids[i] = r.ID
		allFav = allFav && r.Favourite
	}
	n := num(len(ids))
	switch k.String() {
	case "*":
		text := i18n.T("tui.bulk.fav_on", "n", n)
		if allFav {
			text = i18n.T("tui.bulk.fav_off", "n", n)
		}
		return changed(text, func() error { return in.SetFavourite(env.Ctx, ids, !allFav) }), true
	case "t":
		b.input.start(i18n.T("tui.bulk.add_tags", "n", n), func(v string) tea.Cmd {
			tags := splitTags(v)
			return changed(i18n.T("tui.bulk.tags_added", "n", n), func() error { return in.AddTags(env.Ctx, ids, tags) })
		})
		return nil, true
	case "T":
		b.input.start(i18n.T("tui.bulk.remove_tags", "n", n), func(v string) tea.Cmd {
			tags := splitTags(v)
			return changed(i18n.T("tui.bulk.tags_removed", "n", n), func() error { return in.RemoveTags(env.Ctx, ids, tags) })
		})
		return nil, true
	}
	return nil, false
}

func (b *Browser) syncSelection() tea.Cmd {
	var ids []string
	for _, r := range b.Marked() {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		r := b.Current()
		if r == nil {
			return nil
		}
		ids = []string{r.ID}
	}
	return Navigate(ScreenSync, SyncStart{IDs: ids})
}

func (b *Browser) toggle(r core.Repo) {
	if b.marks[r.ID] {
		delete(b.marks, r.ID)
	} else {
		b.marks[r.ID] = true
	}
}

// markAll marks every visible repo, or unmarks them all if they already are.
func (b *Browser) markAll() {
	all := true
	for _, i := range b.rows {
		if !b.marks[b.all[i].ID] {
			all = false
			break
		}
	}
	for _, i := range b.rows {
		if all {
			delete(b.marks, b.all[i].ID)
		} else {
			b.marks[b.all[i].ID] = true
		}
	}
}

func (b *Browser) move(key string) {
	page := max(1, b.height-1)
	switch key {
	case "up", "k":
		b.cursor--
	case "down", "j":
		b.cursor++
	case "pgup", "ctrl+u":
		b.cursor -= page
	case "pgdown", "ctrl+d":
		b.cursor += page
	case "home", "g":
		b.cursor = 0
	case "end", "G":
		b.cursor = len(b.rows) - 1
	default:
		return
	}
	b.clamp()
}

func (b *Browser) scroll(n int) {
	b.offset += n
	b.offset = max(0, min(b.offset, len(b.rows)-b.height))
	b.cursor = max(b.offset, min(b.cursor, b.offset+b.height-1))
	b.clamp()
}

func (b *Browser) clamp() {
	b.cursor = max(0, min(b.cursor, len(b.rows)-1))
	if b.height <= 0 {
		return
	}
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+b.height {
		b.offset = b.cursor - b.height + 1
	}
	b.offset = max(0, min(b.offset, max(0, len(b.rows)-b.height)))
}

func (b *Browser) sortBy(col int) {
	if col == b.sortCol {
		b.desc = !b.desc
	} else {
		b.sortCol, b.desc = col, false
	}
	b.refresh()
}

func (b *Browser) click(c ZoneClick) tea.Cmd {
	switch {
	case c.ID == "filter":
		b.filter = (b.filter + 1) % len(filters)
		b.ids = nil
		b.refresh()
	case c.ID == "search":
		b.searching = true
	case strings.HasPrefix(c.ID, "col:"):
		col, _ := strconv.Atoi(strings.TrimPrefix(c.ID, "col:"))
		b.sortBy(col)
	case strings.HasPrefix(c.ID, "row:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(c.ID, "row:"))
		r := b.repo(i)
		if r == nil {
			return nil
		}
		switch {
		case c.Shift:
			lo, hi := min(b.anchor, i), max(b.anchor, i)
			for j := lo; j <= hi; j++ {
				b.marks[b.all[b.rows[j]].ID] = true
			}
		case c.Double:
			b.cursor = i
			return Navigate(ScreenDetail, *r)
		default:
			b.anchor = i
		}
		b.cursor = i
		b.clamp()
	}
	return nil
}

// refresh recomputes the visible rows, keeping the cursor on the same
// repo when it is still visible.
func (b *Browser) refresh() {
	var keep string
	if r := b.Current(); r != nil {
		keep = r.ID
	}
	match := filters[b.filter].match
	if b.ids != nil {
		match = func(r core.Repo) bool { return b.ids[r.ID] }
	}
	b.rows = b.rows[:0]
	scores := map[int]int{}
	for i, r := range b.all {
		if !match(r) {
			continue
		}
		if b.query != "" {
			sc, ok := fuzzyScore(b.query, r.Path)
			if !ok {
				continue
			}
			scores[i] = sc
		}
		b.rows = append(b.rows, i)
	}
	col := columns[b.sortCol]
	slices.SortStableFunc(b.rows, func(x, y int) int {
		// While searching, the best match comes first.
		if b.query != "" {
			if c := cmp.Compare(scores[y], scores[x]); c != 0 {
				return c
			}
		}
		c := col.cmp(b.all[x], b.all[y])
		if c == 0 {
			c = strings.Compare(b.all[x].Path, b.all[y].Path)
		}
		if b.desc {
			c = -c
		}
		return c
	})
	b.cursor = 0
	for i, idx := range b.rows {
		if b.all[idx].ID == keep {
			b.cursor = i
		}
	}
	b.clamp()
}

// layout returns the visible columns and their widths for a frame.
func (b *Browser) layout(f Frame) ([]int, []int) {
	var cols, widths []int
	fixed := 3 // cursor + mark
	for i, c := range columns {
		if f.Compact && !c.compact {
			continue
		}
		cols = append(cols, i)
		widths = append(widths, c.width)
		fixed += c.width + 1
	}
	for i, c := range cols {
		if columns[c].width == 0 {
			widths[i] = max(10, f.Width-fixed)
		}
	}
	return cols, widths
}

func (b *Browser) View(f Frame) string {
	s := f.S
	lines := []string{b.toolbar(f)}
	if !b.loaded {
		return strings.Join(append(lines, "", "  "+s.Accent.Render(f.Anim.Spinner())+" "+s.Muted.Render(i18n.T("tui.browser.loading"))), "\n")
	}
	if b.err != nil {
		return strings.Join(append(lines, "", "  "+s.Fail.Render(s.G.Fail+" "+b.err.Error())), "\n")
	}

	cols, widths := b.layout(f)
	header := "   "
	x := 3
	for i, c := range cols {
		title := i18n.T("tui.col." + columns[c].key)
		if c == b.sortCol {
			title += map[bool]string{false: " ↑", true: " ↓"}[b.desc]
		}
		cell := fit(title, widths[i], s.G.Ellipsis)
		f.Zone("col:"+strconv.Itoa(c), x, 1, widths[i], 1)
		header += s.Header.Render(cell) + " "
		x += widths[i] + 1
	}
	lines = append(lines, header)

	b.height = f.Height - 2
	b.clamp()
	if len(b.rows) == 0 {
		msg := i18n.T("tui.browser.no_match")
		if len(b.all) == 0 {
			msg = i18n.T("tui.browser.empty")
		}
		return strings.Join(append(lines, "", "  "+s.Muted.Render(msg)), "\n")
	}
	now := f.Env.Now()
	for i := b.offset; i < min(len(b.rows), b.offset+b.height); i++ {
		r := b.all[b.rows[i]]
		f.Zone("row:"+strconv.Itoa(i), 0, len(lines), f.Width, 1)
		lines = append(lines, b.row(f, r, i == b.cursor, cols, widths, now))
	}
	return strings.Join(lines, "\n")
}

func (b *Browser) toolbar(f Frame) string {
	s := f.S
	if b.input.active {
		return b.input.view(s, f.Width)
	}
	var search string
	switch {
	case b.searching:
		search = s.Accent.Render("/ ") + s.Text.Render(b.query) + s.Accent.Render("▏")
	case b.query != "":
		search = s.Accent.Render("/ ") + s.Text.Render(b.query)
	default:
		search = s.Muted.Render("/ " + i18n.T("tui.browser.search"))
	}
	searchW := min(max(20, f.Width/3), width(search)+2)
	if b.searching {
		searchW = max(searchW, f.Width/3)
	}
	f.Zone("search", 0, 0, searchW+1, 1)
	label := i18n.T("tui.filter." + b.filterKey())
	if b.ids != nil {
		label = b.idLabel
	}
	chip := s.Hint.Render("[" + label + "]")
	left := " " + fit(search, searchW, s.G.Ellipsis) + " " + chip
	f.Zone("filter", 1+searchW+1, 0, width(chip), 1)

	count := i18n.T("tui.browser.count", "shown", num(len(b.rows)), "total", num(len(b.all)))
	if b.note != "" {
		st := s.OK
		if b.noteErr {
			st = s.Warn
		}
		count = st.Render(b.note)
	} else if n := len(b.marks); n > 0 {
		count = s.Marked.Render(i18n.T("tui.browser.marked", "n", num(n))) + s.Muted.Render(s.G.Sep) + s.Muted.Render(count)
	} else {
		count = s.Muted.Render(count)
	}
	return spread(left, count+" ", f.Width, s.G.Ellipsis)
}

func (b *Browser) row(f Frame, r core.Repo, current bool, cols, widths []int, now time.Time) string {
	s := f.S
	cursor, mark := " ", " "
	if current {
		cursor = s.Cursor.Render(s.G.Cursor)
	}
	switch {
	case b.marks[r.ID]:
		mark = s.Marked.Render(s.G.Mark)
	case r.Favourite:
		mark = s.Hint.Render(s.G.Fav)
	}
	out := cursor + mark + " "
	nameStyle := s.Text
	if current {
		nameStyle = s.Selected
	}
	for i, c := range cols {
		w := widths[i]
		var cell string
		switch columns[c].key {
		case "name":
			cell = nameStyle.Render(fit(r.Path, w, s.G.Ellipsis))
		case "owner":
			cell = s.Muted.Render(fit(r.Owner, w, s.G.Ellipsis))
		case "provider":
			cell = s.Muted.Render(fit(r.Provider, w, s.G.Ellipsis))
		case "status":
			cell = b.badge(s, r, w)
		case "synced":
			cell = s.Muted.Render(fitRight(synced(r, now), w-1, s.G.Ellipsis)) + " "
		}
		out += cell + " "
	}
	return out
}

// badge renders the status column: a glyph and a short label.
func (b *Browser) badge(s *Styles, r core.Repo, w int) string {
	key := statusKey(r)
	label := i18n.T("tui.status." + key)
	var g string
	var st lipgloss.Style
	switch {
	case key == "missing" || key == syncer.StatusFailed:
		g, st = s.G.Fail, s.Fail
	case key == "unknown":
		g, st = s.G.Unchanged, s.Muted
	case needsLook[key]:
		g, st = s.G.Skip, s.Warn
	default:
		g, st = s.G.OK, s.OK
	}
	return st.Render(fit(g+" "+label, w, s.G.Ellipsis))
}

func synced(r core.Repo, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, r.LastSyncedAt)
	if err != nil {
		return i18n.T("tui.never")
	}
	return ago(t, now)
}
