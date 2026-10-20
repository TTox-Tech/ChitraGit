package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestLayering(t *testing.T) {
	dir := t.TempDir()
	user := writeFile(t, dir, "user.toml", `
[sync]
workers = 4
mode = "fetch"
[ui]
theme = "high-contrast"
`)
	arc := writeFile(t, dir, "archive.toml", `
[sync]
workers = 12
`)
	cfg, warnings, err := Load(Sources{
		UserFile:    user,
		ArchiveFile: arc,
		Environ:     []string{"CHITRAGIT_UI_THEME=ledger-light", "UNRELATED=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.Sync.Workers != 12 {
		t.Errorf("archive should override user: workers = %d", cfg.Sync.Workers)
	}
	if cfg.Sync.Mode != "fetch" {
		t.Errorf("user value should survive when archive does not set it: mode = %q", cfg.Sync.Mode)
	}
	if cfg.UI.Theme != "ledger-light" {
		t.Errorf("env should override files: theme = %q", cfg.UI.Theme)
	}
	if cfg.Sync.Retries != 3 {
		t.Errorf("default should remain: retries = %d", cfg.Sync.Retries)
	}
}

func TestMissingFilesAreFine(t *testing.T) {
	_, _, err := Load(Sources{UserFile: "/does/not/exist.toml", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnknownKeysWarn(t *testing.T) {
	f := writeFile(t, t.TempDir(), "c.toml", "[sync]\nworkerz = 3\n[future]\nx = 1\n")
	_, warnings, err := Load(Sources{UserFile: f, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("want 2 warnings, got %v", warnings)
	}
}

func TestInvalidValuesListed(t *testing.T) {
	f := writeFile(t, t.TempDir(), "c.toml", `
[sync]
mode = "yolo"
workers = 999
[clone]
layout = "{owner}"
`)
	_, _, err := Load(Sources{UserFile: f, Environ: []string{}})
	if core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("want config error, got %v", err)
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *core.Error, got %T", err)
	}
	detail := ce.Detail
	for _, want := range []string{"sync.mode", "sync.workers", "clone.layout"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail should mention %s:\n%s", want, detail)
		}
	}
}

func TestSyntaxError(t *testing.T) {
	f := writeFile(t, t.TempDir(), "c.toml", "[sync\nworkers = ")
	_, _, err := Load(Sources{UserFile: f, Environ: []string{}})
	if core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestBadEnv(t *testing.T) {
	_, _, err := Load(Sources{Environ: []string{"CHITRAGIT_SYNC_WORKERS=lots"}})
	if core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestGetSetAllKeys(t *testing.T) {
	cfg := Defaults()
	for _, k := range Keys() {
		v, err := cfg.Get(k)
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		// Round-trip: setting the printed value must not change it.
		if err := cfg.Set(k, v); err != nil {
			t.Fatalf("Set(%s, %q): %v", k, v, err)
		}
		v2, _ := cfg.Get(k)
		if v != v2 {
			t.Errorf("%s: round trip %q -> %q", k, v, v2)
		}
	}
	if _, err := cfg.Get("nope.key"); err == nil {
		t.Error("unknown key should fail")
	}
}

func TestSetTypes(t *testing.T) {
	cfg := Defaults()
	must := func(k, v string) {
		t.Helper()
		if err := cfg.Set(k, v); err != nil {
			t.Fatalf("Set(%s,%s): %v", k, v, err)
		}
	}
	must("sync.timeout", "90s")
	must("guardian.stale_after", "30d")
	must("scan.exclude", "a, b ,c")
	must("sync.per_host_limit", "github.com=4,gitlab.com=2")
	must("ui.mouse", "false")

	if cfg.Sync.Timeout.Std() != 90*time.Second {
		t.Errorf("timeout = %v", cfg.Sync.Timeout)
	}
	if cfg.Guardian.StaleAfter.Std() != 30*24*time.Hour {
		t.Errorf("stale_after = %v", cfg.Guardian.StaleAfter)
	}
	if strings.Join(cfg.Scan.Exclude, "|") != "a|b|c" {
		t.Errorf("exclude = %v", cfg.Scan.Exclude)
	}
	if cfg.Sync.PerHostLimit["gitlab.com"] != 2 || len(cfg.Sync.PerHostLimit) != 2 {
		t.Errorf("per_host_limit = %v", cfg.Sync.PerHostLimit)
	}
	if err := cfg.Set("sync.workers", "x"); err == nil {
		t.Error("non-number should fail")
	}
}

func TestDuration(t *testing.T) {
	cases := map[string]time.Duration{"10m": 10 * time.Minute, "7d": 7 * 24 * time.Hour, "1h30m": 90 * time.Minute}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "d", "-1d", "soon"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) should fail", bad)
		}
	}
	for d, want := range map[time.Duration]string{
		180 * 24 * time.Hour: "180d", 10 * time.Minute: "10m", time.Hour: "1h",
		90 * time.Second: "1m30s", 25 * time.Hour: "25h",
	} {
		if s := Duration(d).String(); s != want {
			t.Errorf("Duration(%v).String() = %q, want %q", d, s, want)
		}
	}
}

func TestSetInFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := SetInFile(f, "sync.workers", "7"); err != nil {
		t.Fatal(err)
	}
	if err := SetInFile(f, "guardian.stale_after", "90d"); err != nil {
		t.Fatal(err)
	}
	if err := SetInFile(f, "sync.mode", "bogus"); core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("invalid value should be refused, got %v", err)
	}
	cfg, warnings, err := Load(Sources{UserFile: f, Environ: []string{}})
	if err != nil || len(warnings) > 0 {
		t.Fatalf("reload: %v %v", err, warnings)
	}
	if cfg.Sync.Workers != 7 || cfg.Guardian.StaleAfter.Std() != 90*24*time.Hour {
		t.Errorf("values not persisted: %+v %+v", cfg.Sync, cfg.Guardian)
	}
}
