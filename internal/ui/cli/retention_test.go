package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetention(t *testing.T) {
	isolate(t)
	t.Setenv("CHITRAGIT_RETENTION_KEEP_LAST", "1")
	t.Setenv("CHITRAGIT_RETENTION_KEEP_DAILY", "0")
	t.Setenv("CHITRAGIT_RETENTION_KEEP_WEEKLY", "0")
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	app := filepath.Join(root, "app")
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		cmd.Dir = app
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", app).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	for i, msg := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(app, "a.txt"), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn("add", "-A")
		gitIn("commit", "-q", "-m", msg)
		if i == 0 {
			if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
				t.Fatalf("scan: %s", errOut)
			}
		}
		if code, _, errOut := run(t, "--archive", root, "snapshot", "take", "app"); code != 0 {
			t.Fatalf("snapshot take: %s", errOut)
		}
	}

	code, out, errOut := run(t, "--archive", root, "retention")
	if code != 0 || !strings.Contains(out, "Would remove 2 snapshots and 2 pins, and keep 1") || strings.Count(out, "  app") != 2 {
		t.Fatalf("retention: %d %q %q", code, out, errOut)
	}
	if code, out, _ := run(t, "--archive", root, "retention", "--all"); code != 0 || !strings.Contains(out, "last") {
		t.Fatalf("retention --all: %d %q", code, out)
	}
	code, out, errOut = run(t, "--archive", root, "retention", "--apply")
	if code != 0 || !strings.Contains(out, "Removed 2 snapshots and 2 pins; kept 1") {
		t.Fatalf("retention --apply: %d %q %q", code, out, errOut)
	}

	// `gc` is the git word for it; a second run has nothing to do.
	code, out, _ = run(t, "--archive", root, "--json", "gc", "--apply")
	var rep struct {
		Applied       bool
		Kept, Removed int
		Unpinned      int
	}
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || !rep.Applied || rep.Kept != 1 || rep.Removed != 0 || rep.Unpinned != 0 {
		t.Fatalf("gc --json: %d %q", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "verify"); code != 0 {
		t.Fatalf("verify after retention: %d %q", code, out)
	}
}
