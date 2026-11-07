package chaos

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiskFull fills the disk an archive lives on, has upstream grow past
// the space left, and syncs. The sync must fail with a "disk is full"
// error and leave the repo and the database intact; once space is freed,
// the next sync must bring everything in.
//
// It needs a small, otherwise empty file system of at least 32 MB (the
// grown upstream must fit once the filler is gone): CI mounts a tmpfs and
// sets CHITRAGIT_SMALL_DISK to it. Without it the test is skipped.
func TestDiskFull(t *testing.T) {
	small := os.Getenv("CHITRAGIT_SMALL_DISK")
	if small == "" {
		t.Skip("set CHITRAGIT_SMALL_DISK to an empty small file system (at least 32 MB, e.g. a tmpfs)")
	}
	cli := newCLI(t)
	root, err := os.MkdirTemp(small, "archive")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	cli.mustRun("init", root)
	u := newUpstream(t, "big")
	repo := filepath.Join(root, "me", "big")
	git(t, ".", "clone", "-q", u.bare, repo)
	cli.mustRun("--archive", root, "scan")
	cli.mustRun("--archive", root, "sync") // the database and snapshots exist before the disk fills
	before := git(t, repo, "rev-parse", "refs/remotes/origin/main")

	// Upstream grows by more than the disk holds; random data does not
	// compress.
	for i := range 4 {
		blob := make([]byte, 3<<20)
		_, _ = rand.Read(blob)
		name := filepath.Join(u.dev, "blob"+string(rune('a'+i)))
		if err := os.WriteFile(name, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, u.dev, "add", ".")
		git(t, u.dev, "commit", "-q", "-m", "blob")
	}
	u.push("main")
	filler := fill(t, small)

	code, out := cli.run("--archive", root, "sync")
	if code == 0 || !strings.Contains(out, "disk is full") {
		t.Fatalf("sync on a full disk = exit %d:\n%s", code, out)
	}
	if got := git(t, repo, "rev-parse", "refs/remotes/origin/main"); got != before {
		t.Fatalf("a failed fetch moved origin/main to %s", got)
	}
	git(t, repo, "fsck", "--no-progress")

	os.Remove(filler)
	cli.mustRun("--archive", root, "sync")
	if got, want := git(t, repo, "rev-parse", "refs/remotes/origin/main"), git(t, u.dev, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after freeing space origin/main = %s, want %s", got, want)
	}
	git(t, repo, "fsck", "--no-progress")
	cli.mustRun("--archive", root, "log", "--verify", "--limit", "1")
}

// fill writes a file into dir until the file system has about 1 MB left,
// and returns its path.
func fill(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "filler")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	chunk := make([]byte, 256<<10)
	for {
		if _, err := f.Write(chunk); err != nil {
			break
		}
	}
	// Leave room for small writes (the database journal, a lock file),
	// not for the fetch.
	if fi, err := f.Stat(); err == nil {
		_ = f.Truncate(max(fi.Size()-1<<20, 0))
	}
	return path
}
