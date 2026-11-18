package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/guardian"
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

	rep, err := Verify(ctx, root, g, st, VerifyOptions{})
	if err != nil || !rep.OK || rep.Checks[1].Name != CheckPinned || rep.Checks[1].Summary != "no snapshots yet" {
		t.Fatalf("before snapshots: %+v %v", rep, err)
	}
	if taken := TakeSnapshots(ctx, root, repos, g, st); len(taken) != 1 || taken[0].Error != "" {
		t.Fatalf("snapshot: %+v", taken)
	}
	rep, err = Verify(ctx, root, g, st, VerifyOptions{})
	if err != nil || !rep.OK || !strings.Contains(rep.Checks[1].Summary, "1 snapshot objects in 1 repos, all present and pinned") {
		t.Fatalf("after a snapshot: %+v %v", rep.Checks, err)
	}

	// A pin removed by hand: the object is still there, but gc may take it.
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, repo, "update-ref", "-d", snapRefPrefix+first)
	rep, _ = Verify(ctx, root, g, st, VerifyOptions{})
	if rep.OK || len(rep.Checks[1].Problems) != 1 || !strings.Contains(rep.Checks[1].Problems[0], "1 snapshot objects are not pinned") ||
		!strings.Contains(rep.Checks[1].Problems[0], first[:12]) {
		t.Fatalf("unpinned: %+v", rep.Checks[1])
	}

	// The object itself gone: that snapshot can no longer be restored.
	if _, err := st.DB.ExecContext(ctx, `UPDATE snapshot_refs SET sha = ?`, strings.Repeat("ab", 20)); err != nil {
		t.Fatal(err)
	}
	rep, _ = Verify(ctx, root, g, st, VerifyOptions{})
	if rep.OK || !strings.Contains(strings.Join(rep.Checks[1].Problems, "\n"), "1 snapshot objects are missing") {
		t.Fatalf("missing: %+v", rep.Checks[1])
	}
	if !rep.Checks[0].OK {
		t.Errorf("the ledger check failed too: %+v", rep.Checks[0])
	}
}

// Verify reads every bundle backup in full, and finds one a rescue record
// points at that is gone.
func TestVerifyBundles(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	repo := filepath.Join(root, "acme", "app")
	git(t, ".", "init", "-q", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	bundles := func() VerifyCheck {
		t.Helper()
		rep, err := Verify(ctx, root, g, st, VerifyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Checks[2]
	}
	if c := bundles(); !c.OK || c.Name != CheckBundles || c.Summary != "no bundle backups yet" {
		t.Fatalf("no bundles: %+v", c)
	}

	dest := filepath.Join(root, ".chitragit", "bundles", "acme", "app.bundle")
	if err := guardian.Bundle(ctx, g, repo, dest); err != nil {
		t.Fatal(err)
	}
	if c := bundles(); !c.OK || c.Summary != "1 bundle backups, all readable" {
		t.Fatalf("one bundle: %+v", c)
	}

	// A bundle cut short, as by a full disk.
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, b[:len(b)-20], 0o600); err != nil {
		t.Fatal(err)
	}
	if c := bundles(); c.OK || len(c.Problems) != 1 || !strings.Contains(c.Problems[0], ".chitragit/bundles/acme/app.bundle: does not verify") {
		t.Fatalf("truncated: %+v", c)

	}

	// A rescue whose bundle was deleted.
	repos, _ := st.Repos(ctx)
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO rescues(repo_id, reason, first_failed_at, bundle_path) VALUES (?, 'gone', ?, ?)`,
		repos[0].ID, "2026-01-01T00:00:00Z", filepath.Join(root, "elsewhere.bundle")); err != nil {
		t.Fatal(err)
	}
	c := bundles()
	if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), "acme/app: its rescue bundle") {
		t.Fatalf("missing rescue bundle: %+v", c)
	}
}

// git fsck finds a missing object by connectivity alone; a damaged one
// only with Full, which reads every object.
func TestVerifyFsck(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	repo := filepath.Join(root, "acme", "app")
	git(t, ".", "init", "-q", repo)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "one")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	fsck := func(full bool) VerifyCheck {
		t.Helper()
		rep, err := Verify(ctx, root, g, st, VerifyOptions{Full: full})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Checks[3]
	}
	if c := fsck(true); !c.OK || c.Name != CheckFsck || c.Summary != "1 repos pass git fsck (every object)" {
		t.Fatalf("clean: %+v", c)
	}
	object := func(rev string) string {
		sha := git(t, repo, "rev-parse", rev)
		return filepath.Join(repo, ".git", "objects", sha[:2], sha[2:])
	}

	// A damaged object: still there, so only reading it finds the damage.
	a := object("HEAD:a.txt")
	if err := os.Chmod(a, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("not a git object"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := fsck(false); !c.OK {
		t.Fatalf("connectivity should not read objects: %+v", c)
	}
	if c := fsck(true); c.OK || len(c.Problems) != 1 || !strings.HasPrefix(c.Problems[0], "acme/app: ") {
		t.Fatalf("damaged object, --full: %+v", c)
	}

	// A missing object breaks connectivity.
	if err := os.Remove(object("HEAD:b.txt")); err != nil {
		t.Fatal(err)
	}
	c := fsck(false)
	if c.OK || len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "missing") {
		t.Fatalf("missing object: %+v", c)
	}
}
