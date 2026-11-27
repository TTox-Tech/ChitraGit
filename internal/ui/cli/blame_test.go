package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBlame(t *testing.T) {
	root, first := codeArchive(t)
	code, out, errOut := run(t, "--archive", root, "blame", "proj", "main.go")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 0 || len(lines) != 4 || !strings.HasPrefix(lines[0], "^"+first[:7]) || strings.HasPrefix(lines[2], "^") ||
		!strings.HasSuffix(lines[2], "// Entry point.") {
		t.Fatalf("blame = %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = run(t, "--archive", root, "--json", "blame", "proj@"+first+":main.go")
	var got struct {
		Blame struct {
			Lines []struct{ SHA string }
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Blame.Lines) != 3 || got.Blame.Lines[0].SHA != first {
		t.Errorf("blame --json at first = %d %q", code, out)
	}
	for _, args := range [][]string{{"proj"}, {"proj:"}, {"proj:main.go", "main.go"}, {"proj", "nope.go"}} {
		if code, _, _ := run(t, append([]string{"--archive", root, "blame"}, args...)...); code == 0 {
			t.Errorf("blame %v succeeded", args)
		}
	}
}
