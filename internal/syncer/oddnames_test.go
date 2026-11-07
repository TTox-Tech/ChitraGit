package syncer

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// oddNames are file names that trip up tools: non-ASCII (composed and
// decomposed, which macOS treats as one name), emoji, spaces, quotes and
// a leading dash, plus a path long enough to pass Windows' old 260
// character limit inside a temp folder.
var oddNames = []string{
	"café.txt",            // é composed (NFC)
	"naïve.txt",          // ï decomposed (NFD)
	"日本語のメモ.md",           // CJK
	"हिंदी नोट्स.txt",     // Devanagari with combining marks
	"party 🎉 notes.txt",   // emoji and spaces
	"it's \"quoted\".txt", // quotes
	"-dash first.txt",     // looks like a flag
	filepath.Join(strings.Repeat("deep-folder-name/", 12), "long file name that keeps going.txt"),
}

func writeAll(t *testing.T, dir, suffix string) {
	t.Helper()
	for _, name := range oddNames {
		if strings.Contains(name, "\"") && runtime.GOOS == "windows" {
			continue // not a valid Windows file name
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Dir(path), filepath.Base(path), name+suffix)
	}
}

// Sync and the stash flow work with odd file names: upstream changes
// them while the local copy has uncommitted edits to them and new
// untracked odd files; the update applies and every local change stays.
func TestOddFileNames(t *testing.T) {
	f := newFixture(t)
	writeAll(t, f.dev, " v1")
	git(t, f.dev, "add", ".")
	git(t, f.dev, "commit", "-q", "-m", "odd names")
	f.push("main")
	local := f.clone(t)

	f.commit("a.txt", "upstream edit")
	f.push("main")
	local1 := filepath.Join(local, oddNames[0])
	write(t, local, oddNames[0], "my edit") // uncommitted edit to an odd file
	untracked := filepath.Join(local, "nouveau-é", "ünïcödé untracked.txt")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Dir(untracked), filepath.Base(untracked), "mine")

	r := syncDir(t, local, Options{Mode: "ff", StashLocalChanges: true})
	if r.Outcome != Updated || git(t, local, "rev-parse", "HEAD") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Fatalf("sync = %s %s: %s", r.Outcome, r.Status, detail(r.Err))
	}
	for path, want := range map[string]string{local1: "my edit", untracked: "mine"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	if st := git(t, local, "stash", "list"); st != "" {
		t.Errorf("stash left behind: %s", st)
	}
}

// Two upstream files whose names differ only in case collide on a
// case-insensitive disk (macOS and Windows by default). The sync must
// still fetch everything and keep local work, whatever it decides about
// the working tree.
func TestCaseClash(t *testing.T) {
	f := newFixture(t)
	write(t, f.dev, "README.md", "upper")
	git(t, f.dev, "add", ".")
	git(t, f.dev, "commit", "-q", "-m", "upper")
	// Add the lower-case twin straight to the index so this works on a
	// case-insensitive disk too.
	sha := gitIn(t, f.dev, "lower\n", "hash-object", "-w", "--stdin")
	git(t, f.dev, "update-index", "--add", "--cacheinfo", "100644,"+sha+",readme.md")
	git(t, f.dev, "commit", "-q", "-m", "lower twin")
	f.push("main")
	local := f.clone(t)

	f.commit("a.txt", "upstream edit")
	f.push("main")
	write(t, local, "mine.txt", "mine")
	r := syncDir(t, local, Options{Mode: "ff", StashLocalChanges: true})
	if r.Outcome == Failed {
		t.Fatalf("sync failed: %s %s", r.Status, detail(r.Err))
	}
	if got, want := git(t, local, "rev-parse", "refs/remotes/origin/main"), git(t, f.dev, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin/main = %s, want %s", got, want)
	}
	if got, err := os.ReadFile(filepath.Join(local, "mine.txt")); err != nil || string(got) != "mine" {
		t.Fatalf("local file = %q, %v", got, err)
	}
}

// gitIn runs plain git with stdin and returns its trimmed output.
func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
