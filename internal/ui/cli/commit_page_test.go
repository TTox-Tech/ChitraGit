package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShowCommit(t *testing.T) {
	root, first := codeArchive(t)
	code, out, errOut := run(t, "--archive", root, "show", "proj@main")
	for _, want := range []string{"commit ", "Author:", "    second", " M main.go", " A logo.png", "binary", "+// Entry point.", "@@ "} {
		if !strings.Contains(out, want) {
			t.Errorf("show proj@main lacks %q", want)
		}
	}
	if code != 0 || strings.Contains(out, "docs/") {
		t.Fatalf("show proj@main = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ = run(t, "--archive", root, "show", "--stat", "proj@main"); code != 0 || strings.Contains(out, "@@") ||
		!strings.Contains(out, "file(s) changed") {
		t.Errorf("show --stat = %d\n%s", code, out)
	}
	// The root commit: everything added.
	code, out, _ = run(t, "--archive", root, "--json", "show", "proj@"+first[:8])
	var got struct {
		Commit struct {
			SHA     string
			Parents []string
			Message string
			Files   []struct{ Status, Path, Patch string }
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("show --json = %d %q", code, out)
	}
	c := got.Commit
	if c.SHA != first || len(c.Parents) != 0 || c.Message != "first" || len(c.Files) != 2 || c.Files[0].Status != "A" || c.Files[0].Patch == "" {
		t.Errorf("root commit = %+v", c)
	}
	for _, arg := range []string{"proj@nope", "proj@-x", "nope@main"} {
		if code, _, _ := run(t, "--archive", root, "show", arg); code == 0 {
			t.Errorf("show %s succeeded", arg)
		}
	}
}
