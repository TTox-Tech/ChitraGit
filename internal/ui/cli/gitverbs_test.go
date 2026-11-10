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

// Every git word maps onto a ChitraGit command (CG-43.5).
func TestGitVerbs(t *testing.T) {
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

	// -C is --archive; pull is sync; fetch is sync --mode fetch.
	for _, verb := range []string{"pull", "fetch"} {
		code, out, errOut := run(t, "-C", root, "--json", verb)
		var rep struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal([]byte(out), &rep)
		if code != 0 && code != core.ExitPartial {
			t.Fatalf("%s = %d %q", verb, code, errOut)
		}
		if want := map[string]string{"pull": "ff", "fetch": "fetch"}[verb]; rep.Mode != want {
			t.Errorf("fetch ran in mode %q", rep.Mode)
		}
	}
	if code, _, errOut := run(t, "-C", root, "fetch", "--dry-run"); code != 0 && code != core.ExitPartial {
		t.Errorf("fetch --dry-run = %d %q", code, errOut)
	}

	// status -s: one plain line per repo, no totals.
	if code, out, _ := run(t, "-C", root, "status", "-s"); code != 0 || strings.TrimSpace(out) != strings.Fields(out)[0]+" me/notes" {
		t.Errorf("status -s = %d %q", code, out)
	}

	// show <number> falls back to a snapshot.
	code, out, _ := run(t, "-C", root, "--json", "snapshot", "take", "me/notes")
	var took struct {
		Snapshots []struct {
			ID int64 `json:"snapshot"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(out), &took); err != nil || code != 0 || len(took.Snapshots) != 1 {
		t.Fatalf("take = %d %q", code, out)
	}
	id := strconv.FormatInt(took.Snapshots[0].ID, 10)
	if code, out, _ := run(t, "-C", root, "show", id); code != 0 || !strings.Contains(out, "refs/heads/") {
		t.Errorf("show %s = %d %q", id, code, out)
	}

	// log <repo> is history <repo>; -n limits it.
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "two").CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	run(t, "-C", root, "snapshot", "take", "me/notes")
	if code, out, _ := run(t, "-C", root, "log", "me/notes"); code != 0 || strings.Count(out, "(manual)") != 2 {
		t.Errorf("log me/notes = %d %q", code, out)
	}
	if code, out, _ := run(t, "-C", root, "--json", "log", "me/notes", "-n", "1"); code != 0 || strings.Count(out, `"snapshot_kind"`) != 1 {
		t.Errorf("log me/notes -n 1 = %d %q", code, out)
	}

	// restore --source is --to.
	if code, _, errOut := run(t, "-C", root, "restore", "me/notes", "--source", id, "--dry-run"); code != 0 {
		t.Errorf("restore --source = %d %q", code, errOut)
	}

	// config <key> [value], --list.
	if code, _, errOut := run(t, "-C", root, "config", "sync.workers", "3"); code != 0 {
		t.Fatalf("config set = %d %q", code, errOut)
	}
	if code, out, _ := run(t, "-C", root, "config", "sync.workers"); code != 0 || strings.TrimSpace(out) != "3" {
		t.Errorf("config get = %d %q", code, out)
	}
	if code, out, _ := run(t, "-C", root, "config", "--list"); code != 0 || !strings.Contains(out, "sync.workers = 3") {
		t.Errorf("config --list = %d %q", code, out)
	}

	// remote lists sources; remote rm of an unknown one fails like unfollow.
	if code, out, _ := run(t, "-C", root, "remote", "-v"); code != 0 || !strings.Contains(out, "Not following") {
		t.Errorf("remote -v = %d %q", code, out)
	}
	if code, _, _ := run(t, "-C", root, "remote", "rm", "github:nobody"); code == 0 {
		t.Error("remote rm of an unknown source succeeded")
	}
}
