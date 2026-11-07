package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// upstreamClone makes a bare upstream with one commit, clones it into the
// archive as "r", and returns a dev clone for pushing more.
func upstreamClone(t *testing.T, root string) (dev string) {
	t.Helper()
	up := filepath.Join(t.TempDir(), "up.git")
	git(t, ".", "init", "-q", "--bare", up)
	dev = filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", up, dev)
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dev, "push", "-q", "origin", "HEAD")
	git(t, ".", "clone", "-q", up, filepath.Join(root, "r"))
	return dev
}

func TestSyncTakesPreAndPostSnapshots(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	dev := upstreamClone(t, root)
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dev, "push", "-q", "origin", "HEAD")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	sync := func() *SyncReport {
		rep, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff"}}, g, st)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := sync()
	snaps, err := st.Snapshots(ctx, "", 0)
	if err != nil || len(snaps) != 2 || snaps[1].Kind != store.SnapshotPre || snaps[0].Kind != store.SnapshotPost ||
		snaps[0].RunID != rep.RunID {
		t.Fatalf("after first sync: %+v %v", snaps, err)
	}
	pre, _ := st.Snapshot(ctx, snaps[1].ID)
	post, _ := st.Snapshot(ctx, snaps[0].ID)
	main := "refs/remotes/origin/" + git(t, dev, "branch", "--show-current")
	if pre.Refs[main] == post.Refs[main] || post.Refs[main] != git(t, dev, "rev-parse", "HEAD") {
		t.Fatalf("pre %s, post %s", pre.Refs[main], post.Refs[main])
	}

	for _, sha := range []string{pre.Refs[main], post.Refs[main]} {
		if got := git(t, filepath.Join(root, "r"), "rev-parse", "refs/chitragit/snap/"+sha); got != sha {
			t.Errorf("object %s is not pinned (%s)", sha, got)
		}
	}

	sync() // nothing new upstream: no new snapshot
	if again, _ := st.Snapshots(ctx, "", 0); len(again) != 2 {
		t.Fatalf("unchanged sync stored %d snapshots, want 2", len(again))
	}
}
