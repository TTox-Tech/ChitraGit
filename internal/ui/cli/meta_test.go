package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
)

// show prints what the forge said about a repo, and ls filters on it.
func TestShowAndListRepoMeta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	for _, name := range []string{"me/tool", "me/site", "me/plain"} {
		dir := filepath.Join(root, name)
		gitT(t, ".", "init", "-q", dir)
		gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	arc, err := engine.OpenArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := engine.OpenStore(context.Background(), arc.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	repos, _ := st.Repos(context.Background())
	for _, r := range repos {
		ids[r.Path] = r.ID
	}
	err = st.SetRepoMeta(context.Background(), map[string]core.RepoMeta{
		ids["me/tool"]: {Description: "a small tool", Topics: []string{"CLI", "git"}, Stars: 12, Language: "Go", License: "MIT"},
		ids["me/site"]: {Language: "HTML", Topics: []string{"web"}, Homepage: "https://example.com"},
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}

	code, out, _ := run(t, "--archive", root, "show", "me/tool")
	for _, want := range []string{"a small tool", "★ 12 · Go · MIT", "CLI, git"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}
	if code, out, _ = run(t, "--archive", root, "--json", "show", "me/plain"); code != 0 || strings.Contains(out, `"meta"`) {
		t.Errorf("a repo the forge said nothing about has meta: %s", out)
	}
	code, out, _ = run(t, "--archive", root, "--json", "show", "me/site")
	var got struct {
		Meta struct {
			Homepage string `json:"homepage"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.Meta.Homepage != "https://example.com" {
		t.Errorf("show --json meta = %q", out)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--language", "go"}, "me/tool"},
		{[]string{"--topic", "cli"}, "me/tool"},
		{[]string{"--topic", "web", "--language", "html"}, "me/site"},
		{[]string{"--topic", "web", "--language", "go"}, ""},
	} {
		_, out, _ := run(t, append([]string{"--archive", root, "ls"}, c.args...)...)
		if strings.TrimSpace(out) == "" != (c.want == "") || c.want != "" && (!strings.Contains(out, c.want) || strings.Count(out, "\n") != 1) {
			t.Errorf("ls %v = %q, want %q", c.args, out, c.want)
		}
	}
}
