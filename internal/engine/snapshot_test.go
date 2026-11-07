package engine

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestGCKeepsSnapshotObjects rewrites and deletes upstream history, drops
// every other ChitraGit safety ref and the reflogs, runs an aggressive gc,
// and checks that every object any snapshot names is still there.
func TestGCKeepsSnapshotObjects(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	dev := upstreamClone(t, root)
	repo := filepath.Join(root, "r")
	git(t, dev, "checkout", "-q", "-b", "feature")
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "feature work")
	git(t, dev, "push", "-q", "origin", "feature")
	git(t, dev, "tag", "-a", "v1", "-m", "release")
	git(t, dev, "push", "-q", "origin", "v1")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		if _, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff", Prune: true}}, g, st); err != nil {
			t.Fatal(err)
		}
	}
	sync()

	// Upstream force-pushes the feature branch away, then deletes it and the tag.
	git(t, dev, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
	git(t, dev, "push", "-q", "--force", "origin", "feature")
	sync()
	git(t, dev, "push", "-q", "origin", ":feature", ":refs/tags/v1")
	git(t, repo, "tag", "-d", "v1")
	sync()

	for _, ref := range strings.Fields(git(t, repo, "for-each-ref", "--format=%(refname)", "refs/chitragit/rewritten", "refs/chitragit/archived")) {
		git(t, repo, "update-ref", "-d", ref)
	}
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "-q", "--prune=now")

	snaps, err := st.Snapshots(ctx, "", 0)
	if err != nil || len(snaps) < 3 {
		t.Fatalf("snapshots = %d, %v", len(snaps), err)
	}
	checked := 0
	for _, s := range snaps {
		sn, err := st.Snapshot(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		for ref, sha := range sn.Refs {
			if out, err := exec.Command("git", "-C", repo, "cat-file", "-e", sha).CombinedOutput(); err != nil {
				t.Errorf("snapshot %d %s: object %s lost after gc: %s", s.ID, ref, sha, out)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no refs checked")
	}
}
