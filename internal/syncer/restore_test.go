package syncer

import (
	"context"
	"slices"
	"testing"
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
