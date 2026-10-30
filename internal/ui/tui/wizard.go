package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/schedule"
)

// Wizard steps (PRD F16). Connecting providers joins between scan and
// look once provider logins exist (M5).
const (
	stepArchive = iota
	stepScan
	stepLook
	stepSchedule
	stepSyncNow
	stepCount
)

var (
	wizardThemes     = []string{"ledger", "ledger-light", "high-contrast"}
	wizardAnimations = []string{"auto", "full", "light", "off"}
	wizardSchedules  = []string{"", schedule.Daily, schedule.Weekly}
)

// WizardStart, as a NavigateMsg payload, opens the wizard at one step.
// With Only set the wizard returns to Back after that step, which is how
// Settings reuses the schedule step.
type WizardStart struct {
	Step int
	Only bool
	Back ScreenID
}

// Wizard is the first-run setup. Every step can be skipped, which keeps
// the defaults; nothing it does is needed to use ChitraGit.
type Wizard struct {
	step     int
	only     bool
	back     ScreenID
	startDir string

	path         field // stepArchive
	askedArchive bool  // the wizard started without an archive
	busy         bool  // waiting for the backend
	err          string
	found        *ScanSummary

	row             int // stepLook: 0 theme, 1 animation; stepSchedule: 0 frequency, 1 time
	themeI, animI   int
	freqI           int
	at              field
	weekday         time.Weekday
	scheduled       string // result line from stepSchedule
	focus           int    // focused button on the current step
	themeAtStart    string
	animationsStart string
}

func NewWizard(startDir string) *Wizard {
	w := &Wizard{startDir: startDir, back: ScreenBrowser}
	w.at.set("02:00")
	return w
}

func (w *Wizard) Title() string { return "" }

func (w *Wizard) Init(env *Env) tea.Cmd {
	w.path.set(w.startDir)
	w.themeI = indexOf(wizardThemes, env.Config.UI.Theme)
	w.animI = indexOf(wizardAnimations, env.Config.UI.Animations)
	w.themeAtStart, w.animationsStart = env.Config.UI.Theme, env.Config.UI.Animations
	if env.Setup != nil && env.Setup.ArchiveRoot() != "" {
		return w.enter(stepScan, env)
	}
	w.askedArchive = true
	return nil
}

// Capturing: the wizard owns every key so typing a path or a time never
// switches tabs or quits.
func (w *Wizard) Capturing() bool { return true }
func (w *Wizard) Animating() bool { return w.busy }

func (w *Wizard) Keys() []KeyHint {
	return []KeyHint{{"enter", i18n.T("tui.wizard.next")}, {"esc", i18n.T("tui.wizard.skip")},
		{"ctrl+s", i18n.T("tui.wizard.skip_all")}}
}

type (
	archiveReadyMsg struct {
		root string
		err  error
	}
	scanDoneMsg struct {
		sum ScanSummary
		err error
	}
	scheduleDoneMsg struct {
		info ScheduleInfo
		err  error
	}
	settingSavedMsg struct{ err error }
)

func (w *Wizard) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		if s, ok := msg.Payload.(WizardStart); ok {
			w.only, w.back = s.Only, s.Back
			if w.back == "" {
				w.back = ScreenBrowser
			}
			w.themeAtStart, w.animationsStart = env.Config.UI.Theme, env.Config.UI.Animations
			return w.enter(s.Step, env)
		}
	case archiveReadyMsg:
		w.busy = false
		if msg.err != nil {
			w.err = msg.err.Error()
			return nil
		}
		env.Archive = msg.root
		return tea.Batch(w.reload(env), w.enter(stepScan, env))
	case scanDoneMsg:
		w.busy = false
		if msg.err != nil {
			w.err = msg.err.Error()
			return nil
		}
		w.found = &msg.sum
		return func() tea.Msg { return ReposChangedMsg{} }
	case scheduleDoneMsg:
		w.busy = false
		if msg.err != nil {
			w.err = msg.err.Error()
			return nil
		}
		w.scheduled = describeSchedule(msg.info)
		return w.next(env)
	case settingSavedMsg:
		if msg.err != nil {
			w.err = msg.err.Error()
			return nil
		}
		return tea.Batch(w.reload(env), w.next(env))
	case tea.KeyMsg:
		return w.key(msg, env)
	case ZoneClick:
		return w.click(msg, env)
	}
	return nil
}

// reload refreshes env.Config after the wizard wrote settings.
func (w *Wizard) reload(env *Env) tea.Cmd {
	if cfg, _, err := env.Setup.Settings(); err == nil {
		env.Config = cfg
	}
	return nil
}

// enter shows a step and starts its work.
func (w *Wizard) enter(step int, env *Env) tea.Cmd {
	w.step, w.err, w.focus, w.row = step, "", 0, 0
	if step == stepScan && w.found == nil && env.Setup != nil && env.Setup.ArchiveRoot() != "" {
		w.busy = true
		ctx, setup := env.Ctx, env.Setup
		return func() tea.Msg {
			sum, err := setup.Scan(ctx)
			return scanDoneMsg{sum, err}
		}
	}
	return nil
}

// next leaves the current step; past the last step the wizard closes.
func (w *Wizard) next(env *Env) tea.Cmd {
	if w.only || w.step+1 >= stepCount {
		return w.finish(env, false)
	}
	return w.enter(w.step+1, env)
}

func (w *Wizard) finish(env *Env, syncNow bool) tea.Cmd {
	back := w.back
	w.only, w.back = false, ScreenBrowser
	if syncNow {
		return Navigate(ScreenSync, SyncStart{})
	}
	return Navigate(back, nil)
}

// skip leaves a step without changing anything. On the look step that
// also undoes the live preview.
func (w *Wizard) skip(env *Env) tea.Cmd {
	if w.busy {
		return nil
	}
	if w.step == stepArchive {
		return nil // there is nothing to show without an archive
	}
	var cmd tea.Cmd
	if w.step == stepLook {
		cmd = restyle(w.themeAtStart, w.animationsStart)
	}
	return tea.Batch(cmd, w.next(env))
}

// confirm does the current step's action.
func (w *Wizard) confirm(env *Env) tea.Cmd {
	if w.busy {
		return nil
	}
	w.err = ""
	switch w.step {
	case stepArchive:
		path := strings.TrimSpace(w.path.String())
		if path == "" {
			w.err = i18n.T("tui.wizard.archive_empty")
			return nil
		}
		w.busy = true
		ctx, setup := env.Ctx, env.Setup
		return func() tea.Msg {
			root, err := setup.InitArchive(ctx, path)
			return archiveReadyMsg{root, err}
		}
	case stepScan:
		if w.found == nil {
			return nil
		}
		return w.next(env)
	case stepLook:
		setup := env.Setup
		themeName, anim := wizardThemes[w.themeI], wizardAnimations[w.animI]
		return func() tea.Msg {
			if _, err := setup.SaveSetting("ui.theme", themeName, true); err != nil {
				return settingSavedMsg{err}
			}
			_, err := setup.SaveSetting("ui.animations", anim, true)
			return settingSavedMsg{err}
		}
	case stepSchedule:
		choice := ScheduleChoice{Frequency: wizardSchedules[w.freqI], Weekday: w.weekday}
		if choice.Frequency != "" {
			h, m, err := schedule.ParseClock(w.at.String())
			if err != nil {
				w.err = err.Error()
				return nil
			}
			choice.Hour, choice.Minute = h, m
		}
		w.busy = true
		ctx, setup := env.Ctx, env.Setup
		return func() tea.Msg {
			info, err := setup.SetSchedule(ctx, choice)
			return scheduleDoneMsg{info, err}
		}
	case stepSyncNow:
		return w.finish(env, w.focus == 0)
	}
	return nil
}

func (w *Wizard) key(k tea.KeyMsg, env *Env) tea.Cmd {
	switch k.String() {
	case "enter":
		return w.confirm(env)
	case "esc":
		return w.skip(env)
	case "ctrl+s":
		if w.step == stepArchive || w.busy {
			return nil
		}
		var cmd tea.Cmd
		if w.step == stepLook {
			cmd = restyle(w.themeAtStart, w.animationsStart)
		}
		return tea.Batch(cmd, w.finish(env, false))
	}
	switch w.step {
	case stepArchive:
		w.path.key(k)
	case stepLook:
		return w.choose(k)
	case stepSchedule:
		if w.row == 1 && wizardSchedules[w.freqI] != "" && w.at.key(k) {
			return nil
		}
		w.choose(k)
	case stepSyncNow:
		switch k.String() {
		case "left", "right", "tab", "shift+tab", "h", "l":
			w.focus = 1 - w.focus
		case "y":
			return w.finish(env, true)
		case "n":
			return w.finish(env, false)
		}
	}
	return nil
}

// choose moves between rows (↑↓) and options (←→) on the look and
// schedule steps.
func (w *Wizard) choose(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "k", "shift+tab":
		w.row = 0
	case "down", "j", "tab":
		w.row = 1
	case "left", "h":
		return w.shift(-1)
	case "right", "l", " ":
		return w.shift(1)
	case "d":
		if w.step == stepSchedule && wizardSchedules[w.freqI] == schedule.Weekly {
			w.weekday = (w.weekday + 1) % 7
		}
	}
	return nil
}

func (w *Wizard) shift(d int) tea.Cmd {
	rot := func(i, n int) int { return (i + d + n) % n }
	switch {
	case w.step == stepLook && w.row == 0:
		w.themeI = rot(w.themeI, len(wizardThemes))
	case w.step == stepLook:
		w.animI = rot(w.animI, len(wizardAnimations))
	case w.step == stepSchedule && w.row == 0:
		w.freqI = rot(w.freqI, len(wizardSchedules))
	default:
		return nil
	}
	if w.step == stepLook {
		return restyle(wizardThemes[w.themeI], wizardAnimations[w.animI])
	}
	return nil
}

func (w *Wizard) click(c ZoneClick, env *Env) tea.Cmd {
	var i int
	switch {
	case c.ID == "wz:next":
		return w.confirm(env)
	case c.ID == "wz:skip":
		return w.skip(env)
	case c.ID == "wz:yes":
		return w.finish(env, true)
	case c.ID == "wz:no":
		return w.finish(env, false)
	case zoneIndex(c.ID, "wz:theme:%d", &i):
		w.row, w.themeI = 0, i
		return restyle(wizardThemes[w.themeI], wizardAnimations[w.animI])
	case zoneIndex(c.ID, "wz:anim:%d", &i):
		w.row, w.animI = 1, i
		return restyle(wizardThemes[w.themeI], wizardAnimations[w.animI])
	case zoneIndex(c.ID, "wz:freq:%d", &i):
		w.row, w.freqI = 0, i
	case zoneIndex(c.ID, "wz:day:%d", &i):
		w.weekday = time.Weekday(i)
	}
	return nil
}

func zoneIndex(id, format string, v *int) bool {
	_, err := fmt.Sscanf(id, format, v)
	return err == nil
}

func (w *Wizard) View(f Frame) string {
	s := f.S
	inner := min(f.Width-4, 76)
	left := max(0, (f.Width-inner)/2)
	pad := strings.Repeat(" ", left)
	var lines []string
	add := func(l string) { lines = append(lines, pad+l) }
	zone := func(id string, col, w int) { f.Zone(id, left+col, len(lines), w, 1) }

	// The archive step counts only when the wizard had to ask for one.
	steps, n := stepCount-1, w.step
	if w.askedArchive {
		steps, n = stepCount, w.step+1
	}
	add("")
	add(s.Brand.Render(i18n.T("tui.wizard.title")) + s.Muted.Render("   "+i18n.T("tui.wizard.step", "n", n, "of", steps)))
	add(s.rule(inner))
	add("")

	switch w.step {
	case stepArchive:
		add(s.Title.Render(i18n.T("tui.wizard.archive_title")))
		for _, l := range wrap(i18n.T("tui.wizard.archive_text"), inner) {
			add(s.Text.Render(l))
		}
		add("")
		add(s.Muted.Render(i18n.T("tui.wizard.folder")+" ") + w.path.view(s, inner-12))
	case stepScan:
		add(s.Title.Render(i18n.T("tui.wizard.scan_title")))
		add("")
		switch {
		case w.busy:
			add(s.Accent.Render(f.Anim.Spinner()) + " " + s.Text.Render(i18n.T("tui.wizard.scanning", "path", f.Env.Archive)))
		case w.found != nil:
			add(s.OK.Render(s.G.OK+" ") + s.Text.Render(i18n.T("tui.wizard.found",
				"repos", num(w.found.Found), "owners", num(w.found.Owners))))
			if w.found.Found == 0 {
				add("")
				for _, l := range wrap(i18n.T("tui.wizard.found_none"), inner) {
					add(s.Muted.Render(l))
				}
			}
		}
	case stepLook:
		add(s.Title.Render(i18n.T("tui.wizard.look_title")))
		add(s.Muted.Render(i18n.T("tui.wizard.look_text")))
		add("")
		w.options(s, &lines, pad, left, f, i18n.T("tui.wizard.theme"), "wz:theme:%d", wizardThemes, w.themeI, w.row == 0)
		w.options(s, &lines, pad, left, f, i18n.T("tui.wizard.animation"), "wz:anim:%d", wizardAnimations, w.animI, w.row == 1)
		add("")
		add(s.Muted.Render(i18n.T("tui.wizard.preview")) + s.OK.Render(s.G.OK+" ok ") + s.Warn.Render(s.G.Skip+" warn ") +
			s.Fail.Render(s.G.Fail+" fail ") + s.Accent.Render(f.Anim.Spinner()) + " " + s.bar(f.Anim, 3, 5, 12))
	case stepSchedule:
		add(s.Title.Render(i18n.T("tui.wizard.schedule_title")))
		for _, l := range wrap(i18n.T("tui.wizard.schedule_text"), inner) {
			add(s.Muted.Render(l))
		}
		add("")
		labels := []string{i18n.T("tui.wizard.off"), i18n.T("tui.wizard.daily"), i18n.T("tui.wizard.weekly")}
		w.options(s, &lines, pad, left, f, i18n.T("tui.wizard.how_often"), "wz:freq:%d", labels, w.freqI, w.row == 0)
		if wizardSchedules[w.freqI] != "" {
			label := fit(i18n.T("tui.wizard.at"), 12, "")
			cur := "  "
			if w.row == 1 {
				cur = s.Cursor.Render(s.G.Bullet) + " "
			}
			add(cur + s.Muted.Render(label) + w.at.view(s, 8))
			if wizardSchedules[w.freqI] == schedule.Weekly {
				line, col := "  "+s.Muted.Render(fit(i18n.T("tui.wizard.day"), 12, "")), 14
				for d := time.Sunday; d <= time.Saturday; d++ {
					b := s.choice(d.String()[:3], d == w.weekday)
					zone(fmt.Sprintf("wz:day:%d", d), col, width(b))
					line += b + " "
					col += width(b) + 1
				}
				add(line)
			}
		}
	case stepSyncNow:
		add(s.Title.Render(i18n.T("tui.wizard.sync_title")))
		if w.scheduled != "" {
			add(s.OK.Render(s.G.OK+" ") + s.Text.Render(w.scheduled))
		}
		add("")
		for _, l := range wrap(i18n.T("tui.wizard.sync_text"), inner) {
			add(s.Text.Render(l))
		}
		add("")
		yes, no := s.button(i18n.T("tui.wizard.sync_yes"), w.focus == 0), s.button(i18n.T("tui.wizard.sync_no"), w.focus == 1)
		zone("wz:yes", 0, width(yes))
		zone("wz:no", width(yes)+2, width(no))
		add(yes + "  " + no)
	}

	if w.err != "" {
		add("")
		for _, l := range wrap(w.err, inner) {
			add(s.Fail.Render(l))
		}
	}
	if w.step != stepSyncNow {
		add("")
		next := s.button(i18n.T("tui.wizard.next"), true)
		zone("wz:next", 0, width(next))
		line := next
		if w.step != stepArchive {
			skip := s.button(i18n.T("tui.wizard.skip"), false)
			zone("wz:skip", width(next)+2, width(skip))
			line += "  " + skip
		}
		if w.busy {
			line += "  " + s.Accent.Render(f.Anim.Spinner())
		}
		add(line)
	}
	return strings.Join(lines, "\n")
}

// options draws one row of choices as clickable buttons.
func (w *Wizard) options(s *Styles, lines *[]string, pad string, left int, f Frame, label, zoneFmt string, opts []string, sel int, focused bool) {
	cur := "  "
	if focused {
		cur = s.Cursor.Render(s.G.Bullet) + " "
	}
	line, col := cur+s.Muted.Render(fit(label, 12, "")), 14
	for i, o := range opts {
		b := s.choice(o, i == sel)
		f.Zone(fmt.Sprintf(zoneFmt, i), left+col, len(*lines), width(b), 1)
		line += b + " "
		col += width(b) + 1
	}
	*lines = append(*lines, pad+line)
}

func describeSchedule(info ScheduleInfo) string {
	if !info.On {
		return i18n.T("tui.wizard.schedule_off")
	}
	return i18n.T("tui.wizard.schedule_on", "when", info.When, "backend", info.Backend)
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}
