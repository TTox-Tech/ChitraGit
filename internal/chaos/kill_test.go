package chaos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/ui/cli"
)

// childEnv holds the CLI arguments, separated by \x1f, when this test
// binary runs as the child that gets killed.
const childEnv = "CHITRAGIT_CHAOS_CHILD"

func TestMain(m *testing.M) {
	if args := os.Getenv(childEnv); args != "" {
		os.Exit(cli.Execute(context.Background(), strings.Split(args, "\x1f"), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// git runs plain git for test setup and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t",
		"-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// upstream is one bare upstream repo and a dev clone that plays its
// author.
type upstream struct {
	t    *testing.T
	bare string
	dev  string
	n    int
}

func newUpstream(t *testing.T, name string) *upstream {
	u := &upstream{t: t, bare: filepath.Join(t.TempDir(), name+".git"), dev: filepath.Join(t.TempDir(), name)}
	git(t, ".", "init", "-q", "--bare", u.bare)
	git(t, ".", "clone", "-q", u.bare, u.dev)
	u.commit()
	u.push("main")
	return u
}

func (u *upstream) commit() {
	u.n++
	name := fmt.Sprintf("up-%d.txt", u.n)
	if err := os.WriteFile(filepath.Join(u.dev, name), []byte(name), 0o644); err != nil {
		u.t.Fatal(err)
	}
	git(u.t, u.dev, "add", name)
	git(u.t, u.dev, "commit", "-q", "-m", name)
}

func (u *upstream) push(args ...string) {
	git(u.t, u.dev, append([]string{"push", "-q", "origin"}, args...)...)
}

// change makes one random upstream change: new commits, a force-push that
// drops the last commit, or a branch created or deleted.
func (u *upstream) change(rng *rand.Rand) {
	switch rng.IntN(5) {
	case 0: // force-push: replace the tip
		if u.n > 1 {
			git(u.t, u.dev, "reset", "-q", "--hard", "HEAD~1")
		}
		u.commit()
		u.push("-f", "main")
	case 1:
		git(u.t, u.dev, "branch", "-f", "side", "HEAD")
		u.push("-f", "side")
	case 2:
		if git(u.t, u.dev, "ls-remote", "--heads", "origin", "side") != "" {
			u.push("--delete", "side")
		}
	default:
		for range 1 + rng.IntN(3) {
			u.commit()
		}
		u.push("main")
	}
}

// TestKilledSyncLosesNothing runs `chitragit sync` in a child process and
// kills it (the whole process tree) at a random moment, over and over,
// while upstream keeps changing: new commits, force-pushes and deleted
// branches. One repo has uncommitted work. Then a last sync runs to the
// end: no commit the archive ever held may be gone, every upstream tip
// must be there, the
// uncommitted work must be in place, no update may be left half done, and
// the operation log must verify.
//
// CHITRAGIT_CHAOS_KILLS sets the number of kills (default 10; the nightly
// job runs 1,000) and CHITRAGIT_CHAOS_SEED replays a run.
func TestKilledSyncLosesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("kills real processes; skipped with -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	kills := envInt(t, "CHITRAGIT_CHAOS_KILLS", 10)
	seed := uint64(envInt(t, "CHITRAGIT_CHAOS_SEED", int(time.Now().UnixNano()%1e9)))
	t.Logf("CHITRAGIT_CHAOS_SEED=%d CHITRAGIT_CHAOS_KILLS=%d", seed, kills)
	rng := rand.New(rand.NewPCG(seed, seed))

	home := t.TempDir()
	env := append(os.Environ(),
		"CHITRAGIT_CONFIG_DIR="+filepath.Join(home, "config"),
		"CHITRAGIT_CACHE_DIR="+filepath.Join(home, "cache"),
		"CHITRAGIT_NOTIFY_DESKTOP=false", "NO_COLOR=1")
	root := filepath.Join(t.TempDir(), "archive")
	chitragit := func(ctx context.Context, args ...string) (*exec.Cmd, *bytes.Buffer) {
		cmd := exec.CommandContext(ctx, os.Args[0])
		var out bytes.Buffer
		cmd.Env = append(slices.Clone(env), childEnv+"="+strings.Join(args, "\x1f"))
		cmd.Stdout, cmd.Stderr = &out, &out
		platform.KillTreeOnCancel(cmd)
		return cmd, &out
	}
	runToEnd := func(args ...string) (int, string) {
		cmd, out := chitragit(context.Background(), args...)
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), out.String()
		} else if err != nil {
			t.Fatal(err)
		}
		return 0, out.String()
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := runToEnd("init", root); code != 0 {
		t.Fatalf("init: %s", out)
	}
	ups := map[string]*upstream{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		u := newUpstream(t, name)
		ups[name] = u
		git(t, ".", "clone", "-q", u.bare, filepath.Join(root, "me", name))
	}
	work := filepath.Join(root, "me", "beta")
	mine := map[string]string{"mine.txt": "untracked work\n", "up-1.txt": "edited up-1.txt\n"}
	for name, content := range mine {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runToEnd("--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", out)
	}

	// Every commit the archive ever held must survive. A commit upstream
	// dropped before any sync fetched it was never the archive's to keep.
	held := map[string]map[string]bool{}
	note := func() {
		for name := range ups {
			if held[name] == nil {
				held[name] = map[string]bool{}
			}
			for sha := range reachable(t, filepath.Join(root, "me", name)) {
				held[name][sha] = true
			}
		}
	}
	// One whole sync sets the scale: kills land anywhere inside one.
	start := time.Now()
	if code, out := runToEnd("--archive", root, "sync"); code > 2 {
		t.Fatalf("first sync exit %d:\n%s", code, out)
	}
	span := max(time.Since(start), 50*time.Millisecond)
	note()
	killed := 0
	for i := range kills {
		for _, u := range ups {
			if rng.IntN(2) == 0 {
				u.change(rng)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cmd, out := chitragit(ctx, "--archive", root, "sync")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.AfterFunc(time.Duration(rng.Int64N(int64(span))), cancel)
		err := cmd.Wait()
		cancel()
		if ctx.Err() != nil && err != nil {
			killed++
		} else if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() > 3 {
			t.Fatalf("kill %d: sync failed on its own (exit %d):\n%s", i, ee.ExitCode(), out)
		}
		note()
	}
	t.Logf("%d of %d runs were killed mid-way", killed, kills)

	// git processes a killed sync started run in their own process groups
	// and finish on their own; wait for their locks to go.
	waitForLocks(t, root)
	// A sync that finds an interrupted update rolls it back and leaves the
	// repo for the next sync, so the last state needs two.
	for range 2 {
		if code, out := runToEnd("--archive", root, "sync"); code > 2 {
			t.Fatalf("final sync exit %d:\n%s", code, out)
		} else {
			t.Logf("final sync:\n%s", out)
		}
	}

	for name, u := range ups {
		repo := filepath.Join(root, "me", name)
		have := reachable(t, repo)
		for sha := range held[name] {
			if !have[sha] {
				t.Errorf("%s: commit %s the archive held is gone", name, sha)
			}
		}
		for _, tip := range strings.Fields(git(t, u.dev, "ls-remote", "origin")) {
			if len(tip) == 40 && !have[tip] {
				t.Errorf("%s: upstream tip %s missing after the final sync", name, tip)
			}
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "chitragit-update.json")); !os.IsNotExist(err) {
			t.Errorf("%s: an update was left half done: %v", name, err)
		}
	}
	for name, want := range mine {
		got, err := os.ReadFile(filepath.Join(work, name))
		if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("uncommitted %s = %q, %v; want %q (stashes: %s)", name, got, err, want, git(t, work, "stash", "list"))
		}
	}
	if code, out := runToEnd("--archive", root, "log", "--verify", "--limit", "1"); code != 0 {
		t.Errorf("operation log does not verify (exit %d):\n%s", code, out)
	}
}

// reachable lists every commit any ref of the repo reaches, ChitraGit's
// own refs included; the stash is left out, since an applied stash is
// meant to go.
func reachable(t *testing.T, repo string) map[string]bool {
	t.Helper()
	have := map[string]bool{}
	for _, sha := range strings.Fields(git(t, repo, "rev-list", "--exclude=refs/stash", "--all")) {
		have[sha] = true
	}
	return have
}

// waitForLocks waits until no git lock is left in the archive's repos.
// On Unix the git processes a killed sync started run in their own
// process groups and finish on their own. On Windows the tree kill stops
// them too, so a lock still there after a while is stale: a repo with one
// is skipped as busy, never touched, and the test clears it to go on.
func waitForLocks(t *testing.T, root string) {
	t.Helper()
	wait := 30 * time.Second
	if runtime.GOOS == "windows" {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		var locks []string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".lock") && strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) {
				locks = append(locks, path)
			}
			return nil
		})
		switch {
		case len(locks) == 0:
			return
		case time.Now().Before(deadline):
			time.Sleep(100 * time.Millisecond)
		case runtime.GOOS == "windows":
			t.Logf("removing locks left by killed git processes: %v", locks)
			for _, l := range locks {
				_ = os.Remove(l)
			}
			return
		default:
			t.Fatalf("git locks still held after a killed sync: %v", locks)
		}
	}
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q is not a number", name, v)
	}
	return n
}
