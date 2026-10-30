package tui

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/TTox-Tech/chitragit/internal/conflict"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// ScreenConflicts lists repos whose local changes clash with an update and
// resolves them (PRD F4).
const ScreenConflicts ScreenID = "conflicts"

// ConflictBackend is what the Conflict Screen needs on top of Backend. The
// screen is registered only when the backend provides it.
type ConflictBackend interface {
	Conflicts(ctx context.Context) ([]store.Conflict, error)
	// ConflictOpen returns the session open in a repo; with start set it
	// starts one if none is open.
	ConflictOpen(ctx context.Context, repoID string, start bool) (*ConflictState, error)
	// ConflictResolve settles hunk (0-based) of a file, or the whole file
	// when hunk is negative.
	ConflictResolve(ctx context.Context, repoID, path string, hunk int, c conflict.Choice) (*ConflictState, error)
	ConflictMarkResolved(ctx context.Context, repoID, path string) (*ConflictState, error)
	ConflictFinish(ctx context.Context, repoID string) error
	ConflictAbort(ctx context.Context, repoID string) error
	ConflictDismiss(ctx context.Context, repoID string) error
	// ConflictEditor returns the command that opens a file in the user's
	// editor; the screen runs it with the terminal handed over.
	ConflictEditor(ctx context.Context, repoID, path string) (*exec.Cmd, error)
}

// ConflictState is an open session as the screen shows it.
type ConflictState struct {
	Repo  core.Repo
	Files []conflict.File
	Hunks map[string][]conflict.Hunk // unresolved hunks of text files
	// Done means nothing clashed any more: the update is applied and the
	// session already ended.
	Done bool
}

// ConflictsScreen has two modes: the list of repos with conflicts, and a
// session in one repo with its files on the left and the current hunk,
// theirs beside mine, on the right.
type ConflictsScreen struct {
	list   []store.Conflict
	cursor int

	st         *ConflictState
	file, hunk int

	note    string // last result, shown above the buttons
	err     error
	working bool
}

func NewConflictsScreen() *ConflictsScreen { return &ConflictsScreen{} }

func (c *ConflictsScreen) Title() string { return i18n.T("tui.tab.conflicts") }

func (c *ConflictsScreen) Init(env *Env) tea.Cmd { return c.reload(env) }

type conflictsListMsg struct {
	list []store.Conflict
	err  error
}

type conflictStateMsg struct {
	st   *ConflictState
	err  error
	note string
}

// conflictEndedMsg follows finish, abort or dismiss.
type conflictEndedMsg struct {
	err  error
	note string
}

type editorDoneMsg struct {
	path string
	err  error
}

func backendOf(env *Env) ConflictBackend {
	b, _ := env.Backend.(ConflictBackend)
	return b
}

func (c *ConflictsScreen) reload(env *Env) tea.Cmd {
	b := backendOf(env)
	if b == nil {
		return nil
	}
	return func() tea.Msg {
		list, err := b.Conflicts(env.Ctx)
		return conflictsListMsg{list, err}
	}
}

// op runs a backend call that returns the session's new state.
func (c *ConflictsScreen) op(env *Env, note string, fn func(ConflictBackend) (*ConflictState, error)) tea.Cmd {
	b := backendOf(env)
	if b == nil || c.working {
		return nil
	}
	c.working, c.err = true, nil
	return func() tea.Msg {
		st, err := fn(b)
		return conflictStateMsg{st, err, note}
	}
}

func (c *ConflictsScreen) end(env *Env, note string, fn func(ConflictBackend) error) tea.Cmd {
	b := backendOf(env)
	if b == nil || c.working {
		return nil
	}
	c.working, c.err = true, nil
	return func() tea.Msg { return conflictEndedMsg{fn(b), note} }
}

func (c *ConflictsScreen) Keys() []KeyHint {
	if c.st == nil {
		return []KeyHint{{"enter", i18n.T("tui.conflicts.resolve")}, {"x", i18n.T("tui.conflicts.dismiss")}}
	}
	return []KeyHint{{"m/t/b", i18n.T("tui.conflicts.pick")}, {"←/→", i18n.T("tui.conflicts.hunk_key")},
		{"e", i18n.T("tui.conflicts.edit")}, {"f", i18n.T("tui.conflicts.finish")}, {"a", i18n.T("tui.conflicts.abort")},
		{"esc", i18n.T("tui.key.back")}}
}

func (c *ConflictsScreen) Update(msg tea.Msg, env *Env) tea.Cmd {
	switch msg := msg.(type) {
	case NavigateMsg:
		if id, ok := msg.Payload.(string); ok && id != "" {
			return c.open(env, id)
		}
		if c.st == nil {
			return c.reload(env)
		}
	case ReposChangedMsg:
		return c.reload(env)
	case conflictsListMsg:
		c.list, c.err = msg.list, msg.err
		c.cursor = min(c.cursor, max(0, len(c.list)-1))
	case conflictStateMsg:
		c.working, c.err, c.note = false, msg.err, msg.note
		if msg.st == nil {
			return nil
		}
		if msg.st.Done {
			c.st = nil
			c.note = i18n.T("conflicts.nothing_clashed")
			return tea.Batch(c.reload(env), reposChanged)
		}
		c.setState(msg.st)
	case conflictEndedMsg:
		c.working = false
		if msg.err != nil {
			c.err = msg.err
			return nil
		}
		c.st, c.note = nil, msg.note
		return tea.Batch(c.reload(env), reposChanged)
	case editorDoneMsg:
		if msg.err != nil {
			c.err = msg.err
			return nil
		}
		path := msg.path
		return c.op(env, "", func(b ConflictBackend) (*ConflictState, error) {
			st, err := b.ConflictMarkResolved(env.Ctx, c.st.Repo.ID, path)
			if err != nil {
				// Markers left: show the file as it is now and say why.
				if again, oerr := b.ConflictOpen(env.Ctx, c.st.Repo.ID, false); oerr == nil {
					return again, err
				}
			}
			return st, err
		})
	case tea.KeyMsg:
		return c.key(msg.String(), env)
	case ZoneClick:
		return c.click(msg, env)
	}
	return nil
}

func reposChanged() tea.Msg { return ReposChangedMsg{} }

func (c *ConflictsScreen) open(env *Env, repoID string) tea.Cmd {
	return c.op(env, "", func(b ConflictBackend) (*ConflictState, error) {
		return b.ConflictOpen(env.Ctx, repoID, true)
	})
}

// setState keeps the selection on the same file while it still has
// conflicts, otherwise moves on to the next file still unresolved.
func (c *ConflictsScreen) setState(st *ConflictState) {
	cur := ""
	if c.st != nil && c.file < len(c.st.Files) {
		cur = c.st.Files[c.file].Path
	}
	c.st, c.file = st, 0
	for i, f := range st.Files {
		if f.Path == cur {
			c.file = i
			break
		}
	}
	for i := range st.Files {
		if k := (c.file + i) % len(st.Files); !st.Files[k].Resolved {
			if k != c.file {
				c.hunk = 0
			}
			c.file = k
			break
		}
	}
	c.hunk = min(c.hunk, max(0, len(c.hunks())-1))
}

func (c *ConflictsScreen) current() (conflict.File, bool) {
	if c.st == nil || c.file >= len(c.st.Files) {
		return conflict.File{}, false
	}
	return c.st.Files[c.file], true
}

func (c *ConflictsScreen) hunks() []conflict.Hunk {
	f, ok := c.current()
	if !ok {
		return nil
	}
	return c.st.Hunks[f.Path]
}

func (c *ConflictsScreen) allResolved() bool {
	if c.st == nil {
		return false
	}
	for _, f := range c.st.Files {
		if !f.Resolved {
			return false
		}
	}
	return true
}

func (c *ConflictsScreen) key(k string, env *Env) tea.Cmd {
	if c.st == nil {
		switch k {
		case "up", "k":
			c.cursor = max(0, c.cursor-1)
		case "down", "j":
			c.cursor = min(len(c.list)-1, c.cursor+1)
		case "enter", "r":
			if c.cursor < len(c.list) {
				return c.open(env, c.list[c.cursor].RepoID)
			}
		case "x":
			return c.confirmDismiss(env)
		}
		return nil
	}
	switch k {
	case "esc", "backspace":
		// The session stays open in the repo; sync leaves it alone.
		c.st, c.note, c.err = nil, i18n.T("tui.conflicts.left_open"), nil
		return c.reload(env)
	case "up", "k":
		c.file, c.hunk = max(0, c.file-1), 0
	case "down", "j":
		c.file, c.hunk = min(len(c.st.Files)-1, c.file+1), 0
	case "left", "p":
		c.hunk = max(0, c.hunk-1)
	case "right", "n":
		c.hunk = min(max(0, len(c.hunks())-1), c.hunk+1)
	case "m", "t", "b", "M", "T", "B":
		return c.pick(env, k)
	case "e":
		return c.edit(env)
	case "f":
		return c.finish(env)
	case "a":
		return c.confirmAbort(env)
	}
	return nil
}

var choiceKeys = map[string]conflict.Choice{"m": conflict.Mine, "t": conflict.Theirs, "b": conflict.Both}

// pick settles the current hunk, or the whole file with a capital letter
// or when the file has no hunks (binary files, deletions).
func (c *ConflictsScreen) pick(env *Env, k string) tea.Cmd {
	f, ok := c.current()
	if !ok || f.Resolved {
		return nil
	}
	choice := choiceKeys[strings.ToLower(k)]
	hunk := c.hunk
	if k != strings.ToLower(k) || len(c.hunks()) == 0 {
		hunk = -1
	}
	repo, path := c.st.Repo.ID, f.Path
	return c.op(env, "", func(b ConflictBackend) (*ConflictState, error) {
		return b.ConflictResolve(env.Ctx, repo, path, hunk, choice)
	})
}

func (c *ConflictsScreen) edit(env *Env) tea.Cmd {
	f, ok := c.current()
	b := backendOf(env)
	if !ok || f.Resolved || b == nil {
		return nil
	}
	cmd, err := b.ConflictEditor(env.Ctx, c.st.Repo.ID, f.Path)
	if err != nil {
		c.err = err
		return nil
	}
	path := f.Path
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{path, err} })
}

func (c *ConflictsScreen) finish(env *Env) tea.Cmd {
	if !c.allResolved() {
		c.err = errors.New(i18n.T("tui.conflicts.not_yet"))
		return nil
	}
	repo := c.st.Repo.ID
	return c.end(env, i18n.T("conflicts.finished"), func(b ConflictBackend) error {
		return b.ConflictFinish(env.Ctx, repo)
	})
}

func (c *ConflictsScreen) confirmAbort(env *Env) tea.Cmd {
	repo := c.st.Repo
	return OpenDialog(&Dialog{
		Title:   i18n.T("tui.conflicts.abort_title"),
		Lines:   []string{i18n.T("tui.conflicts.abort_text", "repo", repo.Path)},
		Buttons: []string{i18n.T("tui.conflicts.abort"), i18n.T("tui.conflicts.keep_going")},
		Focus:   1, Cancel: 1,
		OnChoose: func(i int) tea.Cmd {
			if i != 0 {
				return nil
			}
			return c.end(env, i18n.T("conflicts.aborted"), func(b ConflictBackend) error {
				return b.ConflictAbort(env.Ctx, repo.ID)
			})
		},
	})
}

func (c *ConflictsScreen) confirmDismiss(env *Env) tea.Cmd {
	if c.cursor >= len(c.list) {
		return nil
	}
	item := c.list[c.cursor]
	return OpenDialog(&Dialog{
		Title:   i18n.T("tui.conflicts.dismiss_title"),
		Lines:   []string{i18n.T("tui.conflicts.dismiss_text", "repo", item.Repo)},
		Buttons: []string{i18n.T("tui.conflicts.dismiss"), i18n.T("tui.conflicts.keep")},
		Focus:   1, Cancel: 1,
		OnChoose: func(i int) tea.Cmd {
			if i != 0 {
				return nil
			}
			return c.end(env, i18n.T("conflicts.dismissed"), func(b ConflictBackend) error {
				return b.ConflictDismiss(env.Ctx, item.RepoID)
			})
		},
	})
}

func (c *ConflictsScreen) click(z ZoneClick, env *Env) tea.Cmd {
	switch {
	case strings.HasPrefix(z.ID, "cf:repo:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(z.ID, "cf:repo:"))
		c.cursor = i
		if z.Double {
			return c.key("enter", env)
		}
	case strings.HasPrefix(z.ID, "cf:file:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(z.ID, "cf:file:"))
		if c.st != nil && i < len(c.st.Files) {
			c.file, c.hunk = i, 0
		}
	case strings.HasPrefix(z.ID, "cf:key:"):
		return c.key(strings.TrimPrefix(z.ID, "cf:key:"), env)
	}
	return nil
}

// ---- view ----

func (c *ConflictsScreen) View(f Frame) string {
	if c.st == nil {
		return c.listView(f)
	}
	return c.sessionView(f)
}

func (c *ConflictsScreen) status(f Frame) []string {
	s := f.S
	switch {
	case c.err != nil:
		lines := []string{" " + s.Fail.Render(s.G.Fail+" "+plainErr(c.err))}
		var ce *core.Error
		if errors.As(c.err, &ce) && ce.Hint != "" {
			lines = append(lines, "   "+s.Hint.Render(i18n.T("error.hint")+": ")+s.Text.Render(ce.Hint))
		}
		return lines
	case c.working:
		return []string{" " + s.Muted.Render(i18n.T("tui.conflicts.working"))}
	case c.note != "":
		return []string{" " + s.OK.Render(s.G.OK+" "+c.note)}
	}
	return nil
}

func plainErr(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

// buttonRows draws clickable buttons whose zones send keys, from row on,
// wrapping onto more rows when the screen is narrow.
func buttonRows(f Frame, row int, keys, labels []string) []string {
	var out []string
	line, x := " ", 1
	for i, k := range keys {
		r := f.S.button(labels[i], false)
		if x > 1 && x+width(r) >= f.Width {
			out, line, x = append(out, line), " ", 1
		}
		f.Zone("cf:key:"+k, x, row+len(out), width(r), 1)
		line += r + "  "
		x += width(r) + 2
	}
	return append(out, line)
}

func (c *ConflictsScreen) listView(f Frame) string {
	s := f.S
	lines := []string{" " + s.Title.Render(i18n.T("tui.conflicts.title")), " " + s.rule(f.Width-2)}
	if len(c.list) == 0 && c.err == nil {
		lines = append(lines, " "+s.OK.Render(s.G.OK+" "+i18n.T("conflicts.none")), "")
		lines = append(lines, c.status(f)...)
		return block(lines, f.Width, f.Height, s.G.Ellipsis)
	}
	pathW := min(40, max(16, f.Width/3))
	for i, item := range c.list {
		cur := "  "
		st := s.Text
		if i == c.cursor {
			cur, st = s.Cursor.Render(s.G.Cursor)+" ", s.Selected
		}
		files := strings.Join(item.Files, ", ")
		if files == "" {
			files = i18n.T("conflicts.files_unknown")
		}
		line := cur + s.Warn.Render(fit(item.Status, 10, s.G.Ellipsis)) + " " + st.Render(fit(item.Repo, pathW, s.G.Ellipsis)) +
			"  " + s.Muted.Render(files)
		f.Zone("cf:repo:"+strconv.Itoa(i), 0, len(lines), f.Width, 1)
		lines = append(lines, " "+line)
	}
	lines = append(lines, "")
	lines = append(lines, c.status(f)...)
	if len(c.list) > 0 {
		lines = append(lines, buttonRows(f, len(lines), []string{"enter", "x"},
			[]string{i18n.T("tui.conflicts.resolve"), i18n.T("tui.conflicts.dismiss")})...)
	}
	return block(lines, f.Width, f.Height, s.G.Ellipsis)
}

func (c *ConflictsScreen) sessionView(f Frame) string {
	s := f.S
	done := 0
	for _, fl := range c.st.Files {
		if fl.Resolved {
			done++
		}
	}
	head := " " + s.Title.Render(c.st.Repo.Path) + s.Muted.Render(s.G.Sep+
		i18n.T("tui.conflicts.progress", "done", done, "total", len(c.st.Files)))
	lines := []string{head, " " + s.rule(f.Width-2)}

	// Bottom: status and buttons; the panes get the rest.
	bottom := c.status(f)
	keys := []string{"m", "t", "b", "e", "f", "a"}
	labels := []string{i18n.T("tui.conflicts.mine"), i18n.T("tui.conflicts.theirs"), i18n.T("tui.conflicts.both"),
		i18n.T("tui.conflicts.edit"), i18n.T("tui.conflicts.finish"), i18n.T("tui.conflicts.abort")}
	nButtons := len(buttonRows(Frame{S: f.S, Width: f.Width}, 0, keys, labels)) // measure only: no zones
	bodyH := max(3, f.Height-len(lines)-len(bottom)-1-nButtons)

	listW := 0
	if !f.Compact {
		listW = min(34, f.Width/3)
	}
	var left []string
	if listW > 0 {
		for i, fl := range c.st.Files {
			mark, st := s.Warn.Render(s.G.Fail), s.Text
			if fl.Resolved {
				mark, st = s.OK.Render(s.G.OK), s.Muted
			}
			cur := " "
			if i == c.file {
				cur, st = s.Cursor.Render(s.G.Cursor), s.Selected
			}
			f.Zone("cf:file:"+strconv.Itoa(i), 0, len(lines)+len(left), listW, 1)
			left = append(left, fit(cur+mark+" "+st.Render(fl.Path), listW, s.G.Ellipsis))
		}
	}
	right := c.filePane(f, f.Width-listW-3)
	for i := 0; i < bodyH; i++ {
		l, r := strings.Repeat(" ", listW), ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if listW > 0 {
			lines = append(lines, " "+l+s.Rule.Render("│")+" "+r)
		} else {
			lines = append(lines, " "+r)
		}
	}
	lines = append(lines, bottom...)
	lines = append(lines, "")
	lines = append(lines, buttonRows(f, len(lines), keys, labels)...)
	return block(lines, f.Width, f.Height, s.G.Ellipsis)
}

// filePane shows the current file: in compact mode its name first, then
// its state or the current hunk, theirs beside (or above) mine.
func (c *ConflictsScreen) filePane(f Frame, w int) []string {
	s := f.S
	fl, ok := c.current()
	if !ok {
		return nil
	}
	var out []string
	if f.Compact {
		out = append(out, s.Label.Render(i18n.T("tui.conflicts.file_n", "n", c.file+1, "total", len(c.st.Files))+" "+fl.Path))
	}
	if fl.Resolved {
		return append(out, s.OK.Render(s.G.OK+" "+i18n.T("conflicts.resolved")))
	}
	hs := c.hunks()
	if len(hs) == 0 {
		return append(out, s.Warn.Render(i18n.T("conflicts.kind."+fl.Kind)), "",
			s.Muted.Render(i18n.T("tui.conflicts.whole_file")))
	}
	h := hs[min(c.hunk, len(hs)-1)]
	out = append(out, s.Label.Render(i18n.T("tui.conflicts.hunk_of", "n", c.hunk+1, "total", len(hs), "line", h.Line))+
		s.Muted.Render(s.G.Sep+i18n.T("conflicts.kind."+fl.Kind)))
	theirs, mine := sideLines(h.Theirs), sideLines(h.Mine)
	if w >= 70 {
		half := (w - 3) / 2
		out = append(out, s.Accent.Render(fit(i18n.T("conflicts.side_theirs"), half, s.G.Ellipsis))+" "+s.Rule.Render("│")+" "+
			s.Accent.Render(i18n.T("conflicts.side_mine")))
		for i := 0; i < max(len(theirs), len(mine)); i++ {
			l, r := "", ""
			if i < len(theirs) {
				l = theirs[i]
			}
			if i < len(mine) {
				r = mine[i]
			}
			out = append(out, s.Text.Render(fit(l, half, s.G.Ellipsis))+" "+s.Rule.Render("│")+" "+s.Text.Render(truncate(r, half, s.G.Ellipsis)))
		}
		return out
	}
	out = append(out, s.Accent.Render(i18n.T("conflicts.side_theirs")))
	for _, l := range theirs {
		out = append(out, s.Text.Render(truncate("  "+l, w, s.G.Ellipsis)))
	}
	out = append(out, s.Accent.Render(i18n.T("conflicts.side_mine")))
	for _, l := range mine {
		out = append(out, s.Text.Render(truncate("  "+l, w, s.G.Ellipsis)))
	}
	return out
}

// sideLines splits one side of a hunk into display lines, with tabs
// expanded and line endings dropped.
func sideLines(text string) []string {
	if text == "" {
		return []string{i18n.T("conflicts.empty_side")}
	}
	var out []string
	for _, l := range strings.SplitAfter(text, "\n") {
		if l != "" {
			out = append(out, strings.ReplaceAll(strings.TrimRight(l, "\r\n"), "\t", "    "))
		}
	}
	return out
}
