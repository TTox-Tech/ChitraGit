// Package gitx runs the system git binary safely.
//
// Archived repositories are untrusted content: a cloned repo can carry
// config that executes commands (hooks, core.fsmonitor). Every invocation
// therefore goes through Runner, which adds hardening flags, disables
// interactive prompts, applies timeouts and classifies failures.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// Runner executes git commands. The zero value is not usable; call New.
type Runner struct {
	bin      string
	hooksDir string
	baseEnv  []string
	timeout  time.Duration
}

// Options configures a Runner.
type Options struct {
	// Bin is the git executable; defaults to "git" looked up in PATH.
	Bin string
	// HooksDir must be an empty directory; it becomes core.hooksPath.
	HooksDir string
	// Timeout applies to each command when the context has no deadline.
	// Zero means no extra timeout.
	Timeout time.Duration
}

// New finds git and prepares the hardened environment.
func New(opts Options) (*Runner, error) {
	bin := opts.Bin
	if bin == "" {
		bin = "git"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, &core.Error{
			Category: core.CatEnvironment,
			Message:  "git is not installed or not in PATH",
			Hint:     installHint(),
			Err:      err,
		}
	}
	if opts.HooksDir == "" {
		return nil, errors.New("gitx: HooksDir is required")
	}
	r := &Runner{bin: path, hooksDir: opts.HooksDir, timeout: opts.Timeout}
	r.baseEnv = hardenedEnv(os.Environ(), configuredSSHCommand(path))
	return r, nil
}

// Bin returns the resolved path of the git executable.
func (r *Runner) Bin() string { return r.bin }

// Result is the captured output of a git command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
}

// hardeningArgs are prepended to every git command.
func (r *Runner) hardeningArgs() []string {
	args := []string{
		"-c", "core.hooksPath=" + r.hooksDir, // never run hooks shipped inside archived repos
		"-c", "core.fsmonitor=false", // fsmonitor can be set to an arbitrary command
		"-c", "protocol.file.allow=user", // block file:// tricks via submodules
		"-c", "color.ui=false",
		"-c", "advice.detachedHead=false",
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-c", "core.longpaths=true")
	}
	return args
}

// Run executes git with args inside dir. A non-zero exit returns a
// *core.Error whose category is derived from git's stderr.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) (*Result, error) {
	return r.run(ctx, dir, "", args)
}

// RunInput is Run with stdin, for batch commands such as `update-ref --stdin`.
func (r *Runner) RunInput(ctx context.Context, dir, stdin string, args ...string) (*Result, error) {
	return r.run(ctx, dir, stdin, args)
}

func (r *Runner) run(ctx context.Context, dir, stdin string, args []string) (*Result, error) {
	if r.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, r.timeout)
			defer cancel()
		}
	}

	full := append(r.hardeningArgs(), args...)
	cmd := exec.CommandContext(ctx, r.bin, full...)
	cmd.Dir = dir
	platform.KillTreeOnCancel(cmd)
	cmd.Env = r.baseEnv
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Give git a moment to clean up lock files when cancelled.
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	err := cmd.Run()
	res := &Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(start)}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err == nil {
		return res, nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		cat := core.CatTimeout
		msg := "git took too long and was stopped"
		if errors.Is(ctxErr, context.Canceled) {
			cat, msg = core.CatInternal, "git was cancelled"
		}
		return res, &core.Error{Category: cat, Message: msg, Detail: commandLine(args), Err: ctxErr}
	}
	return res, classify(args, res.Stderr, err)
}

// Output runs git and returns trimmed stdout.
func (r *Runner) Output(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := r.Run(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func commandLine(args []string) string { return "git " + strings.Join(args, " ") }

// configuredSSHCommand returns the user's core.sshCommand from global or
// system git config, so that adding BatchMode does not discard it.
func configuredSSHCommand(bin string) string {
	cmd := exec.Command(bin, "config", "--get", "core.sshCommand")
	cmd.Dir = os.TempDir()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hardenedEnv returns environ with interactive prompts disabled and output
// made stable for parsing. User settings (proxy, SSH agent, HOME) are kept.
// configuredSSH is the user's core.sshCommand, if any.
func hardenedEnv(environ []string, configuredSSH string) []string {
	drop := map[string]bool{
		"GIT_TERMINAL_PROMPT": true, "GCM_INTERACTIVE": true, "GIT_OPTIONAL_LOCKS": true,
		"LC_ALL": true, "LANGUAGE": true, "GIT_SSH_COMMAND": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true,
	}
	env := make([]string, 0, len(environ)+8)
	ssh, hasGitSSH := "", false
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_SSH_COMMAND":
			ssh = v
		case "GIT_SSH":
			hasGitSSH = v != ""
		}
		if !drop[k] {
			env = append(env, kv)
		}
	}
	if ssh == "" {
		ssh = configuredSSH
	}
	if ssh == "" && !hasGitSSH {
		ssh = "ssh"
	}
	if ssh != "" {
		// BatchMode makes ssh fail instead of waiting for a password or a
		// host-key confirmation that nobody will type.
		env = append(env, "GIT_SSH_COMMAND="+ssh+" -o BatchMode=yes")
	}
	return append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_OPTIONAL_LOCKS=0", // read-only commands must not take index.lock
		"LC_ALL=C",             // stable, parseable English messages
		"LANGUAGE=C",
	)
}

func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install it with `xcode-select --install` or `brew install git`"
	case "windows":
		return "install Git for Windows from https://git-scm.com/download/win or `winget install Git.Git`"
	}
	return "install it with your package manager, e.g. `sudo apt install git` or `sudo dnf install git`"
}
