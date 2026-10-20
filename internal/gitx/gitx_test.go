package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func newRunner(t *testing.T) *Runner {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r, err := New(Options{HooksDir: t.TempDir(), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// initRepo creates a repo with one commit using plain git (no hardening).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestParseVersion(t *testing.T) {
	cases := map[string]Version{
		"git version 2.43.0":                 {2, 43, 0},
		"git version 2.39.3 (Apple Git-146)": {2, 39, 3},
		"git version 2.45.1.windows.1":       {2, 45, 1},
		"git version 2.30.rc1":               {2, 30, 0},
		"git version 2.47.0\n":               {2, 47, 0},
	}
	for in, want := range cases {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseVersion("hello"); err == nil {
		t.Error("garbage should fail")
	}
	if !(Version{2, 29, 9}).Less(MinVersion) || (Version{2, 30, 0}).Less(MinVersion) {
		t.Error("Less is wrong")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]core.Category{
		"fatal: unable to access 'https://x/': Could not resolve host: x":                      core.CatNetwork,
		"remote: Repository not found.\nfatal: repository 'https://github.com/a/b/' not found": core.CatNotFound,
		"fatal: unable to access 'https://h/': The requested URL returned error: 451":          core.CatNotFound,
		"git@github.com: Permission denied (publickey).":                                       core.CatAuth,
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":   core.CatAuth,
		"fatal: detected dubious ownership in repository at '/x'":                              core.CatPermission,
		"fatal: Unable to create '/x/.git/index.lock': File exists.":                           core.CatUnsafeState,
		"error: RPC failed; curl 56 GnuTLS recv error":                                         core.CatNetwork,
		"fatal: write error: No space left on device":                                          core.CatDisk,
		"something unexpected": core.CatInternal,
	}
	for stderr, want := range cases {
		got := classify([]string{"fetch"}, []byte(stderr), errors.New("exit 128"))
		if got.Category != want {
			t.Errorf("classify(%q) = %s, want %s", stderr, got.Category, want)
		}
		if got.Detail == "" && stderr != "" {
			t.Errorf("detail should carry stderr for %q", stderr)
		}
	}
}

func TestHardenedEnv(t *testing.T) {
	env := hardenedEnv([]string{
		"HOME=/home/u", "GIT_TERMINAL_PROMPT=1", "LC_ALL=de_DE.UTF-8", "HTTPS_PROXY=http://p",
	}, "")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{"\nGIT_TERMINAL_PROMPT=0\n", "\nLC_ALL=C\n", "\nHOME=/home/u\n",
		"\nHTTPS_PROXY=http://p\n", "\nGIT_SSH_COMMAND=ssh -o BatchMode=yes\n", "\nGCM_INTERACTIVE=never\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(joined, "GIT_TERMINAL_PROMPT=1") || strings.Contains(joined, "de_DE") {
		t.Error("user values that break automation must be replaced")
	}

	env = hardenedEnv([]string{"GIT_SSH_COMMAND=ssh -i ~/.ssh/k"}, "ignored")
	if !contains(env, "GIT_SSH_COMMAND=ssh -i ~/.ssh/k -o BatchMode=yes") {
		t.Errorf("user GIT_SSH_COMMAND must be kept: %v", env)
	}
	env = hardenedEnv(nil, "ssh -F ~/.ssh/cfg")
	if !contains(env, "GIT_SSH_COMMAND=ssh -F ~/.ssh/cfg -o BatchMode=yes") {
		t.Errorf("core.sshCommand must be kept: %v", env)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestRunAndVersion(t *testing.T) {
	r := newRunner(t)
	ctx := context.Background()
	v, err := r.CheckVersion(ctx)
	if err != nil {
		t.Fatalf("CheckVersion: %v", err)
	}
	if v.Major < 2 {
		t.Errorf("odd version %v", v)
	}
	repo := initRepo(t)
	out, err := r.Output(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out != "main" {
		t.Fatalf("rev-parse = %q, %v", out, err)
	}
}

func TestErrorIsClassified(t *testing.T) {
	r := newRunner(t)
	_, err := r.Run(context.Background(), t.TempDir(), "status")
	if core.CategoryOf(err) != core.CatCorrupt {
		t.Fatalf("status outside a repo: want corrupt/not-a-repo, got %v", err)
	}
}

// A malicious repo must not be able to run code through hooks or fsmonitor.
func TestRepoConfigCannotExecute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script hook")
	}
	r := newRunner(t)
	repo := initRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")

	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := exec.Command("git", "config", "core.fsmonitor", "touch "+marker+"; false")
	cfg.Dir = repo
	if out, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}

	ctx := context.Background()
	if _, err := r.Run(ctx, repo, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, err := r.Run(ctx, repo, "-c", "user.email=t@x", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "second"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repo-controlled hook or fsmonitor was executed")
	}
}

func TestTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on a POSIX shell alias")
	}
	r := newRunner(t)
	r.timeout = 200 * time.Millisecond
	repo := initRepo(t)
	start := time.Now()
	_, err := r.Run(context.Background(), repo, "-c", "alias.slow=!sleep 5", "slow")
	if core.CategoryOf(err) != core.CatTimeout {
		t.Fatalf("want timeout, got %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("timeout did not stop git promptly")
	}
}
