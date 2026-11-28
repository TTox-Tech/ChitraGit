package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// showCommit prints one commit as `git show` does: the header, the files it
// changed with their line counts, then the diff, coloured in a terminal. A
// merge is shown against its first parent, a root commit against nothing.
func (a *app) showCommit(ctx context.Context, o *objectArg, stat bool) error {
	d, err := o.git.CommitDiff(ctx, o.dir, o.rev, engine.DiffOptions{NoPatch: stat})
	if err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "repo": o.repo.Path, "commit": d})
	}
	st, w := a.styles, a.out
	fmt.Fprintln(w, st.Accent.Render("commit "+d.SHA))
	if len(d.Parents) > 1 {
		short := make([]string, len(d.Parents))
		for i, p := range d.Parents {
			short[i] = p[:min(len(p), 8)]
		}
		fmt.Fprintln(w, st.Label.Render(i18n.T("commit.merge")), strings.Join(short, " "))
	}
	fmt.Fprintf(w, "%s %s <%s>\n", st.Label.Render(i18n.T("commit.author")), d.Author, d.AuthorEmail)
	fmt.Fprintf(w, "%s %s\n", st.Label.Render(i18n.T("commit.date")), d.AuthorDate)
	if d.Committer != d.Author || d.CommitterEmail != d.AuthorEmail {
		fmt.Fprintf(w, "%s %s <%s>\n", st.Label.Render(i18n.T("commit.committer")), d.Committer, d.CommitterEmail)
	}
	fmt.Fprintln(w)
	for _, l := range strings.Split(d.Message, "\n") {
		fmt.Fprintln(w, "    "+l)
	}
	fmt.Fprintln(w)
	names := make([]string, len(d.Files))
	width := 0
	for i, f := range d.Files {
		names[i] = f.Path
		if f.OldPath != "" {
			names[i] = f.OldPath + " → " + f.Path
		}
		width = max(width, min(len([]rune(names[i])), 60))
	}
	added, deleted := 0, 0
	for i, f := range d.Files {
		name := names[i] + strings.Repeat(" ", max(0, width-len([]rune(names[i]))))
		counts := st.OK.Render(fmt.Sprintf("+%d", f.Added)) + " " + st.Fail.Render(fmt.Sprintf("-%d", f.Deleted))
		if f.Binary {
			counts = st.Muted.Render(i18n.T("commit.binary"))
		}
		fmt.Fprintf(w, " %s %s  %s\n", st.Label.Render(f.Status), name, counts)
		added, deleted = added+f.Added, deleted+f.Deleted
	}
	fmt.Fprintln(w, st.Muted.Render(" "+i18n.T("commit.total", "files", len(d.Files), "added", added, "deleted", deleted)))
	if stat {
		return nil
	}
	for _, f := range d.Files {
		if f.Patch == "" {
			continue
		}
		fmt.Fprintln(w)
		for _, l := range strings.SplitAfter(f.Patch, "\n") {
			fmt.Fprint(w, a.diffLine(l))
		}
	}
	if d.Truncated {
		fmt.Fprintln(w)
		a.warn(i18n.T("commit.cut", "dir", o.dir, "sha", d.SHA[:min(len(d.SHA), 12)]))
	}
	return nil
}

// diffLine colours one line of a patch: additions, deletions, hunk heads
// and file heads.
func (a *app) diffLine(l string) string {
	st := a.styles
	text, nl := strings.CutSuffix(l, "\n")
	switch {
	case strings.HasPrefix(text, "+++"), strings.HasPrefix(text, "---"), strings.HasPrefix(text, "diff --git"):
		text = st.Title.Render(text)
	case strings.HasPrefix(text, "@@"):
		text = st.Accent.Render(text)
	case strings.HasPrefix(text, "+"):
		text = st.OK.Render(text)
	case strings.HasPrefix(text, "-"):
		text = st.Fail.UnsetBold().Render(text)
	}
	if nl {
		text += "\n"
	}
	return text
}
