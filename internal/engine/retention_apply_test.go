package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/scan"
)

// Retention keeps only what the policy asks for, except a snapshot whose
// commit or tag nothing else reaches: that one stays, pinned.
func TestRetentionNeverDropsTheOnlyCopy(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	app := filepath.Join(root, "acme", "app")
	git(t, ".", "init", "-q", "-b", "main", app)
	commit := func(msg string) string {
		if err := os.WriteFile(filepath.Join(app, "a.txt"), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, app, "add", "-A")
		git(t, app, "commit", "-q", "-m", msg)
		return git(t, app, "rev-parse", "HEAD")
	}
	snap := func() {
		t.Helper()
		repos, _ := st.Repos(ctx)
		if taken := TakeSnapshots(ctx, root, repos, g, st); len(taken) != 1 || taken[0].Error != "" || !taken[0].Created {
			t.Fatalf("snapshot: %+v", taken)
		}
	}
	a := commit("a")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	snap()
	b := commit("b")
	git(t, app, "tag", "-a", "-m", "release", "v1")
	tagObj := git(t, app, "rev-parse", "v1")
	snap()
	c := commit("c")
	snap()
	// Rewrite history: c and the tag are now only in snapshots.
	git(t, app, "reset", "-q", "--hard", a)
	git(t, app, "tag", "-d", "v1")
	d := commit("d")
	snap()

	pins := func() string {
		return git(t, app, "for-each-ref", "--format=%(objectname)", snapRefPrefix)
	}
	before, pinsBefore := hashTree(t, root), pins()
	plan, err := Retention(ctx, root, g, st, RetentionPolicy{KeepLast: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if hashTree(t, root) != before || pins() != pinsBefore {
		t.Fatal("a dry run changed the archive")
	}
	// Snapshots 1..4: a, b+tag, c, d. Policy keeps 4. Snapshot 3 holds c,
	// which only it reaches, and snapshot 2 holds the tag object.
	want := map[int64]string{2: KeepOnlyCopy, 3: KeepOnlyCopy, 4: KeepLast}
	for _, it := range plan.Items {
		if it.Keep != (want[it.Snapshot] != "") || it.Reason != want[it.Snapshot] {
			t.Errorf("snapshot %d: keep=%v reason=%q, want %q", it.Snapshot, it.Keep, it.Reason, want[it.Snapshot])
		}
	}
	if plan.Kept != 3 || plan.Removed != 1 || plan.Applied {
		t.Fatalf("plan = %+v", plan)
	}

	rep, err := Retention(ctx, root, g, st, RetentionPolicy{KeepLast: 1}, true)
	if err != nil || rep.Removed != 1 || len(rep.Errors) > 0 {
		t.Fatalf("apply = %+v, %v", rep, err)
	}
	left := pins()
	for _, sha := range []string{b, c, d, tagObj} {
		if !strings.Contains(left, sha) {
			t.Errorf("%s was unpinned but a kept snapshot needs it", sha)
		}
	}
	if strings.Contains(left, a) {
		t.Errorf("a is still pinned; main reaches it, so retention should unpin it")
	}
	if rep.Unpinned != 1 {
		t.Errorf("unpinned = %d, want 1", rep.Unpinned)
	}
	if v, err := Verify(ctx, root, g, st, VerifyOptions{}); err != nil || !v.OK {
		t.Fatalf("verify after retention: %+v %v", v, err)
	}

	// A pin left by an interrupted sync, on a commit no ref or snapshot
	// reaches, is not ours to drop.
	git(t, app, "switch", "-q", "-c", "gone")
	e := commit("e")
	git(t, app, "update-ref", snapRefPrefix+e, e)
	git(t, app, "switch", "-q", "main")
	git(t, app, "branch", "-q", "-D", "gone")

	// Run again: nothing more to do.
	again, err := Retention(ctx, root, g, st, RetentionPolicy{KeepLast: 1}, true)
	if err != nil || again.Removed != 0 || again.Unpinned != 0 {
		t.Fatalf("second run = %+v, %v", again, err)
	}
	if !strings.Contains(pins(), e) {
		t.Error("retention unpinned a commit that only its pin keeps")
	}
}
