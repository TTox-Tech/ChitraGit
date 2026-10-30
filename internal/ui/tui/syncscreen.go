package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

type syncState int

const (
	syncIdle     syncState = iota
	syncChecking           // asking whether the last run was interrupted
	syncRunning
	syncStopping // cancel requested; repos in flight finish their step
	syncDone
)

// ledgerKeep bounds the in-memory ledger; counts cover the whole run.
const ledgerKeep = 500

// SyncScreen runs a sync and shows it live (PRD F11): an overall bar,
// one lane per repo in flight with its phase, and a ledger where every
// finished repo is written as an entry.
type SyncScreen struct {
	state  syncState
	req    SyncStart
	gen    int // run number; messages from older runs are ignored
	cancel context.CancelFunc
	gate   *gate
	events chan tea.Msg
	wg     *sync.WaitGroup

	scanned  *engine.ScanReport
	total    int
	finished int
	counts   map[string]int
	lanes    []lane
	ledger   []entry
	report   *engine.SyncReport
	err      error

	started, pausedAt time.Time
	pausedFor         time.Duration
	doneTick          int // anim tick when the run ended, for the rangoli
	focus             int
}

type lane struct {
	repo    core.Repo
	phase   string
	started time.Time
}

type entry struct {
	res  syncer.Result
	born int // anim tick when written, for the kalam effect
}

// NewSyncScreen returns an idle sync screen. wg tracks the running sync
// so the program can wait for it to finish writing before it exits.
func NewSyncScreen(wg *sync.WaitGroup) *SyncScreen {
	return &SyncScreen{wg: wg, counts: map[string]int{}}
}

// Messages from a running sync, tagged with the run they belong to.
type (
	syncUpdateMsg struct {
		gen int
		u   SyncUpdate
	}
	syncDoneMsg struct {
		gen    int
		report *engine.SyncReport
		err    error
	}
	interruptedMsg struct {
		run   *Interrupted
		start *SyncStart // set when the user was starting a sync
		err   error
	}
	discardedMsg struct{ err error }
)

func (m *SyncScreen) Title() string     { return i18n.T("tui.tab.sync") }
func (m *SyncScreen) Init(*Env) tea.Cmd { return nil }
func (m *SyncScreen) Busy() bool {
	return m.state == syncChecking || m.state == syncRunning || m.state == syncStopping
}
func (m *SyncScreen) paused() bool { return m.gate != nil && m.gate.isPaused() }
func (m *SyncScreen) clean() bool  { return m.counts[syncer.Failed]+m.counts[syncer.Skipped] == 0 }
func (m *SyncScreen) rangoliOn(a *Anim) bool {
	return m.state == syncDone && m.err == nil && m.report != nil && !m.report.Interrupted && m.clean() &&
		m.finished > 0 && a.Level == LevelFull
}

func (m *SyncScreen) Animating() bool {
	return m.Busy() || m.state == syncDone // the rangoli and the last entries settle
}

// Stop cancels the run gracefully: no new repos start, repos in flight
// finish their current step, and the journal records where it stopped.
func (m *SyncScreen) Stop() tea.Cmd {
	switch m.state {
	case syncChecking:
		m.state = syncIdle // the answer is ignored; nothing started
	case syncRunning:
		m.state = syncStopping
		m.cancel()
	}
	return nil
}

func (m *SyncScreen) Keys() []KeyHint {
	switch m.state {
	case syncRunning:
		pause := i18n.T("tui.sync.pause")
		if m.paused() {
			pause = i18n.T("tui.sync.resume")
		}
		return []KeyHint{{"p", pause}, {"c", i18n.T("tui.sync.stop")}}
	case syncDone:
		return []KeyHint{{"f", i18n.T("tui.sync.view_failures")}, {"b", i18n.T("tui.sync.back")}, {"s", i18n.T("tui.sync.again")}}
	case syncIdle:
		return []KeyHint{{"s", i18n.T("tui.sync.all")}}
	}
	return nil
}

func (m *SyncScreen) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		if req, ok := msg.Payload.(SyncStart); ok && !m.Busy() {
			return m.request(req, env)
		}
	case interruptedMsg:
		if msg.start != nil && m.state == syncChecking {
			return m.afterCheck(msg, env)
		}
	case syncUpdateMsg:
		if msg.gen == m.gen {
			m.apply(msg.u, env)
			return m.next()
		}
	case syncDoneMsg:
		if msg.gen == m.gen {
			return m.finish(msg, env)
		}
	case tea.KeyMsg:
		return m.key(msg.String(), env)
	case ZoneClick:
		if b, ok := strings.CutPrefix(msg.ID, "btn:"); ok {
			return m.key(b, env)
		}
	}
	return nil
}

func (m *SyncScreen) key(k string, env *Env) tea.Cmd {
	switch m.state {
	case syncIdle:
		if k == "s" || k == "enter" {
			return m.request(SyncStart{}, env)
		}
	case syncRunning:
		switch k {
		case "p", " ":
			m.togglePause(env)
		case "c":
			return m.Stop()
		}
	case syncDone:
		switch k {
		case "f":
			return Navigate(ScreenBrowser, BrowserFilter("failed"))
		case "b", "esc":
			return Navigate(ScreenBrowser, nil)
		case "s":
			return m.request(SyncStart{}, env)
		case "enter":
			return m.key([]string{"f", "b", "s"}[m.focus], env)
		case "left", "right", "tab":
			m.focus = (m.focus + map[bool]int{true: 2, false: 1}[k == "left"]) % 3
		}
	}
	return nil
}

// request starts a sync, first asking about an interrupted run unless the
// caller already decided.
func (m *SyncScreen) request(req SyncStart, env *Env) tea.Cmd {
	if req.OnInterrupted != "" {
		return m.start(req, env)
	}
	m.state = syncChecking
	m.err = nil
	return func() tea.Msg {
		run, err := env.Backend.Interrupted(env.Ctx)
		return interruptedMsg{run: run, start: &req, err: err}
	}
}

func (m *SyncScreen) afterCheck(msg interruptedMsg, env *Env) tea.Cmd {
	req := *msg.start
	if msg.err != nil {
		m.state, m.err = syncDone, msg.err
		return nil
	}
	if msg.run == nil {
		req.OnInterrupted = "fresh"
		return m.start(req, env)
	}
	m.state = syncIdle
	return OpenDialog(&Dialog{
		Title:   i18n.T("tui.resume.title"),
		Lines:   []string{resumeText(msg.run, env), i18n.T("tui.resume.before_new")},
		Buttons: []string{i18n.T("tui.resume.resume"), i18n.T("tui.resume.new"), i18n.T("tui.resume.cancel")},
		Cancel:  2,
		OnChoose: func(i int) tea.Cmd {
			switch i {
			case 0:
				return Navigate(ScreenSync, SyncStart{OnInterrupted: "resume"})
			case 1:
				req.OnInterrupted = "fresh"
				return Navigate(ScreenSync, req)
			}
			return nil
		},
	})
}

func resumeText(run *Interrupted, env *Env) string {
	when := ago(run.StartedAt, env.Now())
	return i18n.T("tui.resume.text", "done", num(run.Total-run.Pending), "total", num(run.Total), "ago", when)
}

func (m *SyncScreen) start(req SyncStart, env *Env) tea.Cmd {
	m.gen++
	gen := m.gen
	ctx, cancel := context.WithCancel(env.Ctx)
	m.cancel, m.gate = cancel, newGate()
	req.Gate = m.gate.wait
	m.req = req
	m.state = syncRunning
	m.scanned, m.report, m.err = nil, nil, nil
	m.total, m.finished, m.lanes, m.ledger = 0, 0, nil, nil
	clear(m.counts)
	m.started, m.pausedFor, m.focus = env.Now(), 0, 0

	// Updates are queued, not dropped: the journal is in the database, but
	// the screen should also show every entry. The queue is closed after
	// the done message, so the reader never blocks on a finished run.
	events := make(chan tea.Msg, 1024)
	m.events = events
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(events)
		defer cancel()
		send := func(msg tea.Msg) {
			select {
			case events <- msg:
			case <-env.gone:
			}
		}
		rep, err := env.Backend.Sync(ctx, req, func(u SyncUpdate) { send(syncUpdateMsg{gen, u}) })
		send(syncDoneMsg{gen, rep, err})
	}()
	return m.next()
}

// next waits for the next message from the running sync.
func (m *SyncScreen) next() tea.Cmd {
	ch := m.events
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *SyncScreen) apply(u SyncUpdate, env *Env) {
	switch {
	case u.Scanned != nil:
		m.scanned = u.Scanned
	case u.Planned != nil:
		m.total = *u.Planned
	case u.Event != nil:
		e := u.Event
		switch e.Kind {
		case syncer.RepoStarted:
			m.lanes = append(m.lanes, lane{repo: e.Repo, phase: syncer.PhasePreflight, started: env.Now()})
		case syncer.PhaseChanged:
			for i := range m.lanes {
				if m.lanes[i].repo.ID == e.Repo.ID {
					m.lanes[i].phase = e.Phase
				}
			}
		case syncer.RepoFinished:
			m.lanes = slices.DeleteFunc(m.lanes, func(l lane) bool { return l.repo.ID == e.Repo.ID })
			if e.Result == nil || e.Result.Outcome == "" {
				return // never started: stopped before its turn
			}
			m.finished++
			m.counts[e.Result.Outcome]++
			m.ledger = append(m.ledger, entry{res: *e.Result, born: env.app.anim.Tick})
			if len(m.ledger) > ledgerKeep {
				m.ledger = m.ledger[len(m.ledger)-ledgerKeep:]
			}
		}
	}
}

func (m *SyncScreen) finish(msg syncDoneMsg, env *Env) tea.Cmd {
	if m.paused() {
		m.togglePause(env)
	}
	m.state, m.report, m.err = syncDone, msg.report, msg.err
	m.lanes = nil
	m.doneTick = env.app.anim.Tick
	if msg.report != nil && msg.report.Interrupted {
		// Ctrl+C or Stop: say so, unless an error says more.
		if m.err == nil || errors.Is(m.err, context.Canceled) {
			m.err = nil
		}
	}
	return func() tea.Msg { return ReposChangedMsg{} }
}

func (m *SyncScreen) togglePause(env *Env) {
	if m.gate.isPaused() {
		m.gate.set(false)
		m.pausedFor += env.Now().Sub(m.pausedAt)
	} else {
		m.gate.set(true)
		m.pausedAt = env.Now()
	}
}

func (m *SyncScreen) elapsed(env *Env) time.Duration {
	d := env.Now().Sub(m.started) - m.pausedFor
	if m.paused() {
		d -= env.Now().Sub(m.pausedAt)
	}
	return max(d, 0)
}

func (m *SyncScreen) View(f Frame) string {
	s := f.S
	env := f.Env
	switch m.state {
	case syncIdle:
		return m.idleView(f)
	case syncChecking:
		return "\n  " + s.Accent.Render(f.Anim.Spinner()) + " " + s.Muted.Render(i18n.T("tui.sync.checking"))
	}

	var lines []string
	// Title and progress.
	var title string
	switch {
	case m.state == syncDone:
		title = s.Title.Render(i18n.T("tui.sync.finished"))
	case m.state == syncStopping:
		title = s.Accent.Render(f.Anim.Spinner()) + " " + s.Warn.Render(i18n.T("tui.sync.stopping"))
	case m.paused():
		title = s.Warn.Render("‖ " + i18n.T("tui.sync.paused"))
	case m.total == 0:
		title = s.Accent.Render(f.Anim.Spinner()) + " " + s.Label.Render(i18n.T("tui.sync.scanning"))
	default:
		title = s.Accent.Render(f.Anim.Spinner()) + " " + s.Label.Render(i18n.T("tui.sync.syncing"))
	}
	title += "  " + s.Text.Render(i18n.T("tui.sync.progress", "done", num(m.finished), "total", num(m.total)))
	lines = append(lines, " "+spread(title, m.rate(f, env)+" ", f.Width-1, s.G.Ellipsis))
	pct := ""
	if m.total > 0 {
		pct = " " + itoa3(m.finished*100/m.total) + "%"
	}
	lines = append(lines, " "+s.bar(f.Anim, m.finished, m.total, f.Width-2-width(pct))+s.Muted.Render(pct))
	lines = append(lines, " "+m.countsLine(s))
	if m.scanned != nil && m.state != syncDone {
		lines = append(lines, " "+s.Muted.Render(i18n.T("scan.summary", "found", num(m.scanned.Found), "added", m.scanned.Added,
			"moved", m.scanned.Moved, "missing", m.scanned.Missing)))
	}
	lines = append(lines, "")

	if m.state == syncDone {
		lines = append(lines, m.summary(f)...)
	} else {
		lines = append(lines, m.laneLines(f, env)...)
	}

	// Buttons.
	lines = append(lines, "")
	lines = append(lines, m.buttons(f, len(lines)))
	lines = append(lines, "")

	// The ledger takes the rest of the page, newest entry last.
	room := f.Height - len(lines) - 1
	if room > 0 && len(m.ledger) > 0 {
		lines = append(lines, " "+s.Header.Render(i18n.T("tui.sync.ledger")))
		from := max(0, len(m.ledger)-room)
		for _, e := range m.ledger[from:] {
			lines = append(lines, m.entryLine(f, e))
		}
	}
	return strings.Join(lines, "\n")
}

func itoa3(n int) string { return fitRight(num(n), 3, "") }

func (m *SyncScreen) rate(f Frame, env *Env) string {
	s := f.S
	el := m.elapsed(env)
	if m.state == syncDone {
		if m.report != nil {
			return s.Muted.Render(i18n.T("tui.sync.took", "time", clock(m.report.Duration)))
		}
		return ""
	}
	if m.finished == 0 || el < time.Second {
		return s.Muted.Render(clock(el))
	}
	perMin := float64(m.finished) / el.Minutes()
	eta := time.Duration(float64(m.total-m.finished) / float64(m.finished) * float64(el))
	return s.Muted.Render(i18n.T("tui.sync.eta", "eta", clock(eta), "rate", num(int(perMin+0.5))))
}

func (m *SyncScreen) countsLine(s *Styles) string {
	part := func(outcome, key string) string {
		g, st := s.outcome(outcome)
		return st.Render(g + " " + num(m.counts[outcome]) + " " + i18n.T(key))
	}
	return strings.Join([]string{
		part(syncer.Updated, "tui.count.updated"), part(syncer.Unchanged, "tui.count.unchanged"),
		part(syncer.Skipped, "tui.count.skipped"), part(syncer.Failed, "tui.count.failed"),
	}, "   ")
}

func (m *SyncScreen) laneLines(f Frame, env *Env) []string {
	s := f.S
	lines := []string{" " + s.Header.Render(i18n.T("tui.sync.in_flight"))}
	// Lanes get at most a third of the page; the ledger needs room too.
	limit := max(1, min(len(m.lanes), (f.Height-10)/3))
	if len(m.lanes) == 0 {
		lines = append(lines, "  "+s.Muted.Render(i18n.T("tui.sync.waiting")))
	}
	phaseW, timeW := 12, 7
	nameW := f.Width - 4 - phaseW - timeW - 2
	for _, l := range m.lanes[:min(limit, len(m.lanes))] {
		phase := i18n.T("tui.phase." + l.phase)
		lines = append(lines, "  "+s.Accent.Render(s.G.Running)+" "+s.Text.Render(fit(l.repo.Path, nameW, s.G.Ellipsis))+" "+
			s.Hint.Render(fit(phase, phaseW, s.G.Ellipsis))+" "+s.Muted.Render(fitRight(clock(env.Now().Sub(l.started)), timeW, "")))
	}
	if extra := len(m.lanes) - limit; extra > 0 {
		lines = append(lines, "  "+s.Muted.Render(i18n.T("tui.sync.more_lanes", "n", extra)))
	}
	return lines
}

func (m *SyncScreen) summary(f Frame) []string {
	s := f.S
	var lines []string
	switch {
	case m.err != nil:
		lines = append(lines, " "+s.Fail.Render(s.G.Fail+" "+m.err.Error()))
		var ce *core.Error
		if errors.As(m.err, &ce) && ce.Hint != "" {
			lines = append(lines, "   "+s.Hint.Render(i18n.T("error.hint")+": ")+s.Text.Render(ce.Hint))
		}
	case m.report != nil && m.report.Interrupted:
		lines = append(lines, " "+s.Warn.Render(i18n.T("sync.interrupted")))
	case m.finished == 0:
		lines = append(lines, " "+s.Muted.Render(i18n.T("tui.sync.nothing")))
	case m.clean():
		lines = append(lines, " "+s.OK.Render(s.G.OK+" "+i18n.T("tui.sync.all_clean")))
	default:
		lines = append(lines, " "+s.Warn.Render(i18n.T("tui.sync.needs_look")))
	}
	if m.report != nil && m.report.ResumedFrom != 0 {
		lines = append(lines, " "+s.Muted.Render(i18n.T("sync.resumed", "run", m.report.ResumedFrom)))
	}
	if m.report != nil && m.report.Mode == "dry-run" {
		lines = append(lines, " "+s.Muted.Render(i18n.T("sync.dry_run")))
	}
	if m.rangoliOn(f.Anim) {
		frames := rangoli
		if s.G.Rule == asciiGlyphs.Rule {
			frames = rangoliASCII
		}
		step := min(len(frames)-1, (f.Anim.Tick-m.doneTick)/3)
		colours := []func(...string) string{s.Accent.Render, s.Hint.Render, s.OK.Render}
		for i, l := range frames[step] {
			c := colours[(i+f.Anim.Tick/4)%len(colours)]
			lines = append(lines, c(center(fit(l, 5, ""), f.Width)))
		}
	}
	return lines
}

func (m *SyncScreen) buttons(f Frame, row int) string {
	s := f.S
	type btn struct{ key, label string }
	var bs []btn
	switch m.state {
	case syncRunning:
		pause := i18n.T("tui.sync.pause")
		if m.paused() {
			pause = i18n.T("tui.sync.resume")
		}
		bs = []btn{{"p", pause}, {"c", i18n.T("tui.sync.stop")}}
	case syncDone:
		bs = []btn{{"f", i18n.T("tui.sync.view_failures")}, {"b", i18n.T("tui.sync.back")}, {"s", i18n.T("tui.sync.again")}}
	default:
		return ""
	}
	out, x := " ", 1
	for i, b := range bs {
		r := s.button(b.label, m.state == syncDone && i == m.focus)
		f.Zone("btn:"+b.key, x, row, width(r), 1)
		out += r + "  "
		x += width(r) + 2
	}
	return out
}

// entryLine writes one ledger entry. In Full mode the newest entries are
// written out by the kalam, a few letters per frame.
func (m *SyncScreen) entryLine(f Frame, e entry) string {
	s := f.S
	r := e.res
	g, st := s.outcome(r.Outcome)
	note := r.Reason
	if note == "" && r.Outcome == syncer.Updated {
		note = engine.Describe(r.Changes)
	}
	if note == "" && r.Outcome == syncer.Unchanged {
		note = i18n.T("sync.unchanged")
	}
	nameW := min(40, max(16, f.Width/3))
	text := fit(r.Repo.Path, nameW, s.G.Ellipsis) + "  " + note
	if f.Anim.Level == LevelFull {
		if n := (f.Anim.Tick - e.born + 1) * 12; n < width(text) {
			text = truncate(text, n, "") + s.G.Pen
		}
	}
	return "  " + st.Render(g) + " " + s.Text.Render(truncate(text, f.Width-5, s.G.Ellipsis))
}

func (m *SyncScreen) idleView(f Frame) string {
	s := f.S
	lines := []string{"", "  " + s.Title.Render(i18n.T("tui.sync.idle_title")), ""}
	for _, l := range wrap(i18n.T("tui.sync.idle_text"), f.Width-4) {
		lines = append(lines, "  "+s.Text.Render(l))
	}
	lines = append(lines, "")
	b := s.button(i18n.T("tui.sync.all"), true)
	f.Zone("btn:s", 2, len(lines), width(b), 1)
	lines = append(lines, "  "+b)
	return strings.Join(lines, "\n")
}

// gate pauses a run between repos. Repos already running finish.
type gate struct {
	mu     sync.Mutex
	paused bool
	resume chan struct{}
}

func newGate() *gate { return &gate{resume: make(chan struct{})} }

func (g *gate) isPaused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

func (g *gate) set(paused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if paused == g.paused {
		return
	}
	g.paused = paused
	if !paused {
		close(g.resume)
		g.resume = make(chan struct{})
	}
}

func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	paused, ch := g.paused, g.resume
	g.mu.Unlock()
	if !paused {
		return ctx.Err()
	}
	select {
	case <-ch:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// checkInterrupted looks for an interrupted sync when the app starts.
func (a *App) checkInterrupted() tea.Cmd {
	env := a.env
	return func() tea.Msg {
		run, err := env.Backend.Interrupted(env.Ctx)
		return interruptedMsg{run: run, err: err}
	}
}

// offerResume asks what to do with an interrupted sync (PRD F12).
func (a *App) offerResume(msg interruptedMsg) tea.Cmd {
	if msg.start != nil {
		return a.broadcast(msg) // the sync screen asked; it handles the answer
	}
	if msg.run == nil || msg.err != nil || a.dialog != nil {
		return nil
	}
	env := a.env
	run := msg.run
	a.dialog = &Dialog{
		Title:   i18n.T("tui.resume.title"),
		Lines:   []string{resumeText(run, env)},
		Buttons: []string{i18n.T("tui.resume.resume"), i18n.T("tui.resume.fresh"), i18n.T("tui.resume.discard")},
		Cancel:  -1, // esc: decide later
		OnChoose: func(i int) tea.Cmd {
			switch i {
			case 0:
				return Navigate(ScreenSync, SyncStart{OnInterrupted: "resume"})
			case 1:
				return Navigate(ScreenSync, SyncStart{OnInterrupted: "fresh"})
			}
			return func() tea.Msg { return discardedMsg{env.Backend.Discard(env.Ctx, run.RunID)} }
		},
	}
	return nil
}
