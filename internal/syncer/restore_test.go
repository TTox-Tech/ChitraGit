package syncer

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestPlanRestore(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	ctx := context.Background()
	main := git(t, local, "rev-parse", "HEAD")
	target := map[string]string{
		"refs/heads/main":          "1111111111111111111111111111111111111111", // moved
		"refs/remotes/origin/main": main,                                       // unchanged
		"refs/tags/v1":             main,                                       // gone since: create
	}
	git(t, local, "branch", "spike") // new since: delete
	moves, now, err := PlanRestore(ctx, runner(t), local, target)
	if err != nil {
		t.Fatal(err)
	}
	want := []RefMove{
		{Ref: "refs/heads/main", From: main, To: target["refs/heads/main"]},
		{Ref: "refs/heads/spike", From: main},
		{Ref: "refs/tags/v1", To: main},
	}
	if !slices.Equal(moves, want) {
		t.Fatalf("moves = %+v, want %+v", moves, want)
	}
	if now["refs/heads/spike"] != main || len(now) != 3 {
		t.Fatalf("now = %v", now)
	}
	if moves, _, err := PlanRestore(ctx, runner(t), local, now); err != nil || len(moves) != 0 {
		t.Fatalf("plan to the current refs = %+v, %v", moves, err)
	}
}

func TestApplyRestore(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	ctx, g := context.Background(), runner(t)
	git(t, local, "tag", "v1")
	old, _, err := readRefs(ctx, g, local)
	if err != nil {
		t.Fatal(err)
	}
	write(t, local, "b.txt", "two")
	git(t, local, "add", ".")
	git(t, local, "commit", "-q", "-m", "two")
	git(t, local, "tag", "-d", "v1")
	git(t, local, "branch", "spike")

	moves, _, err := PlanRestore(ctx, g, local, old)
	if err != nil || len(moves) != 3 {
		t.Fatalf("plan = %+v, %v", moves, err)
	}
	if err := ApplyRestore(ctx, g, local, moves); err != nil {
		t.Fatal(err)
	}
	now, _, _ := readRefs(ctx, g, local)
	if !maps.Equal(now, old) {
		t.Fatalf("refs after restore = %v, want %v", now, old)
	}
	if _, err := os.Stat(filepath.Join(local, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("checked-out files did not follow the branch: %v", err)
	}
	if st := git(t, local, "status", "--porcelain"); st != "" {
		t.Fatalf("tree not clean after restore: %q", st)
	}
}

// One stale ref fails the whole transaction: no ref moves, and the
// checked-out branch is put back.
func TestApplyRestoreAllOrNothing(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	ctx, g := context.Background(), runner(t)
	one := git(t, local, "rev-parse", "HEAD")
	write(t, local, "b.txt", "two")
	git(t, local, "add", ".")
	git(t, local, "commit", "-q", "-m", "two")
	two := git(t, local, "rev-parse", "HEAD")
	git(t, local, "branch", "spike")
	moves := []RefMove{
		{Ref: "refs/heads/main", From: two, To: one},
		{Ref: "refs/heads/spike", From: one, To: two}, // stale: spike is at two
		{Ref: "refs/tags/v9", To: one},
	}
	if err := ApplyRestore(ctx, g, local, moves); err == nil {
		t.Fatal("stale restore succeeded")
	}
	if h := git(t, local, "rev-parse", "HEAD"); h != two {
		t.Fatalf("HEAD = %s, want %s", h, two)
	}
	if _, err := os.Stat(filepath.Join(local, "b.txt")); err != nil {
		t.Fatalf("files not put back: %v", err)
	}
	if out := git(t, local, "tag", "-l", "v9"); out != "" {
		t.Fatal("tag v9 was created by a failed restore")
	}
}

func TestApplyRestoreRefuses(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	ctx, g := context.Background(), runner(t)
	one := git(t, local, "rev-parse", "HEAD")
	write(t, local, "b.txt", "two")
	git(t, local, "add", ".")
	git(t, local, "commit", "-q", "-m", "two")
	two := git(t, local, "rev-parse", "HEAD")
	back := []RefMove{{Ref: "refs/heads/main", From: two, To: one}}

	write(t, local, "a.txt", "edited")
	if err := ApplyRestore(ctx, g, local, back); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("dirty tree: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(got) != "edited" {
		t.Fatalf("local change lost: %q", got)
	}
	git(t, local, "checkout", "-q", "--", "a.txt")

	write(t, local, "new.txt", "untracked")
	if err := ApplyRestore(ctx, g, local, back); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("untracked file: %v", err)
	}
	os.Remove(filepath.Join(local, "new.txt"))

	if err := ApplyRestore(ctx, g, local, []RefMove{{Ref: "refs/heads/main", From: two}}); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("deleting the checked-out branch: %v", err)
	}
	missing := []RefMove{{Ref: "refs/tags/x", To: "1111111111111111111111111111111111111111"}}
	if err := ApplyRestore(ctx, g, local, missing); core.CategoryOf(err) != core.CatCorrupt {
		t.Fatalf("missing object: %v", err)
	}
	git(t, local, "branch", "side", one)
	git(t, local, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "side")
	if err := ApplyRestore(ctx, g, local, []RefMove{{Ref: "refs/heads/side", From: one, To: two}}); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("branch of another worktree: %v", err)
	}
	if h := git(t, local, "rev-parse", "HEAD"); h != two {
		t.Fatalf("a refused restore moved HEAD to %s", h)
	}
}
