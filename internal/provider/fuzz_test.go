package provider

import (
	"strings"
	"testing"
)

// FuzzParse feeds Parse arbitrary remote URLs: it must never panic, and
// what it returns must be usable as a host and a repo name.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"https://github.com/charmbracelet/glow.git", "git@github.com:TTox-Tech/chitragit.git",
		"ssh://git@gitlab.com:2222/gnome/sub/gtk.git", "https://codeberg.org/forgejo/forgejo/",
		"/srv/git/project.git", `C:\repos\project`, "../relative/path", "git://[::1]:9418/x.git", "",
	} {
		f.Add(s)
	}
	known := map[string]bool{"github": true, "gitlab": true, "gitea": true, "generic": true}
	f.Fuzz(func(t *testing.T, raw string) {
		r := Parse(raw)
		if !known[r.Provider] {
			t.Fatalf("Parse(%q): unknown provider %q", raw, r.Provider)
		}
		if r.Host != strings.ToLower(r.Host) {
			t.Fatalf("Parse(%q): host %q is not lower-case", raw, r.Host)
		}
		if strings.Contains(r.Name, "/") {
			t.Fatalf("Parse(%q): name %q has a slash", raw, r.Name)
		}
		if r.Host == "" && r != (Remote{Provider: "generic"}) {
			t.Fatalf("Parse(%q) = %+v: no host but more fields", raw, r)
		}
	})
}

// FuzzParseSource feeds ParseSource arbitrary specs: it must never panic,
// and every spec it accepts must come back the same from its String.
func FuzzParseSource(f *testing.F) {
	for _, s := range []string{
		"github:torvalds", "github:@starred", "gitlab:gnome/sub", "codeberg:forgejo",
		"gitea@git.example.com:team", "github@ghe.corp:infra", "forgejo@x:y", "github:../x", "codeberg@x:y",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, spec string) {
		src, err := ParseSource(spec)
		if err != nil {
			return
		}
		again, err := ParseSource(src.String())
		if err != nil || again != src {
			t.Fatalf("ParseSource(%q) = %+v, but its String %q gives %+v, %v", spec, src, src.String(), again, err)
		}
	})
}
