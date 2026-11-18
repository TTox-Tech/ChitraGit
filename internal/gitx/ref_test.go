package gitx

import "testing"

func TestParseRef(t *testing.T) {
	for _, c := range []struct {
		arg  string
		want Ref
	}{
		{"cli", Ref{Repo: "cli"}},
		{"github.com/cli/cli", Ref{Repo: "github.com/cli/cli"}},
		{"cli/cli@v2.40.1", Ref{Repo: "cli/cli", Rev: "v2.40.1"}},
		{"cli/cli:README.md", Ref{Repo: "cli/cli", Path: "README.md"}},
		{"git.example.com/a/b/c@release-1.0.0-rc.1:docs/My Notes.md", Ref{"git.example.com/a/b/c", "release-1.0.0-rc.1", "docs/My Notes.md"}},
		{"cli/cli@main@{1}:a b/c:d@e.txt", Ref{"cli/cli", "main@{1}", "a b/c:d@e.txt"}},
		{"cli/cli@HEAD~2", Ref{Repo: "cli/cli", Rev: "HEAD~2"}},
		{`cli/cli@v1.2:docs\guide\read me.md`, Ref{"cli/cli", "v1.2", "docs/guide/read me.md"}},
		{"cli/cli@v1:docs/", Ref{"cli/cli", "v1", "docs"}},
		{"cli/cli@v1:", Ref{Repo: "cli/cli", Rev: "v1"}},
	} {
		got, err := ParseRef(c.arg)
		if err != nil || got != c.want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", c.arg, got, err, c.want)
		}
	}
	for _, arg := range []string{
		"", "@v1", ":README.md", // no repo
		"cli/cli@", "cli/cli@:x", "cli/cli@--output=/tmp/x", "cli/cli@-p:x", "cli/cli@a\nb", // bad rev
		"cli/cli:../secret", "cli/cli:/etc/passwd", `cli/cli:..\..\x`, "cli/cli:a//b", "cli/cli:./a", // path escapes
	} {
		if got, err := ParseRef(arg); err == nil {
			t.Errorf("ParseRef(%q) = %+v; want an error", arg, got)
		}
	}
}
