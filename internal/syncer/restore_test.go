package syncer

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if err := ApplyRestore(ctx, g, local, moves, false); err != nil {
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
	if err := ApplyRestore(ctx, g, local, moves, false); err == nil {
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
	if err := ApplyRestore(ctx, g, local, back, false); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("dirty tree: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(got) != "edited" {
		t.Fatalf("local change lost: %q", got)
	}
	git(t, local, "checkout", "-q", "--", "a.txt")

	write(t, local, "new.txt", "untracked")
	if err := ApplyRestore(ctx, g, local, back, false); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("untracked file: %v", err)
	}
	os.Remove(filepath.Join(local, "new.txt"))

	if err := ApplyRestore(ctx, g, local, []RefMove{{Ref: "refs/heads/main", From: two}}, false); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("deleting the checked-out branch: %v", err)
	}
	missing := []RefMove{{Ref: "refs/tags/x", To: "1111111111111111111111111111111111111111"}}
	if err := ApplyRestore(ctx, g, local, missing, false); core.CategoryOf(err) != core.CatCorrupt {
		t.Fatalf("missing object: %v", err)
	}
	git(t, local, "branch", "side", one)
	git(t, local, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "side")
	if err := ApplyRestore(ctx, g, local, []RefMove{{Ref: "refs/heads/side", From: one, To: two}}, false); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("branch of another worktree: %v", err)
	}
	if h := git(t, local, "rev-parse", "HEAD"); h != two {
		t.Fatalf("a refused restore moved HEAD to %s", h)
	}
}

// --stash puts uncommitted changes aside and back on the restored branch;
// when they clash with the snapshot's files, every ref and the changes
// are put back exactly as they were.
func TestApplyRestoreStash(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	ctx, g := context.Background(), runner(t)
	one := git(t, local, "rev-parse", "HEAD")
	write(t, local, "b.txt", "two")
	git(t, local, "add", ".")
	git(t, local, "commit", "-q", "-m", "two")
	two := git(t, local, "rev-parse", "HEAD")
	moves := []RefMove{{Ref: "refs/heads/main", From: two, To: one}, {Ref: "refs/tags/v1", To: one}}

	write(t, local, "notes.txt", "mine") // untracked, no clash
	write(t, local, "a.txt", "one\nmine")
	if err := ApplyRestore(ctx, g, local, moves, true); err != nil {
		t.Fatal(err)
	}
	if h := git(t, local, "rev-parse", "HEAD"); h != one {
		t.Fatalf("HEAD = %s, want %s", h, one)
	}
	for name, want := range map[string]string{"notes.txt": "mine", "a.txt": "one\nmine"} {
		if got, _ := os.ReadFile(filepath.Join(local, name)); string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if out := git(t, local, "stash", "list"); out != "" {
		t.Fatalf("stash left behind: %q", out)
	}

	// Back to two with a change to b.txt, which the snapshot does not have.
	git(t, local, "checkout", "-q", "--", "a.txt")
	os.Remove(filepath.Join(local, "notes.txt"))
	git(t, local, "reset", "-q", "--hard", two)
	git(t, local, "tag", "-d", "v1")
	write(t, local, "b.txt", "two\nmine")
	if err := ApplyRestore(ctx, g, local, moves, true); core.CategoryOf(err) != core.CatConflict {
		t.Fatalf("clashing changes: %v", err)
	}
	if h := git(t, local, "rev-parse", "HEAD"); h != two {
		t.Fatalf("HEAD = %s after a clash, want %s", h, two)
	}
	if got, _ := os.ReadFile(filepath.Join(local, "b.txt")); string(got) != "two\nmine" {
		t.Fatalf("change lost: %q", got)
	}
	if out := git(t, local, "tag", "-l", "v1"); out != "" {
		t.Fatal("tag v1 kept after a rolled-back restore")
	}
	if _, err := os.Stat(filepath.Join(local, ".git", markerFile)); !os.IsNotExist(err) {
		t.Fatalf("marker left behind: %v", err)
	}
}

type crashAt string

// A restore killed at any step loses nothing: after the next sync no
// commit is gone, the uncommitted changes are in the tree, and running
// the restore again finishes it.
func TestRestoreSurvivesACrash(t *testing.T) {
	steps := []string{"flow.marked", "flow.stashed", "restore.head", "restore.refs", "restore.applied", "flow.kept", "flow.unmarked"}
	for _, dirty := range []bool{false, true} {
		for _, at := range steps {
			if !dirty && strings.HasPrefix(at, "flow.") || !dirty && at == "restore.applied" {
				continue // steps of the stash flow only
			}
			t.Run(fmt.Sprintf("dirty=%v/%s", dirty, at), func(t *testing.T) {
				f := newFixture(t)
				local := f.clone(t)
				ctx, g := context.Background(), runner(t)
				target, _, err := readRefs(ctx, g, local)
				if err != nil {
					t.Fatal(err)
				}
				one := git(t, local, "rev-parse", "HEAD")
				write(t, local, "b.txt", "two")
				git(t, local, "add", ".")
				git(t, local, "commit", "-q", "-m", "two")
				two := git(t, local, "rev-parse", "HEAD")
				git(t, local, "update-ref", snapPrefix+two, two) // the pre snapshot's pin
				git(t, local, "tag", "v2")
				if dirty {
					write(t, local, "a.txt", "one\nmine")
				}
				moves, _, err := PlanRestore(ctx, g, local, target)
				if err != nil {
					t.Fatal(err)
				}

				reached := func() (hit bool) {
					restore := SetStepHook(func(name string) {
						if name == at {
							panic(crashAt(name))
						}
					})
					defer restore()
					defer func() {
						if v := recover(); v != nil {
							if _, ok := v.(crashAt); !ok {
								panic(v)
							}
							hit = true
						}
					}()
					_ = ApplyRestore(ctx, g, local, moves, dirty)
					return false
				}()
				if !reached {
					t.Fatal("step never reached")
				}

				if r := syncDir(t, local, Options{}); r.Outcome == Failed {
					t.Fatalf("sync after the crash failed: %+v", r)
				}
				if _, err := os.Stat(filepath.Join(local, ".git", markerFile)); !os.IsNotExist(err) {
					t.Fatalf("marker left after the next sync: %v", err)
				}
				for _, sha := range []string{one, two} {
					git(t, local, "cat-file", "-e", sha+"^{commit}")
				}
				if dirty {
					if got, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(got) != "one\nmine" {
						t.Fatalf("uncommitted change lost: %q", got)
					}
				}

				moves, _, err = PlanRestore(ctx, g, local, target)
				if err != nil {
					t.Fatal(err)
				}
				if err := ApplyRestore(ctx, g, local, moves, dirty); err != nil {
					t.Fatalf("restore again: %v", err)
				}
				if left, _, _ := PlanRestore(ctx, g, local, target); len(left) != 0 {
					t.Fatalf("refs still differ: %+v", left)
				}
			})
		}
	}
}
