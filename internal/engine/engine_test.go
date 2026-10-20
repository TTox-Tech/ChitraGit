package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setup(t *testing.T) (root string, g *gitx.Runner, st *store.Store) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root = t.TempDir()
	g, err := gitx.New(gitx.Options{HooksDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return root, g, st
}

func TestScanKeepsIdentityAcrossMovesAndCopies(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	a := filepath.Join(root, "owner", "a")
	git(t, ".", "init", "-q", a)
	git(t, a, "remote", "add", "origin", "git@github.com:owner/a.git")

	rep, err := Scan(ctx, root, scan.Options{}, g, st)
	if err != nil || rep.Added != 1 {
		t.Fatalf("first scan: %+v %v", rep, err)
	}
	id := git(t, a, "config", "chitragit.id")
	repos, _ := st.Repos(ctx)
	if repos[0].ID != id || repos[0].Owner != "owner" || repos[0].Provider != "github" {
		t.Fatalf("stored repo = %+v, id in repo = %s", repos[0], id)
	}

	// Move: same ID, new path, no new repo.
	moved := filepath.Join(root, "renamed")
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}
	if rep, _ = Scan(ctx, root, scan.Options{}, g, st); rep.Moved != 1 || rep.Added != 0 {
		t.Fatalf("after move: %+v", rep)
	}

	// Copy: the copy carries the same ID in its config and must get its own.
	cp := filepath.Join(root, "copy")
	if out, err := exec.Command("cp", "-r", moved, cp).CombinedOutput(); err != nil {
		t.Skipf("cp: %v %s", err, out)
	}
	if rep, _ = Scan(ctx, root, scan.Options{}, g, st); rep.Added != 1 {
		t.Fatalf("after copy: %+v", rep)
	}
	if git(t, cp, "config", "chitragit.id") == id {
		t.Error("the copy kept the original's ID")
	}

	// Delete: marked missing, never removed.
	if err := os.RemoveAll(cp); err != nil {
		t.Fatal(err)
	}
	if rep, _ = Scan(ctx, root, scan.Options{}, g, st); rep.Missing != 1 {
		t.Fatalf("after delete: %+v", rep)
	}
	if repos, _ = st.Repos(ctx); len(repos) != 2 {
		t.Errorf("a deleted repo was dropped from the database: %d repos", len(repos))
	}
}

func TestSyncRecordsResults(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	up := filepath.Join(t.TempDir(), "up.git")
	git(t, ".", "init", "-q", "--bare", up)
	dev := filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", up, dev)
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dev, "push", "-q", "origin", "HEAD")
	git(t, ".", "clone", "-q", up, filepath.Join(root, "r"))
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dev, "push", "-q", "origin", "HEAD")

	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	rep, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff"}}, g, st)
	if err != nil || rep.Counts[syncer.Updated] != 1 {
		t.Fatalf("sync: %+v %v", rep, err)
	}
	repos, _ := st.Repos(ctx)
	if repos[0].Status != syncer.StatusOK || repos[0].LastSyncedAt == "" {
		t.Errorf("repo after sync = %+v", repos[0])
	}
	var changes int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM ref_changes WHERE run_id = ?`, rep.RunID).Scan(&changes); err != nil || changes == 0 {
		t.Errorf("no ref changes recorded: %d %v", changes, err)
	}
	if _, err := Sync(ctx, SyncRequest{Root: root, Only: "["}, g, st); err == nil {
		t.Error("bad --only pattern accepted")
	}
}
