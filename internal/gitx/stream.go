package gitx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// Stream is a running git command whose stdout is read as it is produced.
// It is for commands whose output can be far larger than memory, such as
// `git log -p` over a repo's whole history.
//
// Read stdout until EOF (or stop early), then call Close: it waits for git
// and returns the same classified errors as Run.
type Stream struct {
	stdout io.ReadCloser
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	args   []string

	mu     sync.Mutex
	stderr bytes.Buffer

	closeOnce sync.Once
	closeErr  error
}

// Stream starts git with args inside dir, with the same hardening as Run.
// stdin, when not empty, is written to the command's standard input.
func (r *Runner) Stream(ctx context.Context, dir, stdin string, args ...string) (*Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	if r.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			cancel()
			ctx, cancel = context.WithTimeout(ctx, r.timeout)
		}
	}
	full := append(r.hardeningArgs(), args...)
	cmd := exec.CommandContext(ctx, r.bin, full...) // nosemgrep: dangerous-exec-command
	cmd.Dir = dir
	platform.KillTreeOnCancel(cmd)
	cmd.Env = r.baseEnv
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.WaitDelay = 5 * time.Second
	s := &Stream{cmd: cmd, ctx: ctx, cancel: cancel, args: args}
	cmd.Stderr = lockedWriter{&s.mu, &s.stderr}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	s.stdout = out
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, &core.Error{Category: core.CatEnvironment, Message: "cannot start git", Detail: commandLine(args), Err: err}
	}
	return s, nil
}

// Read reads git's standard output.
func (s *Stream) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Close stops reading, waits for git and reports how it ended. Closing
// before EOF stops git; that is not reported as an error.
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		// Drain nothing: closing the pipe makes a still-writing git exit
		// with SIGPIPE, which we treat as a deliberate early stop.
		early := false
		if _, err := s.stdout.Read(make([]byte, 1)); err == nil {
			early = true
			s.cancel()
		}
		err := s.cmd.Wait()
		defer s.cancel()
		if err == nil || early {
			return
		}
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			cat, msg := core.CatTimeout, "git took too long and was stopped"
			if errors.Is(ctxErr, context.Canceled) {
				cat, msg = core.CatInternal, "git was cancelled"
			}
			s.closeErr = &core.Error{Category: cat, Message: msg, Detail: commandLine(s.args), Err: ctxErr}
			return
		}
		s.mu.Lock()
		stderr := bytes.Clone(s.stderr.Bytes())
		s.mu.Unlock()
		s.closeErr = classify(s.args, stderr, err)
	})
	return s.closeErr
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
