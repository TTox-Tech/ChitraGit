package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistory(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "notes")
	git := func(args ...string) {
		t.Helper()
		args = append([]string{"-C", repo, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	git("commit", "-q", "--allow-empty", "-m", "one")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	if code, out, _ := run(t, "--archive", root, "history", "me/notes"); code != 0 || !strings.Contains(out, "Nothing recorded") {
		t.Errorf("empty history = %d %q", code, out)
	}

	run(t, "--archive", root, "snapshot", "take", "me/notes")
	git("commit", "-q", "--allow-empty", "-m", "two")
	run(t, "--archive", root, "snapshot", "take", "me/notes")
	if code, out, _ := run(t, "--archive", root, "history", "me/notes"); code != 0 || strings.Count(out, "(manual)") != 2 {
		t.Errorf("history = %d %q", code, out)
	}
	code, out, errOut := run(t, "--archive", root, "--json", "history", "me/notes", "--limit", "1")
	var got struct {
		Repo    string `json:"repo"`
		History []struct {
			Kind     string `json:"kind"`
			Snapshot int64  `json:"snapshot"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.Repo != "me/notes" ||
		len(got.History) != 1 || got.History[0].Kind != "snapshot" || got.History[0].Snapshot != 2 {
		t.Fatalf("json history = %d %q %q", code, out, errOut)
	}
	if code, _, errOut = run(t, "--archive", root, "history", "nope"); code == 0 || !strings.Contains(errOut, "no repo matches") {
		t.Errorf("unknown repo = %d %q", code, errOut)
	}
}
