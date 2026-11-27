package gitx

import (
	"context"
	"testing"
)

func TestGrep(t *testing.T) {
	r := newRunner(t)
	dir, c := historyRepo(t)
	ctx := context.Background()

	g, err := r.Grep(ctx, dir, "main", "same line", GrepOptions{Fixed: true})
	if err != nil || len(g.Matches) != 20 || g.Truncated || g.Matches[0].Path != "new name.txt" || g.Matches[19].Line != 20 {
		t.Fatalf("grep = %+v %v", g, err)
	}
	if g, err := r.Grep(ctx, dir, "main", "same line", GrepOptions{MaxMatches: 3}); err != nil || len(g.Matches) != 3 || !g.Truncated {
		t.Errorf("capped = %+v %v", g, err)
	}
	if g, err := r.Grep(ctx, dir, "main", "नमस्ते", GrepOptions{}); err != nil || len(g.Matches) != 1 || g.Matches[0].Path != "docs/नमस्ते 👋.md" {
		t.Errorf("unicode = %+v %v", g, err)
	}
	if g, err := r.Grep(ctx, dir, "main", "^T[wh]", GrepOptions{IgnoreCase: true, Paths: []string{"README.md"}}); err != nil || len(g.Matches) != 1 || g.Matches[0].Text != "three" {
		t.Errorf("regexp = %+v %v", g, err)
	}
	// Nothing found is not an error; binaries are skipped; old trees work.
	for _, q := range []string{"no such text", "binary"} {
		if g, err := r.Grep(ctx, dir, "main", q, GrepOptions{Fixed: true}); err != nil || len(g.Matches) != 0 {
			t.Errorf("%q = %+v %v", q, g, err)
		}
	}
	if g, err := r.Grep(ctx, dir, c["root"], "bye", GrepOptions{}); err != nil || len(g.Matches) != 1 || g.Matches[0].Path != "gone.txt" {
		t.Errorf("root tree = %+v %v", g, err)
	}

	if _, err := r.Grep(ctx, dir, "main", "a\nb", GrepOptions{}); err == nil {
		t.Error("a multi-line pattern was accepted")
	}
	if _, err := r.Grep(ctx, dir, "main", "(", GrepOptions{}); err == nil {
		t.Error("a broken regexp was accepted")
	}
	if _, err := r.Grep(ctx, dir, "nope", "x", GrepOptions{}); err == nil {
		t.Error("an unknown rev was accepted")
	}
}
