package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// objectRepo builds a commit straight in the object store, so symlinks and
// gitlinks work the same on every OS: a unicode name, a binary file, a
// symlink, a submodule and a 2 MiB blob.
func objectRepo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = initRepo(t)
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	blob := func(data string) string { return git(data, "hash-object", "-w", "--stdin") }
	head := git("", "rev-parse", "HEAD")
	for _, e := range []struct{ mode, sha, path string }{
		{"100644", blob("hello\n"), "README.md"},
		{"100755", blob("#!/bin/sh\n"), "bin/run.sh"},
		{"100644", blob("नमस्ते\n"), "docs/नमस्ते 👋.md"},
		{"100644", blob("\x00\x01binary"), "img/logo.png"},
		{"120000", blob("../outside"), "link"},
		{"160000", head, "vendor/sub"},
		{"100644", blob(strings.Repeat("x", 2<<20)), "big.txt"},
	} {
		git("", "update-index", "--add", "--cacheinfo", e.mode+","+e.sha+","+e.path)
	}
	tree := git("", "write-tree")
	commit = git("", "-c", "user.email=t@example.com", "-c", "user.name=t", "commit-tree", tree, "-m", "objects")
	return dir, commit
}

func TestTreeAndBlobs(t *testing.T) {
	r := newRunner(t)
	dir, commit := objectRepo(t)
	ctx := context.Background()
	tree, err := r.Tree(ctx, dir, commit, TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]TreeEntry{}
	for _, e := range tree {
		byPath[e.Path] = e
	}
	if len(tree) != 7 {
		t.Fatalf("tree = %+v", tree)
	}
	for p, want := range map[string]struct {
		typ     string
		regular bool
		size    int64
	}{
		"README.md":        {"blob", true, 6},
		"docs/नमस्ते 👋.md": {"blob", true, int64(len("नमस्ते\n"))},
		"link":             {"blob", false, 10},
		"vendor/sub":       {"commit", false, -1},
		"big.txt":          {"blob", true, 2 << 20},
	} {
		e := byPath[p]
		if e.Type != want.typ || e.Regular() != want.regular || e.Size != want.size {
			t.Errorf("%s = %+v", p, e)
		}
	}

	top, err := r.Tree(ctx, dir, commit, TreeOptions{TopLevel: true})
	if err != nil || len(top) != 7 { // README.md big.txt bin docs img link vendor
		t.Errorf("top level = %+v, %v", top, err)
	}
	if _, err := r.Tree(ctx, dir, commit, TreeOptions{MaxEntries: 3}); !errors.Is(err, ErrTreeTooLarge) {
		t.Errorf("entry cap: %v", err)
	}

	var shas []string
	for _, e := range tree {
		shas = append(shas, e.SHA)
	}
	got := map[string]Blob{}
	err = r.Blobs(ctx, dir, shas, 1<<20, func(b Blob) error {
		got[tree[b.Index].Path] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["big.txt"]; ok {
		t.Error("blob over the size cap was read")
	}
	if b := got["docs/नमस्ते 👋.md"]; string(b.Data) != "नमस्ते\n" || b.Binary {
		t.Errorf("unicode file = %+v", b)
	}
	if b := got["img/logo.png"]; !b.Binary {
		t.Errorf("binary not detected: %+v", b)
	}
	if b := got["link"]; string(b.Data) != "../outside" {
		t.Errorf("symlink target = %q", b.Data)
	}
	if !bytes.Equal(got["README.md"].Data, []byte("hello\n")) {
		t.Errorf("README = %q", got["README.md"].Data)
	}
}

func TestBlobsStops(t *testing.T) {
	r := newRunner(t)
	dir, commit := objectRepo(t)
	tree, err := r.Tree(context.Background(), dir, commit, TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var shas []string
	for _, e := range tree {
		if e.Regular() {
			shas = append(shas, e.SHA)
		}
	}
	stop := errors.New("stop")
	calls := 0
	if err := r.Blobs(context.Background(), dir, shas, 0, func(Blob) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Errorf("stop from fn: %v after %d calls", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Blobs(ctx, dir, shas, 0, func(Blob) error { return nil }); err == nil {
		t.Error("cancelled read succeeded")
	}
}

func TestObjectArgsRefused(t *testing.T) {
	r := newRunner(t)
	dir := initRepo(t)
	marker := filepath.Join(t.TempDir(), "out")
	for _, rev := range []string{"", "--output=" + marker, "-h", "HEAD\n--all"} {
		if _, err := r.Tree(context.Background(), dir, rev, TreeOptions{}); err == nil {
			t.Errorf("Tree(%q) accepted", rev)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an option reached git")
	}
	if err := r.Blobs(context.Background(), dir, []string{"--batch-all-objects"}, 0, func(Blob) error { return nil }); err == nil {
		t.Error("Blobs accepted an option")
	}
	for p, want := range map[string]bool{
		"a/b.txt": true, "नमस्ते.md": true, "..a": true,
		"": false, "/etc/passwd": false, "../x": false, "a/../../x": false, "a//b": false, `a\..\..\x`: false, "./a": false,
	} {
		if safeTreePath(p) != want {
			t.Errorf("safeTreePath(%q) = %v", p, !want)
		}
	}
}
