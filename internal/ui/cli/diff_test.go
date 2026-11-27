package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
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
	gitT(t, ".", "clone", "-q", up, filepath.Join(root, "proj"))
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	if code, _, errOut := run(t, "--archive", root, "diff"); code == 0 || !strings.Contains(errOut, "no sync has run yet") {
		t.Errorf("diff before any sync = %d %q", code, errOut)
	}

	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	gitT(t, dev, "commit", "-q", "--allow-empty", "-m", "three")
	gitT(t, dev, "push", "-q", "origin", "main")
	if code, _, errOut := run(t, "--archive", root, "sync"); code != 0 {
		t.Fatalf("sync: %s", errOut)
	}
	code, out, errOut := run(t, "--archive", root, "diff")
	if code != 0 || !strings.Contains(out, "refs/heads/main") || !strings.Contains(out, "forward") ||
		!strings.Contains(out, "1 repo(s) changed, 2 new commit(s)") {
		t.Fatalf("diff = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ = run(t, "--archive", root, "diff", "--stat", "proj"); code != 0 || strings.Contains(out, "refs/heads/main") ||
		!strings.Contains(out, "proj") {
		t.Errorf("diff --stat proj = %d %q", code, out)
	}

	// The last sync's snapshots: compare them by ID, and the first with now.
	code, out, _ = run(t, "--archive", root, "--json", "diff")
	var got struct {
		Diffs []struct {
			From, To int64
			New      int `json:"new_commits"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Diffs) != 1 || got.Diffs[0].New != 2 {
		t.Fatalf("json diff = %d %q", code, out)
	}
	from, to := strconv.FormatInt(got.Diffs[0].From, 10), strconv.FormatInt(got.Diffs[0].To, 10)
	for _, args := range [][]string{{from, to}, {from}, {from, "proj"}} {
		code, out, errOut = run(t, append([]string{"--archive", root, "diff"}, args...)...)
		if code != 0 || !strings.Contains(out, "2 new commit(s)") {
			t.Errorf("diff %v = %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if code, out, _ = run(t, "--archive", root, "diff", to); code != 0 || !strings.Contains(out, "Nothing changed") {
		t.Errorf("diff with now = %d %q", code, out)
	}
	for _, args := range [][]string{{"0"}, {"1", "2", "3"}, {"a", "b"}, {"--run", "1", from}} {
		if code, _, _ = run(t, append([]string{"--archive", root, "diff"}, args...)...); code == 0 {
			t.Errorf("diff %v: no usage error", args)
		}
	}
}
