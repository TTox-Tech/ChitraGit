package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShowPath(t *testing.T) {
	root, first := codeArchive(t)
	if code, out, errOut := run(t, "--archive", root, "show", "proj:main.go"); code != 0 || out != "package main\n\n// Entry point.\nfunc main() {}\n" {
		t.Errorf("show proj:main.go = %d %q %q", code, out, errOut)
	}
	if code, out, _ := run(t, "--archive", root, "show", "proj@"+first+":main.go"); code != 0 || strings.Contains(out, "Entry point") {
		t.Errorf("show at the first commit = %d %q", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "show", "proj@main:docs"); code != 0 || out != "नमस्ते.md\n" {
		t.Errorf("show a folder = %d %q", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "show", "proj:logo.png"); code != 0 || !strings.Contains(out, "binary") {
		t.Errorf("show a binary = %d %q", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "show", "--raw", "proj:logo.png"); code != 0 || out != "\x89PNG\x00\x01" {
		t.Errorf("show --raw = %d %q", code, out)
	}
	code, out, _ := run(t, "--archive", root, "--json", "show", "proj:docs/नमस्ते.md")
	var got struct {
		Path, Text string
		Binary     bool
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.Text != "# hi\n" || got.Binary || got.Path != "docs/नमस्ते.md" {
		t.Errorf("show --json = %d %q", code, out)
	}
	// The plain repo form still works, and bad paths fail.
	if code, out, _ := run(t, "--archive", root, "show", "proj"); code != 0 || !strings.Contains(out, "proj") {
		t.Errorf("show proj = %d %q", code, out)
	}
	for _, arg := range []string{"proj:nope.txt", "proj:../x", "proj@main", "proj@-x:main.go"} {
		if code, _, _ := run(t, "--archive", root, "show", arg); code == 0 {
			t.Errorf("show %s succeeded", arg)
		}
	}
}
