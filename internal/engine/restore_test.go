package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// Sync, upstream force-push, sync again, then restore the first sync's
// post snapshot: every ref equals it, survives an aggressive gc, and the
// restore shows in the repo's history.
func TestRestoreAfterForcePush(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	dev := upstreamClone(t, root)
	repo := filepath.Join(root, "r")
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dev, "push", "-q", "origin", "HEAD")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		if _, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff"}}, g, st); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	snaps, err := st.Snapshots(ctx, "", 1)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %+v, %v", snaps, err)
	}
	good, _ := st.Snapshot(ctx, snaps[0].ID)
	two := git(t, repo, "rev-parse", "HEAD")

	git(t, dev, "reset", "-q", "--hard", "HEAD~1")
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "rewritten")
	git(t, dev, "push", "-q", "-f", "origin", "HEAD")
	sync()
	if git(t, repo, "rev-parse", "refs/remotes/origin/"+git(t, dev, "branch", "--show-current")) == two {
		t.Fatal("the force-push did not reach the archive")
	}

	repos, err := st.Repos(ctx)
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos = %v, %v", repos, err)
	}
	plan, err := PlanRestore(ctx, root, repos[0], good.ID, g, st)
	if err != nil || len(plan.Moves) == 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if err := Restore(ctx, root, repos[0], plan, false, g, st); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "-q", "--prune=now")
	moves, _, err := syncer.PlanRestore(ctx, g, repo, good.Refs)
	if err != nil || len(moves) != 0 {
		t.Fatalf("refs differ from snapshot %d after restore: %+v, %v", good.ID, moves, err)
	}
	if git(t, repo, "rev-parse", "HEAD") != two || git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("working tree did not follow the restored branch")
	}
	if plan.Pre == 0 {
		t.Fatal("no snapshot of the refs before the restore")
	}
	tl, err := st.Timeline(ctx, repos[0].ID, 1)
	if err != nil || len(tl) != 1 || tl[0].Kind != "restored" {
		t.Fatalf("timeline = %+v, %v", tl, err)
	}
}
