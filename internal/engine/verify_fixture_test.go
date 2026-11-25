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

	"github.com/TTox-Tech/chitragit/internal/guardian"
	"github.com/TTox-Tech/chitragit/internal/scan"
)

// One archive, damaged in every way verify knows about at once. Each check
// must name its own problem, and verify must not change a single file.
func TestVerifyCorruptArchive(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)

	// Two repos, so the damage in one never hides the other.
	app := filepath.Join(root, "acme", "app")
	lib := filepath.Join(root, "acme", "lib")
	for _, repo := range []string{app, lib} {
		git(t, ".", "init", "-q", repo)
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte(repo+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, repo, "add", "-A")
		git(t, repo, "commit", "-q", "-m", "one")
	}
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.Repos(ctx)
	if taken := TakeSnapshots(ctx, root, repos, g, st); len(taken) != 2 {
		t.Fatalf("snapshots: %+v", taken)
	}
	bundle := filepath.Join(root, ".chitragit", "bundles", "acme", "lib.bundle")
	if err := guardian.Bundle(ctx, g, lib, bundle); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(ctx, root, g, st, VerifyOptions{Full: true})
	if err != nil || !rep.OK {
		t.Fatalf("the archive should start whole: %+v %v", rep, err)
	}

	// The ledger: an event edited after it was written.
	if _, err := st.DB.ExecContext(ctx, `UPDATE events SET payload = '{"message":"forged"}' WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	// Pinned: app's snapshot pin deleted by hand.
	head := git(t, app, "rev-parse", "HEAD")
	git(t, app, "update-ref", "-d", snapRefPrefix+head)
	// Bundles: lib's backup cut short.
	b, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, b[:len(b)-20], 0o600); err != nil {
		t.Fatal(err)
	}
	// fsck: lib's file content overwritten with junk.
	blob := git(t, lib, "rev-parse", "HEAD:a.txt")
	obj := filepath.Join(lib, ".git", "objects", blob[:2], blob[2:])
	if err := os.Chmod(obj, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obj, []byte("not a git object"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := hashTree(t, root)
	rep, err = Verify(ctx, root, g, st, VerifyOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verify passed a damaged archive")
	}
	want := map[string]string{
		CheckLedger:  "broken at event 1",
		CheckPinned:  "not pinned",
		CheckBundles: "acme/lib.bundle: does not verify",
		CheckFsck:    "acme/lib: ",
	}
	for _, c := range rep.Checks {
		problems := strings.Join(c.Problems, "\n")
		if c.Name == CheckLedger {
			problems += c.Summary
		}
		if c.OK || !strings.Contains(problems, want[c.Name]) {
			t.Errorf("%s: want a problem with %q, got %+v", c.Name, want[c.Name], c)
		}
		delete(want, c.Name)
	}
	if len(want) > 0 {
		t.Errorf("checks that never ran: %v", want)
	}
	if after := hashTree(t, root); after != before {
		t.Error("verify changed files in the archive")
	}
}

// hashTree sums every file under root, path and content, so any change shows.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
