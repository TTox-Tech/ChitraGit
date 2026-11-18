package audit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseGitConfigMatchesGit checks the git-free .gitmodules reader
// against git itself: on every input it either gives what git gives, or
// refuses the file (and Shield then calls it unreadable). It must never
// read a file differently from git, since that is how a hostile URL or
// path would slip past.
func TestParseGitConfigMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	inputs := []string{
		"[submodule \"a\"]\n\tpath = a\n\turl = https://example.com/a.git\n",
		"[submodule \"a b\"]\npath=x\nurl=\"-u./payload\"\n",
		"[Submodule \"Case\"]\n\tURL = https://x\n",
		"[submodule \"q\\\"uote\"]\n\turl = a ; comment\n\tpath = \"b # not a comment\"\n",
		"[submodule \"esc\"]\n\turl = a\\tb\\\\c\n",
		"[submodule \"cont\"]\n\turl = ext::sh \\\n -c touch\n",
		"[submodule \"s\"]\n\tupdate = !rm -rf /\n\tpath = ../../escape\n",
		"# comment\n; comment\n\n[submodule \"s\"] url = inline\n",
		"[submodule \"s\"]\n\tbare\n",
		"[submodule \"s\"]\n\turl =   spaced   value  \n",
		"[submodule.old]\n\turl = x\n",
		"[submodule \"open\n\turl = x\n",
		"url = no section\n",
		"[submodule \"s\"]\n\turl = \"unterminated\n",
		"[submodule \"s\"]\n\turl = bad\\qescape\n",
		"[submodule \"s\"]\n\t1url = x\n",
		"[submodule \"../../modules/x\"]\n\tpath = x\n",
		"[submodule \"s\"]\r\n\turl = crlf\r\n",
	}
	dir := t.TempDir()
	for i, in := range inputs {
		f := filepath.Join(dir, "cfg")
		if err := os.WriteFile(f, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ParseGitConfig([]byte(in))
		out, gitErr := exec.Command("git", "config", "--file", f, "--list", "-z").Output()
		if err != nil {
			continue // refusing is always allowed
		}
		if gitErr != nil {
			t.Errorf("input %d: git refuses %q but ParseGitConfig read %v", i, in, got)
			continue
		}
		var want [][2]string
		for item := range bytes.SplitSeq(out, []byte{0}) {
			if k, v, _ := strings.Cut(string(item), "\n"); k != "" {
				want = append(want, [2]string{k, v})
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("input %d %q:\n got %q\nwant %q", i, in, got, want)
		}
	}
}
