package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseDuration: a duration that parses is never negative from a day
// count, and prints back to the same value.
func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"10m", "7d", "1h30m", "180d", "", "d", "-1d", "soon", "1.5h"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDuration(s)
		if err != nil {
			return
		}
		if d < 0 && len(s) > 0 && s[len(s)-1] == 'd' {
			t.Fatalf("ParseDuration(%q) = %v: negative day count", s, d)
		}
		back, err := ParseDuration(Duration(d).String())
		if err != nil || back != d {
			t.Fatalf("ParseDuration(%q) = %v, but %q gives %v, %v", s, d, Duration(d).String(), back, err)
		}
	})
}

// FuzzLoad feeds Load arbitrary config files: it must never panic, and a
// config it accepts must pass its own validation again.
func FuzzLoad(f *testing.F) {
	for _, s := range []string{
		"[sync]\nworkers = 3\n[ui]\ntheme = \"ledger-light\"\n",
		"[sync]\nworkerz = 3\n[future]\nx = 1\n",
		"[sync\nworkers = ",
		"[sync]\nper_host_limit = { \"gitlab.com\" = 2 }\n",
		"[sync]\ntimeout = \"7d\"\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		p := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := Load(Sources{UserFile: p, Environ: []string{}})
		if err != nil {
			return
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Load accepted %q but it does not validate: %v", body, err)
		}
	})
}
