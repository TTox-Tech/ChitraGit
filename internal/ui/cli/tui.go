package cli

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/syncer"
	"github.com/TTox-Tech/chitragit/internal/ui/tui"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// interactive reports whether the TUI can start: both ends of the
// terminal must be a TTY, and the user did not ask for JSON.
func (a *app) interactive() bool {
	return !a.jsonOut && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// runTUI opens the archive read-only and hands it to the TUI. Writes take
// the archive lock only while a sync runs, so browsing never blocks a
// scheduled run.
func (a *app) runTUI(ctx context.Context) error {
	s, err := a.open(ctx, "", false)
	if err != nil {
		return err
	}
	defer s.close()
	t := platform.DetectTerminal()
	t.NoColor = t.NoColor || a.noColor
	return tui.Run(ctx, tui.Options{
		Backend:  &localBackend{s: s},
		Config:   s.cfg,
		Archive:  s.arc.Root,
		Terminal: t,
		Stderr:   a.errOut,
	})
}

// localBackend serves the TUI from an archive on this machine.
type localBackend struct {
	s *session

	mu  sync.Mutex
	git *gitx.Runner
}

func (b *localBackend) Repos(ctx context.Context) ([]core.Repo, error) { return b.s.st.Repos(ctx) }

// runner finds git and checks its version once, on first use.
func (b *localBackend) runner(ctx context.Context) (*gitx.Runner, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.git != nil {
		return b.git, nil
	}
	hooks, err := platform.EmptyHooksDir()
	if err != nil {
		return nil, err
	}
	git, err := gitx.New(gitx.Options{HooksDir: hooks})
	if err != nil {
		return nil, err
	}
	if _, err := git.CheckVersion(ctx); err != nil {
		return nil, err
	}
	b.git = git
	return git, nil
}

// lock takes the archive lock for one operation.
func (b *localBackend) lock() (*archive.Lock, error) {
	return b.s.arc.Acquire("chitragit (interactive)")
}

func (b *localBackend) Interrupted(ctx context.Context) (*tui.Interrupted, error) {
	// A run marked "running" is only dead if nobody holds the lock.
	l, err := b.lock()
	var locked *archive.LockedError
	if errors.As(err, &locked) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer l.Release()
	run, err := b.s.st.LastUnfinishedRun(ctx)
	if err != nil || run == nil {
		return nil, err
	}
	started, _ := time.Parse(time.RFC3339Nano, run.StartedAt)
	return &tui.Interrupted{RunID: run.ID, StartedAt: started, Total: run.Total, Pending: len(run.Pending)}, nil
}

func (b *localBackend) Discard(ctx context.Context, runID int64) error {
	l, err := b.lock()
	if err != nil {
		return err
	}
	defer l.Release()
	return b.s.st.SetRunState(ctx, runID, "discarded")
}

func (b *localBackend) Sync(ctx context.Context, req tui.SyncStart, listen func(tui.SyncUpdate)) (*engine.SyncReport, error) {
	git, err := b.runner(ctx)
	if err != nil {
		return nil, err
	}
	l, err := b.lock()
	if err != nil {
		return nil, err
	}
	defer l.Release()

	rep, err := engine.Scan(ctx, b.s.arc.Root, b.s.scanOptions(), git, b.s.st)
	if err != nil {
		return nil, err
	}
	listen(tui.SyncUpdate{Scanned: rep})

	opts := engine.SyncOptions(b.s.cfg.Sync)
	opts.Gate = req.Gate
	onInterrupted := req.OnInterrupted
	if onInterrupted == "" {
		onInterrupted = b.s.cfg.Sync.OnInterrupted
	}
	return engine.Sync(ctx, engine.SyncRequest{
		Root: b.s.arc.Root, IDs: req.IDs, OnInterrupted: onInterrupted, Options: opts,
		Listen:  func(e syncer.Event) { listen(tui.SyncUpdate{Event: &e}) },
		Planned: func(n int) { listen(tui.SyncUpdate{Planned: &n}) },
	}, git, b.s.st)
}
