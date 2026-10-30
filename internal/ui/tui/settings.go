package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// Settings edits the same TOML files as `chitragit config` (PRD F13),
// with each value checked before it is saved, and offers the schedule
// and the setup wizard.
type Settings struct {
	keys     []string
	values   map[string]string
	warnings []string
	cursor   int
	offset   int
	user     bool // write to the personal config instead of the archive's

	editing bool
	input   field
	msg     string // last save result
	msgErr  bool
	sched   *ScheduleInfo
}

func NewSettings() *Settings { return &Settings{keys: config.Keys(), values: map[string]string{}} }

func (st *Settings) Title() string   { return i18n.T("tui.tab.settings") }
func (st *Settings) Capturing() bool { return st.editing }

func (st *Settings) Init(env *Env) tea.Cmd {
	st.load(env)
	return st.loadSchedule(env)
}

type scheduleInfoMsg struct {
	info ScheduleInfo
	err  error
}

func (st *Settings) loadSchedule(env *Env) tea.Cmd {
	if env.Setup.ArchiveRoot() == "" {
		return nil
	}
	ctx, setup := env.Ctx, env.Setup
	return func() tea.Msg {
		info, err := setup.Schedule(ctx)
		return scheduleInfoMsg{info, err}
	}
}

// load reads the effective settings.
func (st *Settings) load(env *Env) {
	cfg, warnings, err := env.Setup.Settings()
	if err != nil {
		st.msg, st.msgErr = err.Error(), true
		return
	}
	env.Config, st.warnings = cfg, warnings
	for _, k := range st.keys {
		st.values[k], _ = cfg.Get(k)
	}
}

func (st *Settings) Keys() []KeyHint {
	if st.editing {
		return []KeyHint{{"enter", i18n.T("tui.settings.save")}, {"esc", i18n.T("tui.settings.cancel")}}
	}
	return []KeyHint{{"↑↓", i18n.T("tui.key.move")}, {"enter", i18n.T("tui.settings.change")},
		{"u", i18n.T("tui.settings.scope")}, {"t", i18n.T("tui.settings.schedule")}, {"w", i18n.T("tui.settings.wizard")}}
}

func (st *Settings) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		if msg.To == ScreenSettings {
			st.load(env)
			return st.loadSchedule(env)
		}
	case scheduleInfoMsg:
		if msg.err == nil {
			st.sched = &msg.info
		}
	case tea.KeyMsg:
		if st.editing {
			return st.editKey(msg, env)
		}
		return st.key(msg, env)
	case ZoneClick:
		return st.click(msg, env)
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			st.move(-3)
		case tea.MouseButtonWheelDown:
			st.move(3)
		}
	}
	return nil
}

func (st *Settings) key(k tea.KeyMsg, env *Env) tea.Cmd {
	switch k.String() {
	case "up", "k":
		st.move(-1)
	case "down", "j":
		st.move(1)
	case "pgup":
		st.move(-10)
	case "pgdown":
		st.move(10)
	case "enter", " ":
		return st.change(env)
	case "u":
		st.user = !st.user
	case "t":
		return Navigate(ScreenWizard, WizardStart{Step: stepSchedule, Only: true, Back: ScreenSettings})
	case "w":
		return Navigate(ScreenWizard, WizardStart{Step: stepScan, Back: ScreenBrowser})
	}
	return nil
}

func (st *Settings) move(d int) {
	st.cursor = min(max(st.cursor+d, 0), len(st.keys)-1)
}

// change toggles a true/false setting at once and opens the editor for
// anything else.
func (st *Settings) change(env *Env) tea.Cmd {
	key := st.keys[st.cursor]
	switch st.values[key] {
	case "true":
		return st.save(env, key, "false")
	case "false":
		return st.save(env, key, "true")
	}
	st.editing = true
	st.input.set(st.values[key])
	st.msg = ""
	return nil
}

func (st *Settings) editKey(k tea.KeyMsg, env *Env) tea.Cmd {
	switch k.String() {
	case "enter":
		st.editing = false
		return st.save(env, st.keys[st.cursor], strings.TrimSpace(st.input.String()))
	case "esc":
		st.editing = false
		return nil
	}
	st.input.key(k)
	return nil
}

// save writes one value. An invalid value is refused with the reason,
// and the file is left as it was.
func (st *Settings) save(env *Env, key, value string) tea.Cmd {
	file, err := env.Setup.SaveSetting(key, value, st.user)
	if err != nil {
		st.msg, st.msgErr = err.Error(), true
		return nil
	}
	st.load(env)
	st.msg, st.msgErr = i18n.T("tui.settings.saved", "key", key, "value", st.values[key], "file", file), false
	if strings.HasPrefix(key, "ui.theme") || strings.HasPrefix(key, "ui.animations") {
		return restyle(env.Config.UI.Theme, env.Config.UI.Animations)
	}
	return nil
}

func (st *Settings) click(c ZoneClick, env *Env) tea.Cmd {
	switch {
	case c.ID == "set:schedule":
		return Navigate(ScreenWizard, WizardStart{Step: stepSchedule, Only: true, Back: ScreenSettings})
	case c.ID == "set:wizard":
		return Navigate(ScreenWizard, WizardStart{Step: stepScan, Back: ScreenBrowser})
	case c.ID == "set:scope":
		st.user = !st.user
	case strings.HasPrefix(c.ID, "set:row:"):
		i, err := strconv.Atoi(strings.TrimPrefix(c.ID, "set:row:"))
		if err != nil || i >= len(st.keys) {
			return nil
		}
		st.cursor = i
		if c.Double || st.values[st.keys[i]] == "true" || st.values[st.keys[i]] == "false" {
			return st.change(env)
		}
	}
	return nil
}

func (st *Settings) View(f Frame) string {
	s := f.S
	var lines []string

	sched, wiz := s.button(i18n.T("tui.settings.schedule_button"), false), s.button(i18n.T("tui.settings.wizard_button"), false)
	f.Zone("set:schedule", 1, 0, width(sched), 1)
	f.Zone("set:wizard", 1+width(sched)+2, 0, width(wiz), 1)
	lines = append(lines, " "+sched+"  "+wiz+"  "+s.Muted.Render(st.scheduleLine()))

	scope := i18n.T("tui.settings.scope_archive")
	if st.user {
		scope = i18n.T("tui.settings.scope_user")
	}
	scopeText := i18n.T("tui.settings.writes_to") + " " + s.Accent.Render(scope)
	f.Zone("set:scope", 1, 1, width(scopeText), 1)
	lines = append(lines, " "+s.Muted.Render(scopeText)+s.Muted.Render("  (u)"))
	lines = append(lines, s.rule(f.Width))

	// The list fills what is left, minus the doc and message lines.
	listH := max(1, f.Height-len(lines)-3)
	if st.cursor < st.offset {
		st.offset = st.cursor
	}
	if st.cursor >= st.offset+listH {
		st.offset = st.cursor - listH + 1
	}
	keyW := 0
	for _, k := range st.keys {
		keyW = max(keyW, width(k))
	}
	for i := st.offset; i < len(st.keys) && i < st.offset+listH; i++ {
		k := st.keys[i]
		cur, style := "  ", s.Text
		if i == st.cursor {
			cur, style = s.Cursor.Render(s.G.Cursor)+" ", s.Selected
		}
		val := s.Accent.Render(st.values[k])
		if i == st.cursor && st.editing {
			val = st.input.view(s, f.Width-keyW-6)
		}
		f.Zone("set:row:"+strconv.Itoa(i), 0, len(lines), f.Width, 1)
		lines = append(lines, cur+style.Render(fit(k, keyW, s.G.Ellipsis))+"  "+val)
	}
	for len(lines) < 3+listH {
		lines = append(lines, "")
	}

	lines = append(lines, s.rule(f.Width))
	if doc := config.Doc(st.keys[st.cursor]); doc != "" {
		lines = append(lines, " "+s.Muted.Render(st.keys[st.cursor]+": "+doc))
	} else {
		lines = append(lines, "")
	}
	switch {
	case st.msg != "" && st.msgErr:
		lines = append(lines, " "+s.Fail.Render(firstLine(st.msg)))
	case st.msg != "":
		lines = append(lines, " "+s.OK.Render(s.G.OK+" ")+s.Text.Render(st.msg))
	case len(st.warnings) > 0:
		lines = append(lines, " "+s.Warn.Render(s.G.Skip+" "+st.warnings[0]))
	}
	return strings.Join(lines, "\n")
}

func (st *Settings) scheduleLine() string {
	switch {
	case st.sched == nil:
		return ""
	case st.sched.Missing:
		return i18n.T("tui.settings.schedule_missing")
	case st.sched.On:
		return describeSchedule(*st.sched)
	}
	return i18n.T("tui.wizard.schedule_off")
}

// firstLine keeps an error to one line; the rest is in `config validate`.
func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
