package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// Minimum terminal size (PRD F18): below it the app asks for a bigger
// window; under compactWidth screens use a compact layout.
const (
	minWidth     = 60
	minHeight    = 20
	compactWidth = 100
	// chrome is the rows the shell uses: header, two rules and the footer.
	chrome = 4
	// doubleClick is the longest gap between two clicks of a double click.
	doubleClick = 400 * time.Millisecond
)

// App is the Bubble Tea model: header with clickable tabs, the active
// screen's body, a footer of key hints, and dialogs on top.
type App struct {
	env   *Env
	s     *Styles
	anim  *Anim
	zones Zones

	screens map[ScreenID]Screen
	tabs    []ScreenID
	active  ScreenID
	prev    ScreenID

	width, height int
	dialog        *Dialog
	help          bool
	splash        int // ticks left of the startup reveal
	ticking       bool

	// page turn: the previous body is replaced line by line.
	turnFrom []string
	turn     int

	lastClick struct {
		id string
		at time.Time
	}
	quitting bool // waiting for a busy screen to stop, then quit
	ctrlC    int
}

// NewApp builds the shell. Register screens before running it.
func NewApp(env *Env, s *Styles, anim *Anim) *App {
	a := &App{env: env, s: s, anim: anim, screens: map[ScreenID]Screen{}}
	env.app = a
	if env.gone == nil {
		env.gone = make(chan struct{})
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if anim.Level == LevelFull {
		a.splash = splashTicks
	}
	return a
}

// Register adds a screen. Screens with a title get a tab, in the order
// they are registered. The first registered screen is shown first.
func (a *App) Register(id ScreenID, s Screen) {
	if _, ok := a.screens[id]; !ok && s.Title() != "" {
		a.tabs = append(a.tabs, id)
	}
	a.screens[id] = s
	if a.active == "" {
		a.active = id
	}
}

// Init starts every screen and checks for an interrupted sync.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.checkInterrupted()}
	for _, id := range a.order() {
		cmds = append(cmds, a.screens[id].Init(a.env))
	}
	if a.needsTick() {
		a.ticking = true
		cmds = append(cmds, a.tick())
	}
	return tea.Batch(cmds...)
}

// order lists screens with tabs first, then the rest, for stable dispatch.
func (a *App) order() []ScreenID {
	ids := append([]ScreenID(nil), a.tabs...)
	for id, s := range a.screens {
		if s.Title() == "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(a.anim.interval(), func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) needsTick() bool {
	if a.anim.Level == LevelOff {
		return false
	}
	if a.splash > 0 || a.turnFrom != nil {
		return true
	}
	if an, ok := a.screens[a.active].(animator); ok && an.Animating() {
		return true
	}
	return false
}

// Update routes messages. Keys and mouse go to the dialog, the help
// overlay or the active screen; everything else goes to every screen.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.update(msg)
	if a.quitting && !a.busy() {
		return a, tea.Quit
	}
	if !a.ticking && a.needsTick() {
		a.ticking = true
		cmd = tea.Batch(cmd, a.tick())
	}
	return a, cmd
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return nil

	case tickMsg:
		a.anim.Tick++
		if a.splash > 0 {
			a.splash--
		}
		if a.turnFrom != nil {
			if a.turn++; a.turn >= 4 {
				a.turnFrom = nil
			}
		}
		if a.needsTick() {
			return a.tick()
		}
		a.ticking = false
		return nil

	case tea.KeyMsg:
		return a.key(msg)

	case tea.MouseMsg:
		return a.mouse(msg)

	case NavigateMsg:
		return a.navigate(msg)

	case openDialogMsg:
		a.dialog = msg.d
		return nil

	case interruptedMsg:
		return a.offerResume(msg)
	}
	return a.broadcast(msg)
}

func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range a.order() {
		cmds = append(cmds, a.screens[id].Update(msg, a.env))
	}
	return tea.Batch(cmds...)
}

func (a *App) key(k tea.KeyMsg) tea.Cmd {
	if a.splash > 0 {
		a.splash = 0
		return nil
	}
	if k.Type == tea.KeyCtrlC {
		return a.quit(true)
	}
	if a.dialog != nil {
		d := a.dialog
		cmd, done := d.key(k)
		if done && a.dialog == d {
			a.dialog = nil
		}
		return cmd
	}
	if a.help {
		a.help = false
		return nil
	}
	scr := a.screens[a.active]
	if c, ok := scr.(capturer); ok && c.Capturing() {
		return scr.Update(k, a.env)
	}
	switch k.String() {
	case "q":
		return a.quit(false)
	case "?":
		a.help = true
		return nil
	case "tab":
		return a.cycleTab(1)
	case "shift+tab":
		return a.cycleTab(-1)
	}
	if n, err := strconv.Atoi(k.String()); err == nil && n >= 1 && n <= len(a.tabs) {
		return Navigate(a.tabs[n-1], nil)
	}
	return scr.Update(k, a.env)
}

func (a *App) cycleTab(step int) tea.Cmd {
	if len(a.tabs) == 0 {
		return nil
	}
	i := 0
	for j, id := range a.tabs {
		if id == a.active {
			i = j
		}
	}
	return Navigate(a.tabs[(i+step+len(a.tabs))%len(a.tabs)], nil)
}

func (a *App) mouse(m tea.MouseMsg) tea.Cmd {
	if a.splash > 0 {
		if m.Action == tea.MouseActionPress {
			a.splash = 0
		}
		return nil
	}
	if tea.MouseEvent(m).IsWheel() {
		if a.dialog != nil || a.help {
			return nil
		}
		m.Y -= 2 // body coordinates
		return a.screens[a.active].Update(m, a.env)
	}
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	if a.help {
		a.help = false
		return nil
	}
	z, ok := a.zones.hit(m.X, m.Y)
	if !ok {
		return nil
	}
	now := a.env.Now()
	click := ZoneClick{ID: z.id, X: m.X - z.x, Y: m.Y - z.y, Shift: m.Shift,
		Double: a.lastClick.id == z.id && now.Sub(a.lastClick.at) < doubleClick}
	a.lastClick.id, a.lastClick.at = z.id, now
	if click.Double {
		a.lastClick.id = "" // a third click starts over
	}

	if a.dialog != nil {
		i, err := strconv.Atoi(strings.TrimPrefix(z.id, "dlg:"))
		if !strings.HasPrefix(z.id, "dlg:") || err != nil {
			return nil
		}
		d := a.dialog
		a.dialog = nil
		return d.choose(i)
	}
	if id, ok := strings.CutPrefix(z.id, "tab:"); ok {
		return Navigate(ScreenID(id), nil)
	}
	return a.screens[a.active].Update(click, a.env)
}

func (a *App) navigate(n NavigateMsg) tea.Cmd {
	scr, ok := a.screens[n.To]
	if !ok {
		return nil
	}
	if n.To != a.active {
		if a.anim.Level == LevelFull && a.width > 0 {
			a.turnFrom = strings.Split(a.body(), "\n")
			a.turn = 0
		}
		a.prev, a.active = a.active, n.To
	}
	return scr.Update(n, a.env)
}

// quit stops busy screens safely before quitting. A second Ctrl+C quits
// at once, like the CLI: the journal and crash recovery cover the rest.
func (a *App) quit(ctrlC bool) tea.Cmd {
	if ctrlC {
		a.ctrlC++
		if a.ctrlC > 1 {
			return tea.Quit
		}
	}
	if !a.busy() {
		return tea.Quit
	}
	if ctrlC || a.quitting {
		return a.stopAll()
	}
	a.dialog = &Dialog{
		Title:   i18n.T("tui.quit.title"),
		Lines:   []string{i18n.T("tui.quit.busy")},
		Buttons: []string{i18n.T("tui.quit.stop"), i18n.T("tui.quit.keep")},
		Cancel:  1,
		OnChoose: func(i int) tea.Cmd {
			if i == 0 {
				return a.stopAll()
			}
			return nil
		},
	}
	return nil
}

func (a *App) stopAll() tea.Cmd {
	a.quitting = true
	var cmds []tea.Cmd
	for _, s := range a.screens {
		if b, ok := s.(busyScreen); ok && b.Busy() {
			cmds = append(cmds, b.Stop())
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) busy() bool {
	for _, s := range a.screens {
		if b, ok := s.(busyScreen); ok && b.Busy() {
			return true
		}
	}
	return false
}

// View draws the whole terminal.
func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return ""
	}
	start := time.Now()
	a.zones.reset()
	var out string
	switch {
	case a.width < minWidth || a.height < minHeight:
		out = a.tooSmall()
	case a.splash > 0:
		out = a.splashView()
	default:
		out = a.frameView()
	}
	out = a.s.paint(out)
	a.anim.observe(time.Since(start))
	return out
}

func (a *App) frame() Frame {
	return Frame{Width: a.width, Height: a.height - chrome, Compact: a.width < compactWidth,
		S: a.s, Anim: a.anim, Env: a.env, zones: &a.zones, x0: 0, y0: 2}
}

func (a *App) body() string {
	f := a.frame()
	return block(strings.Split(a.screens[a.active].View(f), "\n"), f.Width, f.Height, a.s.G.Ellipsis)
}

func (a *App) frameView() string {
	f := a.frame()
	body := a.body()
	if a.turnFrom != nil {
		body = a.pageTurn(body, f)
	}
	if a.help {
		body = a.helpView(body, f)
	} else if a.dialog != nil {
		body = a.dialog.render(body, f)
	}
	return strings.Join([]string{a.header(), a.s.rule(a.width), body, a.s.rule(a.width), a.footer()}, "\n")
}

// pageTurn shows the new page sliding down over the old one, with a
// ledger rule at the edge.
func (a *App) pageTurn(body string, f Frame) string {
	lines := strings.Split(body, "\n")
	edge := (a.turn + 1) * f.Height / 4
	for i := range lines {
		switch {
		case i == edge:
			lines[i] = a.s.Accent.Render(strings.Repeat(a.s.G.Rule, f.Width))
		case i > edge && i < len(a.turnFrom):
			lines[i] = a.s.Muted.Render(fit(a.turnFrom[i], f.Width, a.s.G.Ellipsis))
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) header() string {
	s := a.s
	left := s.Brand.Render(" ChitraGit ")
	x := width(left)
	for i, id := range a.tabs {
		label := " " + strconv.Itoa(i+1) + " " + a.screens[id].Title() + " "
		style := s.Tab
		if id == a.active {
			style = s.TabActive
		}
		tab := style.Render(label)
		a.zones.add("tab:"+string(id), x, 0, width(tab), 1)
		left += tab
		x += width(tab)
	}
	right := s.Muted.Render(truncate(a.env.Archive, max(0, a.width-x-3), s.G.Ellipsis) + " ")
	return spread(left, right, a.width, s.G.Ellipsis)
}

func (a *App) footer() string {
	s := a.s
	hints := a.screens[a.active].Keys()
	hints = append(hints, KeyHint{"tab", i18n.T("tui.key.screens")}, KeyHint{"?", i18n.T("tui.key.help")},
		KeyHint{"q", i18n.T("tui.key.quit")})
	var parts []string
	for _, h := range hints {
		parts = append(parts, s.Key.Render(h.Key)+" "+s.Muted.Render(h.Label))
	}
	return fit(" "+strings.Join(parts, "  "), a.width, s.G.Ellipsis)
}

func (a *App) helpView(body string, f Frame) string {
	var lines []string
	add := func(h KeyHint) { lines = append(lines, fit(h.Key, 12, "")+h.Label) }
	for _, h := range a.screens[a.active].Keys() {
		add(h)
	}
	for _, h := range []KeyHint{
		{"tab / 1-9", i18n.T("tui.help.screens")},
		{"mouse", i18n.T("tui.help.mouse")},
		{"?", i18n.T("tui.help.close")},
		{"q", i18n.T("tui.key.quit")},
	} {
		add(h)
	}
	d := &Dialog{Title: i18n.T("tui.help.title"), Lines: lines, Buttons: []string{i18n.T("tui.help.ok")}}
	return d.render(body, f)
}

func (a *App) tooSmall() string {
	msg := i18n.T("tui.too_small", "w", minWidth, "h", minHeight, "cw", a.width, "ch", a.height)
	lines := make([]string, a.height)
	for i, l := range wrap(msg, a.width) {
		if r := a.height/2 - 1 + i; r >= 0 && r < a.height {
			lines[r] = a.s.Warn.Render(center(l, a.width))
		}
	}
	return block(lines, a.width, a.height, a.s.G.Ellipsis)
}

// splashView reveals the logo line by line, then the tagline: the kalam
// writing the ledger's name.
func (a *App) splashView() string {
	shown := splashTicks - a.splash
	lines := make([]string, a.height)
	top := max(0, a.height/2-len(logo)/2-1)
	for i, l := range logo {
		if i*2 < shown && top+i < a.height {
			n := min(width(l), (shown-i*2)*8)
			lines[top+i] = a.s.Brand.Render(center(fit(l[:n], width(l), ""), a.width))
		}
	}
	if shown > len(logo)*2 && top+len(logo)+1 < a.height {
		lines[top+len(logo)+1] = a.s.Hint.Render(center(i18n.T("app.tagline"), a.width))
	}
	return block(lines, a.width, a.height, a.s.G.Ellipsis)
}
