package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestAuthoringVerbsPointToGit(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "notes")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}

	code, _, errOut := run(t, "--archive", root, "commit", "-am", "msg", "me/notes")
	if code != core.ExitUsage || !strings.Contains(errOut, "does not write commits") || !strings.Contains(errOut, "git -C ") ||
		!strings.Contains(errOut, filepath.Join("me", "notes")+`" commit`) {
		t.Errorf("commit with a repo = %d %q", code, errOut)
	}
	code, _, errOut = run(t, "--archive", root, "push")
	if code != core.ExitUsage || !strings.Contains(errOut, "git -C <repo folder> push") {
		t.Errorf("push without a repo = %d %q", code, errOut)
	}
	// Outside any archive it still explains instead of failing on the archive.
	code, _, errOut = run(t, "--archive", t.TempDir(), "merge")
	if code != core.ExitUsage || !strings.Contains(errOut, "git's job") {
		t.Errorf("merge outside an archive = %d %q", code, errOut)
	}
}
