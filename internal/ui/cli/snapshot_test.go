package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestSnapshotTakeLsShow(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "notes")
	for _, args := range [][]string{{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "one"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	if code, out, _ := run(t, "--archive", root, "snapshot", "ls"); code != 0 || !strings.Contains(out, "No snapshots yet") {
		t.Errorf("empty ls = %d %q", code, out)
	}

	code, out, errOut := run(t, "--archive", root, "--json", "snapshot", "take", "me/notes")
	var took struct {
		Snapshots []struct {
			ID      int64 `json:"snapshot"`
			Created bool  `json:"created"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(out), &took); err != nil || code != 0 || len(took.Snapshots) != 1 || !took.Snapshots[0].Created {
		t.Fatalf("take = %d %q %q", code, out, errOut)
	}
	id := strconv.FormatInt(took.Snapshots[0].ID, 10)
	if code, out, _ = run(t, "--archive", root, "snapshot", "take", "me/notes"); code != 0 || !strings.Contains(out, "unchanged since snapshot "+id) {
		t.Errorf("second take = %d %q", code, out)
	}
	if code, out, _ = run(t, "--archive", root, "snapshot", "ls"); code != 0 || !strings.Contains(out, "me/notes") || !strings.Contains(out, "manual") {
		t.Errorf("ls = %d %q", code, out)
	}
	code, out, _ = run(t, "--archive", root, "--json", "snapshot", "show", id)
	var show struct {
		Snapshot struct {
			Refs map[string]string `json:"refs"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &show); err != nil || code != 0 || len(show.Snapshot.Refs) != 1 {
		t.Fatalf("show = %d %q", code, out)
	}
	for ref, sha := range show.Snapshot.Refs {
		pin, err := exec.Command("git", "-C", repo, "rev-parse", "refs/chitragit/snap/"+sha).Output()
		if err != nil || strings.TrimSpace(string(pin)) != sha {
			t.Errorf("%s (%s) is not pinned: %v", ref, sha, err)
		}
	}
	if code, _, _ = run(t, "--archive", root, "snapshot", "show", "x"); code != core.ExitUsage {
		t.Errorf("show x exit = %d", code)
	}
	if code, _, _ = run(t, "--archive", root, "snapshot", "show", "999"); code != core.ExitUsage {
		t.Errorf("show 999 exit = %d", code)
	}
}
