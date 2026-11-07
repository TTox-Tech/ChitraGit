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

func TestRestore(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "notes")
	git := func(args ...string) string {
		t.Helper()
		args = append([]string{"-C", repo, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	git("commit", "-q", "--allow-empty", "-m", "one")
	one := git("rev-parse", "HEAD")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	code, out, errOut := run(t, "--archive", root, "--json", "snapshot", "take", "me/notes")
	var took struct {
		Snapshots []struct {
			ID int64 `json:"snapshot"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(out), &took); err != nil || code != 0 || len(took.Snapshots) != 1 {
		t.Fatalf("take = %d %q %q", code, out, errOut)
	}
	id := strconv.FormatInt(took.Snapshots[0].ID, 10)
	git("commit", "-q", "--allow-empty", "-m", "two")
	git("tag", "v2")
	two := git("rev-parse", "HEAD")

	if code, out, _ = run(t, "--archive", root, "restore", "me/notes", "--to", id, "--dry-run"); code != 0 ||
		!strings.Contains(out, "refs/heads/main") || !strings.Contains(out, "delete") || git("rev-parse", "HEAD") != two {
		t.Fatalf("dry run = %d %q", code, out)
	}
	if code, _, _ = run(t, "--archive", root, "restore", "me/notes", "--to", id); code != core.ExitUsage || git("rev-parse", "HEAD") != two {
		t.Fatalf("restore without --yes and no terminal = %d", code)
	}
	for _, bad := range [][]string{{"--to", "x"}, {"--to", "999"}} {
		if code, _, _ = run(t, append([]string{"--archive", root, "restore", "me/notes", "--yes"}, bad...)...); code != core.ExitUsage {
			t.Errorf("restore %v exit = %d", bad, code)
		}
	}

	code, out, errOut = run(t, "--archive", root, "--json", "restore", "me/notes", "--to", id, "--yes")
	var got struct {
		Restored bool `json:"restored"`
		Restore  struct {
			Pre   int64 `json:"pre_snapshot"`
			Moves []struct {
				Ref string `json:"ref"`
			} `json:"moves"`
		} `json:"restore"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || !got.Restored || len(got.Restore.Moves) != 2 || got.Restore.Pre == 0 {
		t.Fatalf("restore = %d %q %q", code, out, errOut)
	}
	if h := git("rev-parse", "HEAD"); h != one {
		t.Fatalf("HEAD = %s, want %s", h, one)
	}
	if code, out, _ = run(t, "--archive", root, "history", "me/notes"); code != 0 || !strings.Contains(out, "restored to snapshot "+id) {
		t.Errorf("history after restore = %d %q", code, out)
	}

	// Undo: back to the snapshot taken just before.
	pre := strconv.FormatInt(got.Restore.Pre, 10)
	if code, out, errOut = run(t, "--archive", root, "restore", "me/notes", "--to", pre, "--yes"); code != 0 || !strings.Contains(out, "To undo") {
		t.Fatalf("undo = %d %q %q", code, out, errOut)
	}
	if h := git("rev-parse", "HEAD"); h != two || git("tag", "-l", "v2") != "v2" {
		t.Fatalf("undo left HEAD at %s", h)
	}
	if code, out, _ = run(t, "--archive", root, "restore", "me/notes", "--to", pre); code != 0 || !strings.Contains(out, "already matches") {
		t.Errorf("restore to the current refs = %d %q", code, out)
	}
}
