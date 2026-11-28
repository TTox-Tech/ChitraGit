package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndo(t *testing.T) {
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
	before := gitT(t, local, "rev-parse", "HEAD")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	if code, _, errOut := run(t, "--archive", root, "undo"); code == 0 || !strings.Contains(errOut, "no sync has run yet") {
		t.Errorf("undo before any sync = %d %q", code, errOut)
	}
	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	gitT(t, dev, "push", "-q", "origin", "main")
	if code, _, errOut := run(t, "--archive", root, "sync"); code != 0 {
		t.Fatalf("sync: %s", errOut)
	}
	after := gitT(t, local, "rev-parse", "HEAD")

	if code, out, _ := run(t, "--archive", root, "undo", "--dry-run"); code != 0 || !strings.Contains(out, "refs/heads/main") ||
		gitT(t, local, "rev-parse", "HEAD") != after {
		t.Errorf("undo --dry-run = %d %q", code, out)
	}
	if code, _, errOut := run(t, "--archive", root, "undo"); code == 0 || !strings.Contains(errOut, "--yes") {
		t.Errorf("undo without a terminal or --yes = %d %q", code, errOut)
	}
	code, out, errOut := run(t, "--archive", root, "undo", "--yes")
	if code != 0 || !strings.Contains(out, "undid the sync in proj (2 refs)") || !strings.Contains(out, "restore proj --to") {
		t.Fatalf("undo = %d\n%s\n%s", code, out, errOut)
	}
	if got := gitT(t, local, "rev-parse", "HEAD"); got != before {
		t.Errorf("after undo HEAD = %s, want %s", got, before)
	}

	// Again: both refs have moved since the run, so nothing moves.
	code, out, _ = run(t, "--archive", root, "--json", "undo", "--yes")
	var got struct {
		Undo struct {
			Repos []struct {
				Kept []string `json:"kept"`
			} `json:"repos"`
		} `json:"undo"`
		Undone bool `json:"undone"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.Undone || len(got.Undo.Repos) != 1 ||
		len(got.Undo.Repos[0].Kept) != 2 {
		t.Errorf("second undo = %d %q", code, out)
	}
}
