package syncer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// git runs plain git (no hardening) for test setup.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t",
		"-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is an upstream repo plus a "dev" clone used to push new commits
// to it, so tests can play the role of upstream.
type fixture struct {
	t        *testing.T
	upstream string // bare
	dev      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &fixture{t: t, upstream: filepath.Join(t.TempDir(), "up.git"), dev: filepath.Join(t.TempDir(), "dev")}
	git(t, ".", "init", "-q", "--bare", f.upstream)
	git(t, ".", "clone", "-q", f.upstream, f.dev)
	f.commit("a.txt", "one")
	git(t, f.dev, "push", "-q", "origin", "main")
	return f
}

func (f *fixture) commit(name, content string) {
	write(f.t, f.dev, name, content)
	git(f.t, f.dev, "add", ".")
	git(f.t, f.dev, "commit", "-q", "-m", name+" "+content)
}

func (f *fixture) push(args ...string) {
	git(f.t, f.dev, append([]string{"push", "-q", "origin"}, args...)...)
}

func (f *fixture) clone(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "archive-copy")
	git(t, ".", "clone", "-q", f.upstream, dir)
	return dir
}

func runner(t *testing.T) *gitx.Runner {
	r, err := gitx.New(gitx.Options{HooksDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func syncDir(t *testing.T, dir string, opts Options) Result {
	t.Helper()
	opts.RetryDelay = time.Millisecond
	res := Run(context.Background(), runner(t), []Job{{Repo: core.Repo{Path: "r", Kind: core.KindNormal}, Dir: dir}}, opts, nil)
	return res[0]
}

func TestFastForwardsCleanRepo(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	f.commit("a.txt", "two")
	f.commit("b.txt", "three")
	f.push("main")

	res := syncDir(t, local, Options{})
	if res.Outcome != Updated || res.Status != StatusOK {
		t.Fatalf("got %s/%s: %s %v", res.Outcome, res.Status, res.Reason, res.Err)
	}
	if git(t, local, "rev-parse", "HEAD") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Error("main was not fast-forwarded")
	}
	var commits int
	for _, c := range res.Changes {
		if c.Ref == "refs/heads/main" {
			commits = c.Commits
		}
	}
	if commits != 2 {
		t.Errorf("recorded %d new commits on main, want 2: %+v", commits, res.Changes)
	}

	if again := syncDir(t, local, Options{}); again.Outcome != Unchanged {
		t.Errorf("second sync = %s, want unchanged", again.Outcome)
	}
}

func TestFastForwardsOtherBranchesWithoutCheckout(t *testing.T) {
	f := newFixture(t)
	git(t, f.dev, "branch", "-q", "release")
	f.push("release")
	local := f.clone(t)
	git(t, local, "branch", "-q", "--track", "release", "origin/release")

	git(t, f.dev, "checkout", "-q", "release")
	f.commit("r.txt", "fix")
	f.push("release")

	res := syncDir(t, local, Options{})
	if res.Outcome != Updated {
		t.Fatalf("got %s: %s", res.Outcome, res.Reason)
	}
	if git(t, local, "rev-parse", "release") != git(t, f.dev, "rev-parse", "release") {
		t.Error("release was not fast-forwarded")
	}
	if git(t, local, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Error("checked-out branch changed")
	}
}

func TestNeverTouchesLocalWork(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, f *fixture, local string)
		status string
	}{
		{"dirty", func(t *testing.T, f *fixture, local string) {
			write(t, local, "a.txt", "my unsaved edit")
		}, StatusDirty},
		{"diverged", func(t *testing.T, f *fixture, local string) {
			write(t, local, "mine.txt", "x")
			git(t, local, "add", ".")
			git(t, local, "commit", "-q", "-m", "mine")
		}, StatusDiverged},
		{"detached", func(t *testing.T, f *fixture, local string) {
			git(t, local, "checkout", "-q", "--detach")
		}, StatusDetached},
		{"no upstream", func(t *testing.T, f *fixture, local string) {
			git(t, local, "checkout", "-q", "-b", "feature")
		}, StatusNoUpstream},
		{"blocked by untracked file", func(t *testing.T, f *fixture, local string) {
			write(t, local, "new.txt", "my own file with the same name")
		}, StatusBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			local := f.clone(t)
			tc.setup(t, f, local)
			headBefore := git(t, local, "rev-parse", "HEAD")
			statusBefore := git(t, local, "status", "--porcelain")

			f.commit("new.txt", "upstream")
			f.push("main")

			res := syncDir(t, local, Options{})
			if res.Status != tc.status || res.Reason == "" {
				t.Fatalf("status = %s (%s, %s), want %s", res.Status, res.Outcome, res.Reason, tc.status)
			}
			if git(t, local, "rev-parse", "HEAD") != headBefore || git(t, local, "status", "--porcelain") != statusBefore {
				t.Error("working tree or HEAD changed")
			}
			if git(t, local, "rev-parse", "origin/main") != git(t, f.dev, "rev-parse", "HEAD") {
				t.Error("updates were not downloaded")
			}
		})
	}
}

func TestFetchModeAndDryRun(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	head := git(t, local, "rev-parse", "HEAD")
	f.commit("a.txt", "two")
	f.push("main")
	git(t, f.dev, "tag", "v1")
	f.push("v1")

	res := syncDir(t, local, Options{Mode: "dry-run"})
	if res.Outcome != Updated || !strings.Contains(res.Reason, "2 changed refs") {
		t.Errorf("dry-run = %s %q", res.Outcome, res.Reason)
	}
	if git(t, local, "rev-parse", "origin/main") != head {
		t.Fatal("dry-run downloaded something")
	}

	res = syncDir(t, local, Options{Mode: "fetch"})
	if res.Outcome != Updated || git(t, local, "rev-parse", "HEAD") != head {
		t.Errorf("fetch mode: %s, HEAD moved = %v", res.Outcome, git(t, local, "rev-parse", "HEAD") != head)
	}
	if git(t, local, "rev-parse", "v1") == "" {
		t.Error("tags not fetched")
	}
}

func TestSkipsAndFailures(t *testing.T) {
	f := newFixture(t)

	localOnly := filepath.Join(t.TempDir(), "solo")
	git(t, ".", "init", "-q", localOnly)
	if res := syncDir(t, localOnly, Options{}); res.Outcome != Skipped || res.Status != StatusLocalOnly {
		t.Errorf("local-only: %s/%s", res.Outcome, res.Status)
	}

	busy := f.clone(t)
	write(t, filepath.Join(busy, ".git"), "MERGE_HEAD", git(t, busy, "rev-parse", "HEAD"))
	if res := syncDir(t, busy, Options{}); res.Status != StatusBusy {
		t.Errorf("merge in progress: %s", res.Status)
	}

	gone := f.clone(t)
	git(t, gone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nothing-here"))
	res := syncDir(t, gone, Options{Retries: 3})
	if res.Outcome != Failed || res.Category != string(core.CatNotFound) || res.Attempts != 1 {
		t.Errorf("missing remote: %s %s attempts=%d (%s)", res.Outcome, res.Category, res.Attempts, res.Reason)
	}
}

func TestRetriesNetworkErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := filepath.Join(t.TempDir(), "r")
	git(t, ".", "init", "-q", dir)
	// Port 9 on localhost refuses connections immediately: a network error.
	git(t, dir, "remote", "add", "origin", "http://127.0.0.1:9/x.git")
	res := syncDir(t, dir, Options{Retries: 2})
	if res.Category != string(core.CatNetwork) || res.Attempts != 3 {
		t.Errorf("category %s after %d attempts (%s)", res.Category, res.Attempts, res.Reason)
	}
}

func TestHostBreakerAndHostLimit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	var jobs []Job
	for i := range breakAfter + 3 {
		dir := filepath.Join(t.TempDir(), "r")
		git(t, ".", "init", "-q", dir)
		git(t, dir, "remote", "add", "origin", "http://127.0.0.1:9/x.git")
		jobs = append(jobs, Job{Repo: core.Repo{Path: string(rune('a' + i)), Host: "127.0.0.1"}, Dir: dir})
	}
	results := Run(context.Background(), runner(t), jobs,
		Options{Workers: 4, PerHostLimit: map[string]int{"127.0.0.1": 1}, RetryDelay: time.Millisecond}, nil)
	fast := 0
	for _, r := range results {
		if r.Attempts == 0 && r.Category == string(core.CatNetwork) {
			fast++
		}
	}
	if fast != 3 {
		t.Errorf("%d repos failed fast after the host went down, want 3", fast)
	}
}

func TestStopsStartingReposAfterCancel(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := Run(ctx, runner(t), []Job{{Repo: core.Repo{Path: "r"}, Dir: f.clone(t)}}, Options{}, nil)
	if results[0].Outcome != "" {
		t.Errorf("a repo started after cancel: %+v", results[0])
	}
}
