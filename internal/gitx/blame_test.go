package gitx

import (
	"context"
	"testing"
)

func TestBlame(t *testing.T) {
	r := newRunner(t)
	dir, c := historyRepo(t)
	ctx := context.Background()

	b, err := r.Blame(ctx, dir, "main", "README.md", BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		sha, text string
	}{{c["root"], "one"}, {c["change"], "2"}, {c["root"], "three"}, {c["change"], "four"}}
	if len(b.Lines) != len(want) || b.Truncated {
		t.Fatalf("lines = %+v", b.Lines)
	}
	for i, w := range want {
		if l := b.Lines[i]; l.Line != i+1 || l.SHA != w.sha || l.Text != w.text {
			t.Errorf("line %d = %+v, want %s %q", i+1, l, w.sha[:7], w.text)
		}
	}
	if rc := b.Commits[c["root"]]; rc.Author != "t" || rc.AuthorEmail != "t@example.com" || rc.Summary != "root" || rc.AuthorDate == "" {
		t.Errorf("root commit = %+v", rc)
	}

	// Renames are followed; the cap stops early.
	mv, err := r.Blame(ctx, dir, "main", "new name.txt", BlameOptions{MaxLines: 5})
	if err != nil || len(mv.Lines) != 5 || !mv.Truncated || mv.Commits[c["root"]].Path != "old name.txt" {
		t.Errorf("renamed = %+v %v", mv, err)
	}

	for _, p := range []string{"../x", "/etc/passwd", "", "nope.txt"} {
		if _, err := r.Blame(ctx, dir, "main", p, BlameOptions{}); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	if _, err := r.Blame(ctx, dir, "--since=1", "README.md", BlameOptions{}); err == nil {
		t.Error("an option-like rev was accepted")
	}
}
