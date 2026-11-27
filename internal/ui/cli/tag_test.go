package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	for _, name := range []string{"me/a", "me/b", "work/c"} {
		dir := filepath.Join(root, name)
		gitT(t, ".", "init", "-q", dir)
		gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	if code, out, _ := run(t, "--archive", root, "tag"); code != 0 || !strings.Contains(out, "No named snapshots") {
		t.Errorf("tag with no names = %d %q", code, out)
	}
	if code, out, errOut := run(t, "--archive", root, "tag", "pre-move"); code != 0 || !strings.Contains(out, "3 repo(s)") {
		t.Fatalf("tag pre-move = %d %q %q", code, out, errOut)
	}
	if code, out, errOut := run(t, "--archive", root, "tag", "work-only", "--only", "work/*"); code != 0 || !strings.Contains(out, "1 repo(s)") {
		t.Errorf("tag --only = %d %q %q", code, out, errOut)
	}
	// The same name twice is refused per repo.
	if code, _, errOut := run(t, "--archive", root, "tag", "pre-move", "me/a"); code != core.ExitPartial || !strings.Contains(errOut, "already used") {
		t.Errorf("reused name = %d %q", code, errOut)
	}
	code, out, _ := run(t, "--archive", root, "--json", "tag", "-l")
	var got struct {
		Names []struct {
			Name  string `json:"name"`
			Repos int    `json:"repos"`
		} `json:"names"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Names) != 2 {
		t.Fatalf("tag -l = %d %q", code, out)
	}
	for _, n := range got.Names {
		if want := map[string]int{"pre-move": 3, "work-only": 1}[n.Name]; n.Repos != want {
			t.Errorf("%s: %d repos, want %d", n.Name, n.Repos, want)
		}
	}
	if code, out, _ = run(t, "--archive", root, "snapshot", "ls"); code != 0 || strings.Count(out, "manual") != 4 {
		t.Errorf("snapshots after naming = %q", out)
	}

	if code, out, _ = run(t, "--archive", root, "tag", "-d", "pre-move"); code != 0 || !strings.Contains(out, "3 snapshot(s)") {
		t.Errorf("tag -d = %d %q", code, out)
	}
	if code, _, _ = run(t, "--archive", root, "tag", "-d", "pre-move"); code == 0 {
		t.Error("deleting a missing name succeeded")
	}
	for _, args := range [][]string{{"42"}, {"a b"}, {"-d"}, {"-l", "x"}, {"add", "work"}} {
		if code, _, _ = run(t, append([]string{"--archive", root, "tag"}, args...)...); code == 0 {
			t.Errorf("tag %v: no error", args)
		}
	}
}

func TestSyncTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	up, dev := filepath.Join(t.TempDir(), "up.git"), filepath.Join(t.TempDir(), "dev")
	gitT(t, ".", "init", "-q", "--bare", up)
	gitT(t, ".", "clone", "-q", up, dev)
	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, dev, "push", "-q", "origin", "main")
	local := filepath.Join(root, "proj")
	gitT(t, ".", "clone", "-q", up, local)
	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	gitT(t, dev, "push", "-q", "origin", "main")

	if code, _, errOut := run(t, "--archive", root, "sync", "--tag", "12"); code == 0 ||
		!strings.Contains(errOut, "cannot name a snapshot") {
		t.Fatalf("sync --tag 12 = %d %q", code, errOut)
	}
	if got := gitT(t, local, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("a refused --tag still synced: %s commits", got)
	}
	code, out, errOut := run(t, "--archive", root, "--json", "sync", "--tag", "after-two")
	var got struct {
		Named []struct {
			Path     string `json:"path"`
			Snapshot int64  `json:"snapshot"`
		} `json:"named"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Named) != 1 || got.Named[0].Path != "proj" {
		t.Fatalf("sync --tag = %d %q %q", code, out, errOut)
	}
	// The name marks the state after the sync: the post snapshot itself.
	if code, out, _ = run(t, "--archive", root, "snapshot", "ls"); code != 0 || strings.Count(out, "\n") != 2 {
		t.Errorf("snapshots = %q", out)
	}
	if code, out, _ = run(t, "--archive", root, "tag", "-l"); code != 0 || !strings.Contains(out, "after-two") {
		t.Errorf("tag -l = %d %q", code, out)
	}
}
