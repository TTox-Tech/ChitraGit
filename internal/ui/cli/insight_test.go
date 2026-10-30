package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestShowActivityHealthTags(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	for _, name := range []string{"alice/tool", "bob/tool", "bob/lib"} {
		if out, err := exec.Command("git", "init", "-q", filepath.Join(root, name)).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}

	if code, _, _ := run(t, "--archive", root, "show", "tool"); code != core.ExitUsage {
		t.Errorf("ambiguous show exit = %d", code)
	}
	if code, _, _ := run(t, "--archive", root, "show", "nope"); code == 0 {
		t.Error("show of an unknown repo succeeded")
	}
	code, out, errOut := run(t, "--archive", root, "--json", "show", "alice/tool")
	var show struct {
		Path    string `json:"path"`
		Commits []any  `json:"commits"`
	}
	if err := json.Unmarshal([]byte(out), &show); err != nil || code != 0 || show.Path != "alice/tool" || show.Commits == nil {
		t.Fatalf("show = %d %q %q %v", code, out, errOut, err)
	}

	if code, _, errOut := run(t, "--archive", root, "tag", "add", "work", "--only", "bob/*"); code != 0 {
		t.Fatalf("tag add: %s", errOut)
	}
	if code, _, _ := run(t, "--archive", root, "tag", "add", "work"); code != core.ExitUsage {
		t.Errorf("tag add with no repos exit = %d", code)
	}
	if code, out, _ = run(t, "--archive", root, "tag", "ls"); code != 0 || !strings.Contains(out, "work") || !strings.Contains(out, "2 repos") {
		t.Errorf("tag ls = %d %q", code, out)
	}
	if code, _, _ = run(t, "--archive", root, "fav", "bob/lib"); code != 0 {
		t.Errorf("fav exit = %d", code)
	}

	code, out, _ = run(t, "--archive", root, "--json", "health")
	var h struct {
		Total int            `json:"total"`
		Cards map[string]int `json:"cards"`
	}
	if err := json.Unmarshal([]byte(out), &h); err != nil || h.Total != 3 || h.Cards["favourites"] != 1 {
		t.Errorf("health = %d %q", code, out)
	}
	if code, out, _ = run(t, "--archive", root, "health", "--card", "favourites"); code != 0 || strings.TrimSpace(out) != "bob/lib" {
		t.Errorf("health --card = %d %q", code, out)
	}
	if code, _, _ = run(t, "--archive", root, "health", "--card", "bogus"); code == 0 {
		t.Error("unknown card accepted")
	}

	if code, out, _ = run(t, "--archive", root, "activity"); code != 0 || !strings.Contains(out, "Nothing has changed") {
		t.Errorf("activity = %d %q", code, out)
	}
	if code, _, _ = run(t, "--archive", root, "activity", "--since", "soon"); code != core.ExitUsage {
		t.Errorf("bad --since exit = %d", code)
	}
}
