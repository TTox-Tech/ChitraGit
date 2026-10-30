package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t",
		"-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestConflictsEndToEnd(t *testing.T) {
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
	os.WriteFile(filepath.Join(dev, "a.txt"), []byte("one\n"), 0o644)
	gitT(t, dev, "add", ".")
	gitT(t, dev, "commit", "-q", "-m", "init")
	gitT(t, dev, "push", "-q", "origin", "main")
	local := filepath.Join(root, "proj")
	gitT(t, ".", "clone", "-q", up, local)

	os.WriteFile(filepath.Join(local, "a.txt"), []byte("mine\n"), 0o644)
	os.WriteFile(filepath.Join(dev, "a.txt"), []byte("theirs\n"), 0o644)
	gitT(t, dev, "commit", "-q", "-am", "upstream")
	gitT(t, dev, "push", "-q", "origin", "main")

	if code, out, errOut := run(t, "--archive", root, "sync"); code != 0 || !strings.Contains(out, "conflict") {
		t.Fatalf("sync = %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ := run(t, "--archive", root, "--json", "conflicts")
	var list struct {
		Conflicts []struct {
			Path  string   `json:"path"`
			Files []string `json:"files"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 0 || len(list.Conflicts) != 1 ||
		list.Conflicts[0].Path != "proj" || strings.Join(list.Conflicts[0].Files, ",") != "a.txt" {
		t.Fatalf("conflicts = %d %v\n%s", code, err, out)
	}

	steps := [][]string{
		{"conflicts", "start", "proj"},
		{"conflicts", "show", "proj", "a.txt"},
		{"conflicts", "resolve", "proj", "a.txt", "both"},
		{"conflicts", "finish", "proj"},
	}
	for _, s := range steps {
		if code, out, errOut := run(t, append([]string{"--archive", root}, s...)...); code != 0 {
			t.Fatalf("%v = %d\n%s\n%s", s, code, out, errOut)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(b) != "theirs\nmine\n" {
		t.Errorf("a.txt = %q", b)
	}
	if _, out, _ := run(t, "--archive", root, "conflicts"); !strings.Contains(out, "No conflicts") {
		t.Errorf("still listed:\n%s", out)
	}
	if _, out, _ := run(t, "--archive", root, "status"); !strings.Contains(out, "ok") {
		t.Errorf("status:\n%s", out)
	}
	if code, _, _ := run(t, "--archive", root, "conflicts", "resolve", "proj", "a.txt", "ours"); code == 0 {
		t.Error("accepted a bad choice")
	}
}
