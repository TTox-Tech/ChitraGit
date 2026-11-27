package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// historyRepo makes a small history with the cases a diff reader trips on:
// a unicode name, a binary, a rename, a deletion, a merge and a root
// commit. It returns the repo and the commits by message.
func historyRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t",
			"-c", "core.autocrlf=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, data string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commits := map[string]string{}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		commits[msg] = git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	write("README.md", "one\ntwo\nthree\n")
	write("old name.txt", strings.Repeat("same line\n", 20))
	write("gone.txt", "bye\n")
	commit("root")
	write("README.md", "one\n2\nthree\nfour\n")
	write("docs/नमस्ते 👋.md", "नमस्ते\n")
	write("logo.png", "\x00\x01\x02binary")
	git("mv", "old name.txt", "new name.txt")
	git("rm", "-q", "gone.txt")
	commit("change")
	git("switch", "-q", "-c", "side", commits["root"])
	write("side.txt", "side\n")
	commit("side")
	git("switch", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "merge", "side")
	commits["merge"] = git("rev-parse", "HEAD")
	return dir, commits
}

func TestCommitDiff(t *testing.T) {
	r := newRunner(t)
	dir, c := historyRepo(t)
	ctx := context.Background()

	d, err := r.CommitDiff(ctx, dir, c["change"], DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.SHA != c["change"] || len(d.Parents) != 1 || d.Parents[0] != c["root"] || d.Message != "change" || d.Author != "t" {
		t.Errorf("header = %+v", d.Commit)
	}
	byPath := map[string]FileChange{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	want := map[string]FileChange{
		"README.md":        {Status: "M", Added: 2, Deleted: 1},
		"docs/नमस्ते 👋.md": {Status: "A", Added: 1},
		"logo.png":         {Status: "A", Binary: true},
		"new name.txt":     {Status: "R", OldPath: "old name.txt"},
		"gone.txt":         {Status: "D", Deleted: 1},
	}
	if len(d.Files) != len(want) {
		t.Fatalf("files = %+v", d.Files)
	}
	for p, w := range want {
		f := byPath[p]
		if f.Status != w.Status || f.OldPath != w.OldPath || f.Added != w.Added || f.Deleted != w.Deleted || f.Binary != w.Binary {
			t.Errorf("%s = %+v, want %+v", p, f, w)
		}
		if !strings.HasPrefix(f.Patch, "diff --git ") {
			t.Errorf("%s: patch %q", p, f.Patch)
		}
	}
	if !strings.Contains(byPath["README.md"].Patch, "+four") || strings.Contains(byPath["README.md"].Patch, "नमस्ते") {
		t.Errorf("patches are not matched to their files:\n%s", byPath["README.md"].Patch)
	}

	// A root commit diffs against nothing; a merge against its first parent.
	if root, err := r.CommitDiff(ctx, dir, c["root"], DiffOptions{NoPatch: true}); err != nil || len(root.Files) != 3 ||
		root.Files[0].Patch != "" || len(root.Parents) != 0 {
		t.Errorf("root = %+v %v", root, err)
	}
	if m, err := r.CommitDiff(ctx, dir, c["merge"], DiffOptions{}); err != nil || len(m.Parents) != 2 || len(m.Files) != 1 ||
		m.Files[0].Path != "side.txt" {
		t.Errorf("merge = %+v %v", m, err)
	}

	// The cap cuts the patches, never the file list.
	small, err := r.CommitDiff(ctx, dir, c["change"], DiffOptions{MaxBytes: 200})
	if err != nil || !small.Truncated || len(small.Files) != len(want) {
		t.Fatalf("capped = %+v %v", small, err)
	}
	total := 0
	for _, f := range small.Files {
		total += len(f.Patch)
	}
	if total != 200 {
		t.Errorf("capped patches hold %d bytes, want 200", total)
	}

	for _, bad := range []string{"--output=/tmp/x", "", "nope"} {
		if _, err := r.CommitDiff(ctx, dir, bad, DiffOptions{}); err == nil {
			t.Errorf("rev %q accepted", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	r := newRunner(t)
	dir, c := historyRepo(t)
	ctx := context.Background()
	ahead, behind, base, err := r.Compare(ctx, dir, c["side"], c["change"])
	if err != nil || ahead != 1 || behind != 1 || base != c["root"] {
		t.Errorf("compare = %d %d %s %v", ahead, behind, base, err)
	}
	if ahead, behind, _, err = r.Compare(ctx, dir, c["root"], "main"); err != nil || ahead != 3 || behind != 0 {
		t.Errorf("compare root..main = %d %d %v", ahead, behind, err)
	}
	if _, _, _, err := r.Compare(ctx, dir, "-x", "main"); err == nil {
		t.Error("an option-like rev was accepted")
	}
}
