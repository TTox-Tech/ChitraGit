package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

type undoFixture struct {
	root, repo string
	g          *gitx.Runner
	st         *store.Store
	run        int64  // the sync that brought two commits
	before     string // main before that sync
	after      string // main after it
}

// newUndoFixture clones an upstream into the archive, pushes two commits
// (one changes a.txt) and syncs them in.
func newUndoFixture(t *testing.T) *undoFixture {
	t.Helper()
	ctx := context.Background()
	f := &undoFixture{}
	f.root, f.g, f.st = setup(t)
	dev := upstreamClone(t, f.root)
	f.repo = filepath.Join(f.root, "r")
	f.before = git(t, f.repo, "rev-parse", "HEAD")
	if _, err := Scan(ctx, f.root, scan.Options{}, f.g, f.st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dev, "add", "a.txt")
	git(t, dev, "commit", "-q", "-m", "two")
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "three")
	git(t, dev, "push", "-q", "origin", "HEAD")
	rep, err := Sync(ctx, SyncRequest{Root: f.root, Options: syncer.Options{Mode: "ff"}}, f.g, f.st)
	if err != nil {
		t.Fatal(err)
	}
	f.run, f.after = rep.RunID, git(t, f.repo, "rev-parse", "HEAD")
	if f.after == f.before {
		t.Fatal("sync did not move main")
	}
	return f
}

func (f *undoFixture) plan(t *testing.T) *UndoPlan {
	t.Helper()
	p, err := PlanUndo(context.Background(), f.root, f.run, f.g, f.st)
	if err != nil || len(p.Repos) != 1 || p.Repos[0].Error != "" {
		t.Fatalf("plan = %+v %v", p, err)
	}
	return p
}

// TestUndoAndUndoTheUndo undoes a sync, files included, then puts the sync
// back with restore to the snapshot the undo took first.
func TestUndoAndUndoTheUndo(t *testing.T) {
	ctx := context.Background()
	f := newUndoFixture(t)
	head := "refs/heads/" + git(t, f.repo, "branch", "--show-current")
	p := f.plan(t)
	if u := p.Repos[0]; len(u.Moves) != 2 || len(u.Kept) != 0 {
		t.Fatalf("moves %+v kept %v", u.Moves, u.Kept)
	}
	if n := Undo(ctx, f.root, p, false, f.g, f.st); n != 0 {
		t.Fatalf("undo failed: %+v", p.Repos)
	}
	if got := git(t, f.repo, "rev-parse", head, "refs/remotes/origin/HEAD"); got != f.before+"\n"+f.before {
		t.Errorf("after undo: %s, want %s", got, f.before)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt is still there after undo: %v", err)
	}
	// Undoing again changes nothing: the refs are no longer where the run left them.
	if again := f.plan(t); len(again.Repos[0].Moves) != 0 || len(again.Repos[0].Kept) != 2 {
		t.Errorf("second undo plan = %+v", again.Repos[0])
	}

	r, err := f.st.FindRepo(ctx, p.Repos[0].RepoID)
	if err != nil {
		t.Fatal(err)
	}
	back, err := PlanRestore(ctx, f.root, r, p.Repos[0].Pre, f.g, f.st)
	if err != nil || Restore(ctx, f.root, r, back, false, f.g, f.st) != nil {
		t.Fatalf("restore after undo: %v", err)
	}
	if got := git(t, f.repo, "rev-parse", head); got != f.after {
		t.Errorf("after undoing the undo: %s, want %s", got, f.after)
	}
	if b, err := os.ReadFile(filepath.Join(f.repo, "a.txt")); err != nil || string(b) != "two\n" {
		t.Errorf("a.txt after undoing the undo: %q %v", b, err)
	}
}

// TestUndoKeepsLaterWork commits on the branch after the sync and adds a
// branch: undo puts back only the remote-tracking ref and keeps the rest.
func TestUndoKeepsLaterWork(t *testing.T) {
	ctx := context.Background()
	f := newUndoFixture(t)
	head := "refs/heads/" + git(t, f.repo, "branch", "--show-current")
	git(t, f.repo, "commit", "-q", "--allow-empty", "-m", "mine")
	mine := git(t, f.repo, "rev-parse", "HEAD")
	git(t, f.repo, "branch", "later")
	p := f.plan(t)
	u := p.Repos[0]
	if len(u.Moves) != 1 || u.Moves[0].Ref == head || len(u.Kept) != 1 || u.Kept[0] != head {
		t.Fatalf("moves %+v kept %v", u.Moves, u.Kept)
	}
	if n := Undo(ctx, f.root, p, false, f.g, f.st); n != 0 {
		t.Fatalf("undo failed: %+v", p.Repos)
	}
	if got := git(t, f.repo, "rev-parse", head, "later", u.Moves[0].Ref); got != mine+"\n"+mine+"\n"+f.before {
		t.Errorf("after undo:\n%s", got)
	}
}

// A dirty checked-out branch needs --stash; without it the repo is left as
// it was and the error is reported.
func TestUndoDirtyNeedsStash(t *testing.T) {
	ctx := context.Background()
	f := newUndoFixture(t)
	if err := os.WriteFile(filepath.Join(f.repo, "b.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := f.plan(t)
	if n := Undo(ctx, f.root, p, false, f.g, f.st); n != 1 || p.Repos[0].Error == "" {
		t.Fatalf("dirty undo without stash: %d %+v", n, p.Repos)
	}
	if got := git(t, f.repo, "rev-parse", "HEAD"); got != f.after {
		t.Errorf("repo moved by a failed undo: %s", got)
	}
	p = f.plan(t)
	if n := Undo(ctx, f.root, p, true, f.g, f.st); n != 0 {
		t.Fatalf("undo --stash: %+v", p.Repos)
	}
	if b, err := os.ReadFile(filepath.Join(f.repo, "b.txt")); err != nil || string(b) != "wip\n" ||
		git(t, f.repo, "rev-parse", "HEAD") != f.before {
		t.Errorf("after undo --stash: %q %v", b, err)
	}
}
