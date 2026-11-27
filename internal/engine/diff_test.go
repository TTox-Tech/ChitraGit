package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// TestDiffSnapshots moves one branch forward, rewrites another, deletes a
// third and adds a tag between two snapshots, and checks each change and
// the totals, against the second snapshot and against the refs now.
func TestDiffSnapshots(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	repo := filepath.Join(root, "r")
	git(t, ".", "init", "-q", "-b", "main", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, repo, "branch", "rewrite")
	git(t, repo, "branch", "gone")
	git(t, repo, "switch", "-q", "rewrite")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "old work")
	git(t, repo, "switch", "-q", "main")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	repos, err := st.Repos(ctx)
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos %v %v", repos, err)
	}
	r := repos[0]
	take := func() int64 {
		t.Helper()
		got := TakeSnapshots(ctx, root, repos, g, st)
		if got[0].Error != "" || !got[0].Created {
			t.Fatalf("snapshot: %+v", got[0])
		}
		return got[0].ID
	}
	a := take()

	git(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "three")
	git(t, repo, "branch", "-f", "rewrite", "main~2")
	git(t, repo, "switch", "-q", "rewrite")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "new work")
	git(t, repo, "switch", "-q", "main")
	git(t, repo, "branch", "-q", "-D", "gone")
	git(t, repo, "tag", "-a", "v1", "-m", "v1")
	b := take()

	want := map[string]RefChange{
		"refs/heads/main":    {Kind: DiffForward, New: 2},
		"refs/heads/rewrite": {Kind: DiffRewritten, New: 1, Dropped: 1},
		"refs/heads/gone":    {Kind: DiffDeleted},
		"refs/tags/v1":       {Kind: DiffAdded, New: 2},
	}
	for _, to := range []int64{b, 0} {
		d, err := DiffSnapshots(ctx, root, r, a, to, g, st)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Changes) != len(want) {
			t.Fatalf("to %d: changes %+v", to, d.Changes)
		}
		for _, c := range d.Changes {
			w := want[c.Ref]
			if c.Kind != w.Kind || c.New != w.New || c.Dropped != w.Dropped {
				t.Errorf("to %d: %s = %+v, want %+v", to, c.Ref, c, w)
			}
		}
		// two, three and new work are new; old work is reachable from no ref.
		// The tag counts only what the old refs lacked; gone dropped nothing.
		if d.New != 3 || d.Removed != 1 || d.From != a || d.To != to || d.Path != r.Path {
			t.Errorf("to %d: totals %+v", to, d)
		}
	}

	if d, err := DiffSnapshots(ctx, root, r, b, 0, g, st); err != nil || len(d.Changes) != 0 || d.New != 0 {
		t.Errorf("unchanged repo: %+v %v", d, err)
	}

	// Diffing against the refs now pins nothing new.
	pins := git(t, repo, "for-each-ref", "--format=%(refname)", "refs/chitragit/snap/")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "four")
	if _, err := DiffSnapshots(ctx, root, r, b, 0, g, st); err != nil {
		t.Fatal(err)
	}
	if now := git(t, repo, "for-each-ref", "--format=%(refname)", "refs/chitragit/snap/"); now != pins {
		t.Errorf("diff pinned objects:\n%s\nwas\n%s", now, pins)
	}
}

func TestDiffRefsOtherRepoSnapshot(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		git(t, ".", "init", "-q", dir)
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	}
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.Repos(ctx)
	taken := TakeSnapshots(ctx, root, repos, g, st)
	if _, err := DiffSnapshots(ctx, root, repos[0], taken[1].ID, 0, g, st); err == nil {
		t.Error("diff with another repo's snapshot: no error")
	}
}

// A tag on a tree has no commit to count; it is still reported.
func TestDiffRefsTreeTag(t *testing.T) {
	ctx := context.Background()
	_, g, _ := setup(t)
	dir := t.TempDir()
	git(t, ".", "init", "-q", dir)
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	head := git(t, dir, "rev-parse", "HEAD")
	d, err := DiffRefs(ctx, g, dir, map[string]string{"refs/tags/t": head}, map[string]string{"refs/tags/t": tree})
	if err != nil || len(d.Changes) != 1 || d.Changes[0].Kind != DiffMoved || d.Removed != 1 {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDiffRun(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	dev := upstreamClone(t, root)
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "three")
	git(t, dev, "push", "-q", "origin", "HEAD")
	rep, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff"}}, g, st)
	if err != nil {
		t.Fatal(err)
	}
	diffs, errs, err := DiffRun(ctx, root, rep.RunID, g, st)
	if err != nil || len(errs) != 0 || len(diffs) != 1 {
		t.Fatalf("DiffRun = %+v %v %v", diffs, errs, err)
	}
	// The local branch and its remote-tracking ref both moved by two.
	if d := diffs[0]; d.Path != "r" || d.New != 2 || d.Removed != 0 || len(d.Changes) != 2 || d.Changes[0].Kind != DiffForward {
		t.Errorf("diff = %+v", d)
	}

	// From 0 is an empty repo, as before a clone: every ref is new.
	repos, _ := st.Repos(ctx)
	d, err := DiffSnapshots(ctx, root, repos[0], 0, 0, g, st)
	if err != nil || d.New != 3 || len(d.Changes) != 2 || d.Changes[0].Kind != DiffAdded {
		t.Errorf("from nothing = %+v %v", d, err)
	}
}
