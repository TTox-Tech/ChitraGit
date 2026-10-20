package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// run executes the CLI with an isolated user config/cache directory.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CHITRAGIT_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("CHITRAGIT_CACHE_DIR", filepath.Join(home, "cache"))
	t.Setenv("NO_COLOR", "1")
	return home
}

func TestInitDoctorConfig(t *testing.T) {
	isolate(t)
	root := t.TempDir()

	code, out, errOut := run(t, "init", root)
	if code != 0 || !strings.Contains(out, "Archive created") {
		t.Fatalf("init: %d %q %q", code, out, errOut)
	}

	code, out, _ = run(t, "--archive", root, "--json", "doctor")
	var report struct {
		Checks []check `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor json: %v\n%s", err, out)
	}
	byName := map[string]checkStatus{}
	for _, c := range report.Checks {
		byName[c.Name] = c.Status
	}
	for _, name := range []string{"Git", "Archive", "Database", "Configuration", "Archive lock"} {
		if byName[name] != statusOK {
			t.Errorf("doctor %s = %q (exit %d)\n%s", name, byName[name], code, out)
		}
	}

	if code, _, errOut := run(t, "--archive", root, "config", "set", "sync.workers", "5"); code != 0 {
		t.Fatalf("config set: %d %s", code, errOut)
	}
	code, out, _ = run(t, "--archive", root, "config", "get", "sync.workers")
	if code != 0 || strings.TrimSpace(out) != "5" {
		t.Fatalf("config get = %d %q", code, out)
	}

	// The value went to the archive file, not the user file.
	b, err := os.ReadFile(filepath.Join(root, ".chitragit", "config.toml"))
	if err != nil || !strings.Contains(string(b), "workers = 5") {
		t.Fatalf("archive config: %q %v", b, err)
	}

	if code, _, _ := run(t, "--archive", root, "config", "set", "sync.mode", "yolo"); code != core.ExitUsage {
		t.Errorf("invalid value exit = %d, want %d", code, core.ExitUsage)
	}
	if code, _, _ := run(t, "--archive", root, "config", "validate"); code != 0 {
		t.Errorf("validate exit = %d", code)
	}
}

func TestUserScope(t *testing.T) {
	home := isolate(t)
	if code, _, errOut := run(t, "config", "set", "--user", "ui.theme", "high-contrast"); code != 0 {
		t.Fatalf("set --user: %d %s", code, errOut)
	}
	b, err := os.ReadFile(filepath.Join(home, "config", "config.toml"))
	if err != nil || !strings.Contains(string(b), "high-contrast") {
		t.Fatalf("user config: %q %v", b, err)
	}
}

func TestExitCodes(t *testing.T) {
	isolate(t)
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"--bogus-flag"}, core.ExitUsage},
		{[]string{"nonsense"}, core.ExitUsage},
		{[]string{"config", "get"}, core.ExitUsage},
		{[]string{"--archive", t.TempDir(), "config", "get", "sync.workers"}, core.ExitUsage},
		{[]string{"version"}, core.ExitOK},
	}
	for _, c := range cases {
		if code, _, errOut := run(t, c.args...); code != c.want {
			t.Errorf("%v: exit %d, want %d (%s)", c.args, code, c.want, errOut)
		}
	}
}

func TestLockedArchiveDoctorWarns(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, e := run(t, "init", root); code != 0 {
		t.Fatal(e)
	}
	lock := filepath.Join(root, ".chitragit", "lock")
	host, _ := os.Hostname()
	data, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "host": host, "started": "2026-01-01T00:00:00Z", "command": "sync"})
	if err := os.WriteFile(lock, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, _ := run(t, "--archive", root, "--json", "doctor")
	if !strings.Contains(out, `"status": "warn"`) || !strings.Contains(out, "archive is busy") {
		t.Fatalf("doctor should warn about the lock:\n%s", out)
	}
}
