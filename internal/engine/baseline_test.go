package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// pushChange commits one more file to the bare repo up.
func pushChange(t *testing.T, up, file, data string) string {
	t.Helper()
	dev := filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", up, dev)
	if err := os.WriteFile(filepath.Join(dev, file), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dev, "add", "-A")
	git(t, dev, "commit", "-q", "-m", "more")
	git(t, dev, "push", "-q", "origin", "HEAD")
	return git(t, dev, "rev-parse", "HEAD")
}

// fileHashes maps every file under dir, .git included, to its content hash.
func fileHashes(t *testing.T, dir string, skipGit bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipGit && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The ticket's bar: an archive made before Shield, with one hostile repo
// already on disk, is upgraded. Its first sync judges every repo: the
// hostile one is marked blocked and held (its update is downloaded, but no
// branch moves and no file changes), every other repo syncs, and nothing
// is deleted. Later syncs keep holding it without logging it again.
func TestBaselineAfterUpgrade(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g, err := gitx.New(gitx.Options{HooksDir: t.TempDir()})
	if err != nil {
		t.Skip("git not available:", err)
	}
	dbPath := filepath.Join(t.TempDir(), "db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	ups := map[string]string{
		"acme/one":  shieldUpstream(t, map[string]string{"README.md": "# one\n"}),
		"acme/two":  shieldUpstream(t, map[string]string{"main.go": "package main\n"}),
		"acme/evil": shieldUpstream(t, hostileFixtures["folder-open task"]),
	}
	for p, up := range ups {
		git(t, ".", "clone", "-q", up, filepath.Join(root, filepath.FromSlash(p)))
	}
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}

	// Make this the database of an archive from before Shield: schema v6,
	// so opening it again runs the upgrade.
	for _, q := range []string{`DROP TABLE shield_verdicts`, `DELETE FROM schema_migrations WHERE version >= 7`} {
		if _, err := st.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if st, err = store.Open(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	news := map[string]string{}
	for p, up := range ups {
		news[p] = pushChange(t, up, "CHANGES.md", "more\n")
	}
	evil := filepath.Join(root, "acme", "evil")
	evilHead := git(t, evil, "rev-parse", "HEAD")
	evilFiles := fileHashes(t, evil, true)

	pack, err := audit.BuiltinRules()
	if err != nil {
		t.Fatal(err)
	}
	shield := &ShieldOptions{Pack: pack, BlockAt: audit.SevHigh, Limits: DefaultLimits()}
	req := SyncRequest{Root: root, Baseline: shield, Options: syncer.Options{Mode: "ff", RetryDelay: time.Millisecond,
		Shield: SyncShield(g, shield)}}
	rep, err := Sync(ctx, req, g, st)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Baseline == nil || rep.Baseline.Judged != 3 || len(rep.Baseline.Blocked) != 1 || rep.Baseline.Blocked[0] != "acme/evil" {
		t.Fatalf("baseline: %+v", rep.Baseline)
	}
	for _, r := range rep.Results {
		dir := filepath.Join(root, filepath.FromSlash(r.Repo.Path))
		switch r.Repo.Path {
		case "acme/evil":
			if r.Status != syncer.StatusBlocked || r.Category != string(core.CatBlocked) || !strings.Contains(r.Reason, "already on disk") {
				t.Errorf("evil: %s/%s/%s: %s", r.Outcome, r.Status, r.Category, r.Reason)
			}
			if git(t, dir, "rev-parse", "HEAD") != evilHead || git(t, dir, "rev-parse", "origin/HEAD") != news[r.Repo.Path] {
				t.Error("evil: HEAD moved, or its update was not downloaded")
			}
		default:
			if r.Outcome != syncer.Updated || git(t, dir, "rev-parse", "HEAD") != news[r.Repo.Path] {
				t.Errorf("%s: %s/%s: %s", r.Repo.Path, r.Outcome, r.Status, r.Reason)
			}
		}
	}
	if got := fileHashes(t, evil, true); len(got) != len(evilFiles) {
		t.Errorf("files of the held repo changed: %v -> %v", evilFiles, got)
	} else {
		for p, h := range evilFiles {
			if got[p] != h {
				t.Errorf("%s changed in the held repo", p)
			}
		}
	}

	countEvents := func() int {
		entries, err := st.Ledger(ctx, store.LedgerFilter{})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range entries {
			if e.Type == EventShieldBaseline || e.Type == EventShieldBlocked {
				n++
			}
		}
		return n
	}
	if n := countEvents(); n != 1 {
		t.Errorf("%d Shield events in the ledger, want 1", n)
	}

	// A second sync judges nothing new, keeps holding, and logs nothing more.
	pushChange(t, ups["acme/evil"], "MORE.md", "again\n")
	rep, err = Sync(ctx, req, g, st)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Baseline.Judged != 0 {
		t.Errorf("second baseline judged %d repos", rep.Baseline.Judged)
	}
	for _, r := range rep.Results {
		if r.Repo.Path == "acme/evil" && (r.Status != syncer.StatusBlocked || git(t, evil, "rev-parse", "HEAD") != evilHead) {
			t.Errorf("evil not held on the second sync: %s %s", r.Status, r.Reason)
		}
	}
	if n := countEvents(); n != 1 {
		t.Errorf("%d Shield events after the second sync, want 1", n)
	}
	repos, _ := st.Repos(ctx)
	for _, r := range repos {
		want := syncer.StatusOK
		if r.Path == "acme/evil" {
			want = syncer.StatusBlocked
		}
		if r.Status != want {
			t.Errorf("%s stored as %s", r.Path, r.Status)
		}
	}

	// Removing what was found lets it go: the new HEAD is judged again.
	if err := os.RemoveAll(filepath.Join(evil, ".vscode")); err != nil {
		t.Fatal(err)
	}
	git(t, evil, "rm", "-q", "-r", "--cached", ".vscode")
	git(t, evil, "commit", "-q", "-m", "remove the task")
	brep, err := Baseline(ctx, root, repos, shield, g, st)
	if err != nil || brep.Judged != 1 || len(brep.Blocked) != 0 {
		t.Fatalf("baseline after the fix: %+v %v", brep, err)
	}
	if holds, _ := Holds(ctx, st); len(holds) != 0 {
		t.Errorf("still held: %v", holds)
	}
}

// A bare mirror has no files to hold back: a hostile one is reported, not held.
func TestBaselineDoesNotHoldBareRepos(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	up := shieldUpstream(t, hostileFixtures["folder-open task"])
	git(t, ".", "clone", "-q", "--mirror", up, filepath.Join(root, "acme", "evil.git"))
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.Repos(ctx)
	pack, _ := audit.BuiltinRules()
	rep, err := Baseline(ctx, root, repos, &ShieldOptions{Pack: pack, BlockAt: audit.SevHigh}, g, st)
	if err != nil || rep.Judged != 1 || len(rep.Blocked) != 0 {
		t.Fatalf("baseline: %+v %v", rep, err)
	}
	v, err := st.ShieldVerdictOf(ctx, repos[0].ID)
	if err != nil || v == nil || v.Verdict != string(audit.VerdictWarn) || v.Findings == 0 {
		t.Fatalf("verdict: %+v %v", v, err)
	}
}
