package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// codeArchive makes an archive with one repo "proj": two commits, a folder,
// a unicode name, a binary and a link.
func codeArchive(t *testing.T) (root, first string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	root = t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "proj")
	gitT(t, ".", "init", "-q", "-b", "main", repo)
	gitT(t, repo, "config", "core.autocrlf", "false")
	write := func(name, data string) {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n\nfunc main() {}\n")
	write("docs/नमस्ते.md", "# hi\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "first")
	first = strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	write("main.go", "package main\n\n// Entry point.\nfunc main() {}\n")
	write("logo.png", "\x89PNG\x00\x01")
	gitT(t, repo, "add", "-A")
	if err := os.Symlink("main.go", filepath.Join(repo, "link")); err == nil {
		gitT(t, repo, "add", "link")
	}
	gitT(t, repo, "commit", "-q", "-m", "second")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	return root, first
}

func TestLsTree(t *testing.T) {
	root, first := codeArchive(t)
	code, out, errOut := run(t, "--archive", root, "ls-tree", "proj")
	if code != 0 || !strings.Contains(out, "main.go") || !strings.Contains(out, "docs/") || strings.Contains(out, "नमस्ते") {
		t.Fatalf("ls-tree = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ = run(t, "--archive", root, "ls-tree", "-r", "proj"); code != 0 || !strings.Contains(out, "docs/नमस्ते.md") {
		t.Errorf("ls-tree -r = %d\n%s", code, out)
	}
	for _, args := range [][]string{{"proj", "docs/"}, {"proj:docs"}} {
		if code, out, _ = run(t, append([]string{"--archive", root, "ls-tree"}, args...)...); code != 0 || !strings.Contains(out, "docs/नमस्ते.md") || strings.Contains(out, "main.go") {
			t.Errorf("ls-tree %v = %d\n%s", args, code, out)
		}
	}
	// An older commit has no logo; --json gives the entries.
	code, out, _ = run(t, "--archive", root, "--json", "ls-tree", "proj@"+first[:10])
	var got struct {
		Rev     string
		Entries []struct{ Path, Type string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Entries) != 2 || got.Rev != first[:10] {
		t.Errorf("ls-tree --json @first = %d %q", code, out)
	}
	for _, args := range [][]string{{"proj@nope"}, {"proj", "missing.txt"}, {"proj:docs", "docs"}, {"proj", "../x"}, {"proj@"}, {"nope@main"}} {
		if code, _, _ := run(t, append([]string{"--archive", root, "ls-tree"}, args...)...); code == 0 {
			t.Errorf("ls-tree %v succeeded", args)
		}
	}
}
