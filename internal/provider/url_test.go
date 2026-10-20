package provider

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Remote{
		"https://github.com/charmbracelet/glow.git":   {"github", "github.com", "charmbracelet", "glow"},
		"https://GitHub.com/torvalds/linux":           {"github", "github.com", "torvalds", "linux"},
		"git@github.com:TTox-Tech/chitragit.git":      {"github", "github.com", "TTox-Tech", "chitragit"},
		"ssh://git@gitlab.com:2222/gnome/sub/gtk.git": {"gitlab", "gitlab.com", "gnome/sub", "gtk"},
		"https://codeberg.org/forgejo/forgejo/":       {"gitea", "codeberg.org", "forgejo", "forgejo"},
		"https://git.example.org/team/tool.git":       {"generic", "git.example.org", "team", "tool"},
		"/srv/git/project.git":                        {Provider: "generic"},
		`C:\repos\project`:                            {Provider: "generic"},
		"../relative/path":                            {Provider: "generic"},
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %+v, want %+v", in, got, want)
		}
	}
}
