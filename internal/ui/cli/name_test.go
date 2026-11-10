package cli

import (
	"strings"
	"testing"
)

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/chitragit":          "chitragit",
		"/home/u/.local/bin/chit":           "chit",
		"CHIT.EXE":                          "chit",
		"cg":                                "cg",
		"/usr/lib/git-core/../bin/git-chit": "git-chit",
		"/tmp/go-build123/cli.test":         "chitragit",
		"":                                  "chitragit",
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunAsChit(t *testing.T) {
	isolate(t)
	defer runWith(&app{}, "version") // put {cmd} back to chitragit for later tests

	code, out, _ := runWith(&app{name: "chit"}, "--help")
	if code != 0 || !strings.Contains(out, "chit [command]") || strings.Contains(out, "chitragit [command]") {
		t.Errorf("chit --help = %d %q", code, out)
	}
	code, _, errOut := runWith(&app{name: "chit"}, "nope")
	if code == 0 || !strings.Contains(errOut, "see `chit --help`") {
		t.Errorf("chit nope = %d %q", code, errOut)
	}
	// A hint from a package that cannot know the name is rewritten too.
	code, _, errOut = runWith(&app{name: "chit"}, "--archive", t.TempDir(), "status")
	if code == 0 || strings.Contains(errOut, "`chitragit ") || !strings.Contains(errOut, "`chit init`") {
		t.Errorf("chit status outside an archive = %d %q", code, errOut)
	}

	code, out, _ = runWith(&app{}, "--help")
	if code != 0 || !strings.Contains(out, "chitragit [command]") {
		t.Errorf("chitragit --help = %d %q", code, out)
	}
}
