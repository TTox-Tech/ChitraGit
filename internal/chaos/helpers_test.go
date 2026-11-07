package chaos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/ui/cli"
)

// childEnv holds the CLI arguments, separated by \x1f, when this test
// binary runs as a child chitragit process.
const childEnv = "CHITRAGIT_CHAOS_CHILD"

func TestMain(m *testing.M) {
	if args := os.Getenv(childEnv); args != "" {
		os.Exit(cli.Execute(context.Background(), strings.Split(args, "\x1f"), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// chitragit runs the CLI as a child process (this test binary), with its
// own config and cache folders, so a test can kill it.
type chitragit struct {
	t   *testing.T
	env []string
}

func newCLI(t *testing.T) *chitragit {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	return &chitragit{t: t, env: append(os.Environ(),
		"CHITRAGIT_CONFIG_DIR="+filepath.Join(home, "config"),
		"CHITRAGIT_CACHE_DIR="+filepath.Join(home, "cache"),
		"CHITRAGIT_NOTIFY_DESKTOP=false", "NO_COLOR=1")}
}

// start prepares a child that is killed, with its whole process tree,
// when ctx is cancelled.
func (c *chitragit) start(ctx context.Context, args ...string) (*exec.Cmd, *bytes.Buffer) {
	cmd := exec.CommandContext(ctx, os.Args[0])
	var out bytes.Buffer
	cmd.Env = append(slices.Clone(c.env), childEnv+"="+strings.Join(args, "\x1f"))
	cmd.Stdout, cmd.Stderr = &out, &out
	platform.KillTreeOnCancel(cmd)
	return cmd, &out
}

// run runs the CLI to the end and returns its exit code and output.
func (c *chitragit) run(args ...string) (int, string) {
	c.t.Helper()
	cmd, out := c.start(context.Background(), args...)
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), out.String()
	} else if err != nil {
		c.t.Fatal(err)
	}
	return 0, out.String()
}

func (c *chitragit) mustRun(args ...string) string {
	c.t.Helper()
	code, out := c.run(args...)
	if code != 0 {
		c.t.Fatalf("chitragit %v: exit %d\n%s", args, code, out)
	}
	return out
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
