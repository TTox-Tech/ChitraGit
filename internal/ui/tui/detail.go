package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Tabs of the detail page (PRD F8).
var detailTabs = []string{"readme", "commits", "refs", "activity", "info"}

// Detail is one repo's page. It is reached from the browser with enter or
// a double click, or from the activity feed, and has no tab of its own.
type Detail struct {
	repo  *core.Repo
	info  *store.RepoInfo
	git   *RepoGit
	gitOK bool // git finished loading, with or without error
	err   error
	hist  []store.RepoRun
	feed  []store.Activity

	tab    int
	scroll []int
	height int // content rows in the last frame, for paging

	input lineInput
	note  string
	noteE bool

	// The rendered README, cached for one width.
	readme      []string
	readmeWidth int
}

func NewDetail() *Detail { return &Detail{scroll: make([]int, len(detailTabs))} }

func (d *Detail) Title() string     { return "" }
func (d *Detail) Init(*Env) tea.Cmd { return nil }
func (d *Detail) Capturing() bool   { return d.input.active }
func (d *Detail) Animating() bool   { return d.repo != nil && !d.gitOK }

func (d *Detail) Keys() []KeyHint {
	if d.input.active {
		return []KeyHint{{"enter", i18n.T("tui.detail.key.save")}, {"esc", i18n.T("tui.key.clear_search")}}
	}
	return []KeyHint{
		{"esc", i18n.T("tui.key.back")}, {"←→", i18n.T("tui.detail.key.tabs")}, {"↑↓", i18n.T("tui.key.move")},
		{"s", i18n.T("tui.key.sync")}, {"*", i18n.T("tui.detail.key.fav")}, {"t", i18n.T("tui.detail.key.tag")},
		{"o", i18n.T("tui.detail.key.open")},
	}
}

type detailInfoMsg struct {
	id   string
	info *store.RepoInfo
	hist []store.RepoRun
	feed []store.Activity
	err  error
}

type detailGitMsg struct {
	id  string
	git *RepoGit
	err error
}

func (d *Detail) load(env *Env, withGit bool) tea.Cmd {
	in := env.insights()
	if in == nil || d.repo == nil {
		d.gitOK = true
		return nil
	}
	id, r := d.repo.ID, *d.repo
	cmds := []tea.Cmd{func() tea.Msg {
		m := detailInfoMsg{id: id}
		if m.info, m.err = in.RepoInfo(env.Ctx, id); m.err != nil {
			return m
		}
		if m.hist, m.err = in.RepoHistory(env.Ctx, id); m.err != nil {
			return m
		}
		m.feed, m.err = in.Activity(env.Ctx, store.ActivityFilter{RepoID: id, Limit: 100})
		return m
	}}
	if withGit {
		d.gitOK = false
		cmds = append(cmds, func() tea.Msg {
			g, err := in.RepoGit(env.Ctx, r)
			return detailGitMsg{id: id, git: g, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (d *Detail) open(env *Env, r core.Repo, tab string) tea.Cmd {
	same := d.repo != nil && d.repo.ID == r.ID
	d.repo = &r
	if i := slices.Index(detailTabs, tab); i >= 0 {
		d.tab = i
	}
	if same {
		return d.load(env, false)
	}
	d.info, d.git, d.err, d.hist, d.feed, d.note = nil, nil, nil, nil, nil, ""
	d.readme, d.readmeWidth = nil, 0
	d.scroll = make([]int, len(detailTabs))
	if tab == "" {
		d.tab = 0
	}
	return d.load(env, true)
}

func (d *Detail) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		switch p := msg.Payload.(type) {
		case core.Repo:
			return d.open(env, p, "")
		case OpenDetail:
			if in := env.insights(); in != nil {
				id := p.ID
				return func() tea.Msg {
					info, err := in.RepoInfo(env.Ctx, id)
					if err != nil {
						return ackMsg{err: err}
					}
					return NavigateMsg{To: ScreenDetail, Payload: openResolved{info.Repo, p.Tab}}
				}
			}
		case openResolved:
			return d.open(env, p.repo, p.tab)
		}
	case detailInfoMsg:
		if d.repo == nil || msg.id != d.repo.ID {
			return nil
		}
		if msg.err != nil {
			d.err = msg.err
			return nil
		}
		d.info, d.hist, d.feed = msg.info, msg.hist, msg.feed
		d.repo = &d.info.Repo
	case detailGitMsg:
		if d.repo == nil || msg.id != d.repo.ID {
			return nil
		}
		d.gitOK = true
		d.git = msg.git
		if msg.err != nil {
			d.git = &RepoGit{}
			d.note, d.noteE = i18n.T("show.git_error")+" "+msg.err.Error(), true
		}
		d.readme, d.readmeWidth = nil, 0
		// With no README, open on the commits instead of an empty page.
		if d.tab == 0 && d.git.Readme == nil {
			d.tab = 1
		}
	case ReposChangedMsg:
		if d.repo != nil {
			return d.load(env, false)
		}
	case ackMsg:
		if msg.err != nil {
			d.note, d.noteE = msg.err.Error(), true
		} else {
			d.note, d.noteE = msg.text, false
		}
	case tea.KeyMsg:
		if d.input.active {
			return d.input.key(msg)
		}
		return d.key(msg, env)
	case ZoneClick:
		return d.click(msg, env)
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			d.scrollBy(-3)
		case tea.MouseButtonWheelDown:
			d.scrollBy(3)
		}
	}
	return nil
}

// openResolved is OpenDetail after the repo was looked up.
type openResolved struct {
	repo core.Repo
	tab  string
}

func (d *Detail) key(k tea.KeyMsg, env *Env) tea.Cmd {
	page := max(1, d.height-1)
	switch k.String() {
	case "esc", "backspace", "b":
		return Navigate(ScreenBrowser, nil)
	case "right", "l", "]":
		d.tab = (d.tab + 1) % len(detailTabs)
	case "left", "h", "[":
		d.tab = (d.tab + len(detailTabs) - 1) % len(detailTabs)
	case "up", "k":
		d.scrollBy(-1)
	case "down", "j":
		d.scrollBy(1)
	case "pgup", "ctrl+u":
		d.scrollBy(-page)
	case "pgdown", "ctrl+d", " ":
		d.scrollBy(page)
	case "home", "g":
		d.scroll[d.tab] = 0
	case "end", "G":
		d.scroll[d.tab] = 1 << 30
	default:
		return d.action(k.String(), env)
	}
	return nil
}

// action runs one of the page's actions; buttons and keys share it.
func (d *Detail) action(name string, env *Env) tea.Cmd {
	if d.repo == nil {
		return nil
	}
	r := *d.repo
	in := env.insights()
	switch name {
	case "s":
		return Navigate(ScreenSync, SyncStart{IDs: []string{r.ID}})
	case "*":
		if in == nil {
			return nil
		}
		fav := !r.Favourite
		text := i18n.T("tui.detail.fav_on")
		if !fav {
			text = i18n.T("tui.detail.fav_off")
		}
		return changed(text, func() error { return in.SetFavourite(env.Ctx, []string{r.ID}, fav) })
	case "t", "T":
		if in == nil {
			return nil
		}
		label, remove := i18n.T("tui.detail.add_tags"), name == "T"
		if remove {
			label = i18n.T("tui.detail.remove_tags")
		}
		d.input.start(label, func(v string) tea.Cmd {
			tags := splitTags(v)
			if remove {
				return changed(i18n.T("tui.detail.tags_removed"), func() error { return in.RemoveTags(env.Ctx, []string{r.ID}, tags) })
			}
			return changed(i18n.T("tui.detail.tags_added"), func() error { return in.AddTags(env.Ctx, []string{r.ID}, tags) })
		})
	case "o":
		if in != nil {
			return openCmd(in, in.RepoDir(r))
		}
	case "u":
		if in != nil {
			if u := d.upstreamURL(); u != "" {
				return openCmd(in, u)
			}
		}
	}
	return nil
}

func openCmd(in Insights, target string) tea.Cmd {
	return func() tea.Msg {
		if err := in.Open(target); err != nil {
			return ackMsg{err: err}
		}
		return ackMsg{text: i18n.T("tui.detail.opened", "target", target)}
	}
}

// upstreamURL is the repo's web page, for hosts that have one.
func (d *Detail) upstreamURL() string {
	if d.info == nil {
		return ""
	}
	u, ok := d.info.Remotes["origin"]
	if !ok {
		for _, v := range d.info.Remotes {
			u = v
			break
		}
	}
	rem := provider.Parse(u)
	if rem.Host == "" || rem.Owner == "" || rem.Name == "" {
		return ""
	}
	return "https://" + rem.Host + "/" + rem.Owner + "/" + rem.Name
}

func (d *Detail) click(c ZoneClick, env *Env) tea.Cmd {
	switch {
	case c.ID == "back":
		return Navigate(ScreenBrowser, nil)
	case strings.HasPrefix(c.ID, "dtab:"):
		d.tab, _ = strconv.Atoi(strings.TrimPrefix(c.ID, "dtab:"))
	case strings.HasPrefix(c.ID, "act:"):
		return d.action(strings.TrimPrefix(c.ID, "act:"), env)
	}
	return nil
}

func (d *Detail) scrollBy(n int) { d.scroll[d.tab] = max(0, d.scroll[d.tab]+n) }

func (d *Detail) View(f Frame) string {
	s := f.S
	if d.repo == nil {
		return ""
	}
	r := *d.repo
	in := f.Env.insights()

	// Line 0: back, title, status.
	back := s.button(i18n.T("tui.key.back"), false)
	f.Zone("back", 1, 0, width(back), 1)
	title := r.Path
	if r.Favourite {
		title = s.G.Fav + " " + title
	}
	head := " " + back + "  " + s.Title.Render(title) + "  " + (&Browser{}).badge(s, r, 24)
	lines := []string{fit(head, f.Width, s.G.Ellipsis)}

	// Line 1: actions. Only offered when the backend can do them.
	if in != nil {
		x := 1
		var row strings.Builder
		row.WriteString(" ")
		acts := []struct{ key, label string }{
			{"s", i18n.T("tui.detail.act.sync")},
			{"*", map[bool]string{false: i18n.T("tui.detail.act.fav"), true: i18n.T("tui.detail.act.unfav")}[r.Favourite]},
			{"t", i18n.T("tui.detail.act.tag")},
			{"o", i18n.T("tui.detail.act.folder")},
		}
		if d.upstreamURL() != "" {
			acts = append(acts, struct{ key, label string }{"u", i18n.T("tui.detail.act.upstream")})
		}
		for _, a := range acts {
			b := s.button(a.label, false)
			if x+width(b) > f.Width {
				break
			}
			f.Zone("act:"+a.key, x, 1, width(b), 1)
			row.WriteString(b + " ")
			x += width(b) + 1
		}
		lines = append(lines, row.String())
	} else {
		lines = append(lines, "")
	}

	// Line 2: tabs.
	x := 1
	var tabs strings.Builder
	tabs.WriteString(" ")
	for i, t := range detailTabs {
		// Brackets mark the active tab even without colours.
		label := " " + i18n.T("tui.detail.tab."+t) + " "
		st := s.Tab
		if i == d.tab {
			st = s.TabActive
			label = "[" + strings.TrimSpace(label) + "]"
		}
		f.Zone("dtab:"+strconv.Itoa(i), x, 2, width(label), 1)
		tabs.WriteString(st.Render(label) + " ")
		x += width(label) + 1
	}
	lines = append(lines, tabs.String(), s.rule(f.Width))

	bottom := ""
	switch {
	case d.input.active:
		bottom = d.input.view(s, f.Width)
	case d.note != "":
		st := s.Muted
		if d.noteE {
			st = s.Warn
		}
		bottom = " " + st.Render(d.note)
	}
	d.height = f.Height - len(lines)
	if bottom != "" {
		d.height--
	}
	body := d.content(f)
	off := max(0, min(d.scroll[d.tab], len(body)-d.height))
	d.scroll[d.tab] = off
	end := min(len(body), off+d.height)
	lines = append(lines, body[off:end]...)
	out := block(lines, f.Width, f.Height-btoi(bottom != ""), s.G.Ellipsis)
	if bottom != "" {
		out += "\n" + fit(bottom, f.Width, s.G.Ellipsis)
	}
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// content renders the active tab as lines.
func (d *Detail) content(f Frame) []string {
	s := f.S
	if d.err != nil {
		return []string{"", "  " + s.Fail.Render(s.G.Fail+" "+d.err.Error())}
	}
	tab := detailTabs[d.tab]
	if tab != "info" && tab != "activity" && f.Env.insights() != nil && !d.gitOK {
		return []string{"", "  " + s.Accent.Render(f.Anim.Spinner()) + " " + s.Muted.Render(i18n.T("tui.detail.loading"))}
	}
	switch tab {
	case "readme":
		return d.readmeLines(f)
	case "commits":
		return d.commitLines(f)
	case "refs":
		return d.refLines(f)
	case "activity":
		return d.activityLines(f)
	}
	return d.infoLines(f)
}

func (d *Detail) empty(s *Styles, key string) []string {
	return []string{"", "  " + s.Muted.Render(i18n.T(key))}
}

func (d *Detail) readmeLines(f Frame) []string {
	s := f.S
	if d.git == nil || d.git.Readme == nil {
		return d.empty(s, "tui.detail.no_readme")
	}
	w := max(20, min(f.Width-4, 100))
	if d.readme != nil && d.readmeWidth == w {
		return d.readme
	}
	rd := d.git.Readme
	var text string
	if rd.Markdown {
		text = renderMarkdown(rd.Text, w)
	} else {
		text = lipgloss.NewStyle().Width(w).Render(rd.Text)
	}
	out := []string{" " + s.Muted.Render(rd.Name)}
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, "  "+l)
	}
	if rd.Truncated {
		out = append(out, "", "  "+s.Muted.Render(i18n.T("tui.detail.readme_cut")))
	}
	d.readme, d.readmeWidth = out, w
	return out
}

// renderMarkdown renders with Glamour, matching the terminal: no colours
// when colours are off, otherwise a dark or light style.
func renderMarkdown(md string, w int) string {
	style := "dark"
	switch {
	case lipgloss.ColorProfile() == termenv.Ascii:
		style = "notty"
	case !lipgloss.HasDarkBackground():
		style = "light"
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(w),
		glamour.WithEmoji())
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

func (d *Detail) commitLines(f Frame) []string {
	s := f.S
	if d.git == nil || len(d.git.Commits) == 0 {
		return d.empty(s, "tui.detail.no_commits")
	}
	now := f.Env.Now()
	var out []string
	for _, c := range d.git.Commits {
		line := " " + s.Hint.Render(c.Graph)
		if c.SHA != "" {
			line += " " + s.Muted.Render(c.SHA[:min(8, len(c.SHA))]) + " "
			if len(c.Refs) > 0 {
				line += s.Accent.Render("("+strings.Join(c.Refs, ", ")+")") + " "
			}
			line += s.Text.Render(c.Subject)
			meta := c.Author
			if t, err := time.Parse(time.RFC3339, c.Date); err == nil {
				meta += s.G.Sep + ago(t, now)
			}
			if !f.Compact {
				line = spread(fit(line, f.Width-width(meta)-3, s.G.Ellipsis), s.Muted.Render(meta)+" ", f.Width, s.G.Ellipsis)
			}
		}
		out = append(out, line)
	}
	return out
}

func (d *Detail) refLines(f Frame) []string {
	s := f.S
	if d.git == nil || len(d.git.Refs) == 0 {
		return d.empty(s, "tui.detail.no_refs")
	}
	now := f.Env.Now()
	var out []string
	kind := ""
	for _, r := range d.git.Refs {
		if r.Kind != kind {
			if kind != "" {
				out = append(out, "")
			}
			kind = r.Kind
			out = append(out, " "+s.Header.Render(i18n.T("tui.detail.refs."+kind)))
		}
		mark := "  "
		if r.Current {
			mark = s.Cursor.Render(s.G.Bullet) + " "
		}
		line := "  " + mark + s.Label.Render(r.Name) + " " + s.Muted.Render(r.SHA[:min(8, len(r.SHA))])
		if r.Upstream != "" {
			line += s.Muted.Render(" → " + r.Upstream)
		}
		if r.Track != "" {
			line += " " + s.Warn.Render("["+r.Track+"]")
		}
		if t, err := time.Parse(time.RFC3339, r.Date); err == nil {
			line = spread(line, s.Muted.Render(ago(t, now))+" ", f.Width, s.G.Ellipsis)
		}
		out = append(out, line)
	}
	return out
}

func (d *Detail) activityLines(f Frame) []string {
	s := f.S
	if len(d.feed) == 0 && len(d.hist) == 0 {
		return d.empty(s, "tui.detail.no_activity")
	}
	now := f.Env.Now()
	var out []string
	if len(d.feed) > 0 {
		out = append(out, " "+s.Header.Render(i18n.T("tui.detail.changes")))
		for _, e := range d.feed {
			out = append(out, activityLine(f, e, now, false))
		}
		out = append(out, "")
	}
	if len(d.hist) > 0 {
		out = append(out, " "+s.Header.Render(i18n.T("tui.detail.runs")))
		for _, h := range d.hist {
			g, st := s.outcome(h.Outcome)
			line := "  " + st.Render(g) + " " + s.Muted.Render("#"+strconv.FormatInt(h.RunID, 10)) + " " +
				s.Text.Render(i18n.T("tui.count."+h.Outcome)) + " " + s.Muted.Render(h.Message)
			if t, err := time.Parse(time.RFC3339Nano, h.FinishedAt); err == nil {
				line = spread(line, s.Muted.Render(ago(t, now))+" ", f.Width, s.G.Ellipsis)
			}
			out = append(out, line)
		}
	}
	return out
}

func (d *Detail) infoLines(f Frame) []string {
	s := f.S
	r := *d.repo
	var out []string
	field := func(key, val string) {
		if val != "" {
			out = append(out, " "+s.Muted.Render(fit(i18n.T("tui.detail."+key), 16, s.G.Ellipsis))+s.Text.Render(val))
		}
	}
	now := f.Env.Now()
	when := func(ts string) string {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return ago(t, now)
		}
		return ""
	}
	out = append(out, "")
	field("status", (&Browser{}).badge(s, r, f.Width-20))
	field("reason", r.StatusReason)
	field("synced", synced(r, now))
	field("kind", r.Kind)
	field("provider", r.Provider)
	if r.Owner != "" || r.Name != "" {
		field("upstream", strings.Trim(r.Host+"/"+r.Owner+"/"+r.Name, "/"))
	}
	if in := d.info; in != nil {
		names := make([]string, 0, len(in.Remotes))
		for n := range in.Remotes {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			field("remote", n+"  "+in.Remotes[n])
		}
		field("tags", strings.Join(in.Tags, ", "))
		field("activity", when(in.LastUpstreamActivityAt))
		if in.SizeBytes > 0 {
			field("size", humanBytes(in.SizeBytes))
		}
		if in.Locked {
			field("locked", i18n.T("show.locked"))
		}
		field("first_seen", when(in.FirstSeenAt))
		if g := d.git; g != nil && g.Readme != nil {
			field("readme", g.Readme.Name)
		}
	}
	field("id", r.ID)
	if in := d.info; in != nil {
		if in.Rescue != nil {
			msg := in.Rescue.Reason
			if in.Rescue.RescuedAt == "" {
				msg += s.G.Sep + i18n.T("show.rescue_pending", "count", in.Rescue.Confirmations)
			}
			out = append(out, "", " "+s.Hint.Render(s.G.Rescued+" "+i18n.T("show.rescued")+": "+msg))
		}
		if in.Conflict != nil {
			out = append(out, "", " "+s.Warn.Render(s.G.Skip+" "+i18n.T("show.conflict", "files", strings.Join(in.Conflict.Files, ", "))))
		}
	}
	return out
}

// activityLine renders one feed entry. withRepo adds the repo name.
func activityLine(f Frame, e store.Activity, now time.Time, withRepo bool) string {
	s := f.S
	g, st := s.G.Bullet, s.Muted
	msg := e.Message
	switch e.Type {
	case store.ActivityRefs:
		g, st = s.G.Mark, s.OK
		msg = engine.DescribeActivity(e.Changes)
	case "rescued":
		g, st = s.G.Rescued, s.Hint
	case "renamed":
		g, st = "↻", s.Accent
		if s.G.Rule == "-" {
			g = "~"
		}
	case "conflict", "failed":
		g, st = s.G.Skip, s.Warn
	}
	if msg == "" {
		msg = e.Type
	}
	line := "  " + st.Render(g) + " "
	if withRepo {
		name := e.Path
		if name == "" {
			name = "-"
		}
		nw := min(32, max(12, f.Width/3))
		line += s.Label.Render(fit(name, nw, s.G.Ellipsis)) + " "
	}
	line += s.Text.Render(msg)
	if t, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
		line = spread(fit(line, f.Width-10, s.G.Ellipsis), s.Muted.Render(clockTime(t, now))+" ", f.Width, s.G.Ellipsis)
	}
	return line
}

// clockTime is the time of day, e.g. 14:05, in now's time zone.
func clockTime(t, now time.Time) string { return t.In(now.Location()).Format("15:04") }

// humanBytes renders 1536 as "1.5 KiB".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "iB"
}
