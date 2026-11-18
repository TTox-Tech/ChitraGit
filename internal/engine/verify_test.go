package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/scan"
)

// Verify finds a snapshot object that is no longer pinned, and one that is
// gone from the repo.
func TestVerifyPinnedObjects(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	repo := filepath.Join(root, "acme", "app")
	git(t, ".", "init", "-q", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	first := git(t, repo, "rev-parse", "HEAD")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.Repos(ctx)

	rep, err := Verify(ctx, root, g, st)
	if err != nil || !rep.OK || rep.Checks[1].Name != CheckPinned || rep.Checks[1].Summary != "no snapshots yet" {
		t.Fatalf("before snapshots: %+v %v", rep, err)
	}
	if taken := TakeSnapshots(ctx, root, repos, g, st); len(taken) != 1 || taken[0].Error != "" {
		t.Fatalf("snapshot: %+v", taken)
	}
	rep, err = Verify(ctx, root, g, st)
	if err != nil || !rep.OK || !strings.Contains(rep.Checks[1].Summary, "1 snapshot objects in 1 repos, all present and pinned") {
		t.Fatalf("after a snapshot: %+v %v", rep.Checks, err)
	}

	// A pin removed by hand: the object is still there, but gc may take it.
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, repo, "update-ref", "-d", snapRefPrefix+first)
	rep, _ = Verify(ctx, root, g, st)
	if rep.OK || len(rep.Checks[1].Problems) != 1 || !strings.Contains(rep.Checks[1].Problems[0], "1 snapshot objects are not pinned") ||
		!strings.Contains(rep.Checks[1].Problems[0], first[:12]) {
		t.Fatalf("unpinned: %+v", rep.Checks[1])
	}

	// The object itself gone: that snapshot can no longer be restored.
	if _, err := st.DB.ExecContext(ctx, `UPDATE snapshot_refs SET sha = ?`, strings.Repeat("ab", 20)); err != nil {
		t.Fatal(err)
	}
	rep, _ = Verify(ctx, root, g, st)
	if rep.OK || !strings.Contains(strings.Join(rep.Checks[1].Problems, "\n"), "1 snapshot objects are missing") {
		t.Fatalf("missing: %+v", rep.Checks[1])
	}
	if !rep.Checks[0].OK {
		t.Errorf("the ledger check failed too: %+v", rep.Checks[0])
	}
}
