package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestRescueLockBundleFollowCommands(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	up := filepath.Join(t.TempDir(), "up.git")
	gitT(t, ".", "init", "-q", "--bare", up)
	dev := filepath.Join(t.TempDir(), "dev")
	gitT(t, ".", "clone", "-q", up, dev)
	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	gitT(t, dev, "push", "-q", "origin", "HEAD")
	gitT(t, ".", "clone", "-q", up, filepath.Join(root, "keep"))

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
	if code, _, _ := run(t, "--archive", root, "bundle", "nope"); code == 0 {
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
