package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func gitT(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git %v: %v %s", args, err, out)
	}
}

func TestRescueLockBundleFollowCommands(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	up := filepath.Join(t.TempDir(), "up.git")
	gitT(t, "init", "-q", "--bare", up)
	dev := filepath.Join(t.TempDir(), "dev")
	gitT(t, "clone", "-q", up, dev)
	gitT(t, "-C", dev, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, "-C", dev, "push", "-q", "origin", "HEAD")
	gitT(t, "clone", "-q", up, filepath.Join(root, "keep"))

	// Upstream vanishes: two syncs later the repo is rescued and bundled.
	if err := os.RemoveAll(up); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if code, _, errOut := run(t, "--archive", root, "sync"); code != core.ExitPartial {
			t.Fatalf("sync %d: exit %d %s", i, code, errOut)
		}
	}
	code, out, _ := run(t, "--archive", root, "ls")
	if code != 0 || !strings.Contains(out, "[RESCUED]") {
		t.Fatalf("ls = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".chitragit", "bundles", "keep.bundle")); err != nil {
		t.Fatalf("no automatic bundle: %v", err)
	}

	// Unlocking needs the path typed again.
	if code, _, _ := run(t, "--archive", root, "unlock", "keep"); code != core.ExitUsage {
		t.Fatalf("unlock without confirmation = %d", code)
	}
	if code, _, errOut := run(t, "--archive", root, "unlock", "keep", "--confirm", "keep"); code != 0 {
		t.Fatalf("unlock: %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "--archive", root, "lock", "keep"); code != 0 {
		t.Fatalf("lock: %d %s", code, errOut)
	}
	if code, out, _ := run(t, "--archive", root, "bundle", "--rescued"); code != 0 || !strings.Contains(out, "keep.bundle") {
		t.Fatalf("bundle --rescued = %d\n%s", code, out)
	}
	if code, _, _ := run(t, "--archive", root, "bundle", "nope"); code != core.ExitUsage {
		t.Fatalf("bundle of unknown repo = %d", code)
	}

	if code, _, _ := run(t, "--archive", root, "follow", "svn:x"); code != core.ExitUsage {
		t.Fatalf("bad source = %d", code)
	}
	if code, out, _ := run(t, "--archive", root, "sources"); code != 0 || !strings.Contains(out, "Not following") {
		t.Fatalf("sources = %d %s", code, out)
	}
	if code, _, _ := run(t, "--archive", root, "unfollow", "github:nobody"); code != core.ExitUsage {
		t.Fatalf("unfollow unknown = %d", code)
	}
}
