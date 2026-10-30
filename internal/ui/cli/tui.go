package cli

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/config"
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
// scheduled run. With no archive yet, or an archive that was never
// scanned, the first-run wizard opens first (PRD F16).
func (a *app) runTUI(ctx context.Context) error {
	b := &localBackend{a: a}
	defer func() {
		if b.s != nil {
			b.s.close()
		}
	}()
	s, err := a.open(ctx, "", false)
	firstRun := false
	switch {
	case err == nil:
		b.s = s
		firstRun, err = neverScanned(ctx, s)
		if err != nil {
			return err
		}
	case errors.Is(err, archive.ErrNotFound) && a.archivePath == "":
		firstRun = true
	default:
		return err
	}
	firstRun = firstRun && !a.noWizard
	cfg := (*config.Config)(nil)
	if b.s != nil {
		cfg = b.s.cfg
	} else if cfg, _, err = a.loadConfig(nil); err != nil {
		return err
	}
	if b.s == nil && !firstRun {
		return archive.ErrNotFound
	}
	wd, _ := os.Getwd()
	t := platform.DetectTerminal()
	t.NoColor = t.NoColor || a.noColor
	root := ""
	if b.s != nil {
		root = b.s.arc.Root
	}
	return tui.Run(ctx, tui.Options{
		Backend:    b,
		Setup:      b,
		Config:     cfg,
		Archive:    root,
		Terminal:   t,
		Stderr:     a.errOut,
		FirstRun:   firstRun,
		StartDir:   wd,
		Accessible: a.accessible || cfg.UI.Accessible,
	})
}

// neverScanned reports whether an archive has no repos and no sync yet.
func neverScanned(ctx context.Context, s *session) (bool, error) {
	repos, err := s.st.Repos(ctx)
	if err != nil || len(repos) > 0 {
		return false, err
	}
	last, err := s.st.LastRun(ctx)
	return last == nil, err
}

// localBackend serves the TUI from an archive on this machine. Before the
// wizard creates an archive, s is nil and the archive looks empty.
type localBackend struct {
	a *app

	smu sync.RWMutex
	s   *session // replaced when the wizard opens a new archive

	mu  sync.Mutex
	git *gitx.Runner
}

func (b *localBackend) sess() *session {
	b.smu.RLock()
	defer b.smu.RUnlock()
	return b.s
}

func (b *localBackend) Repos(ctx context.Context) ([]core.Repo, error) {
	if b.sess() == nil {
		return nil, nil
	}
	return b.sess().st.Repos(ctx)
}

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
	return b.sess().arc.Acquire("chitragit (interactive)")
}

func (b *localBackend) Interrupted(ctx context.Context) (*tui.Interrupted, error) {
	if b.sess() == nil {
		return nil, nil
	}
	// A run marked "running" is only dead if nobody holds the lock.
	l, err := b.lock()
	var locked *archive.LockedError
	if errors.As(err, &locked) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Release() }()
	run, err := b.sess().st.LastUnfinishedRun(ctx)
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
	defer func() { _ = l.Release() }()
	return b.sess().st.SetRunState(ctx, runID, "discarded")
}

func (b *localBackend) Sync(ctx context.Context, req tui.SyncStart, listen func(tui.SyncUpdate)) (*engine.SyncReport, error) {
	if b.sess() == nil {
		return nil, archive.ErrNotFound
	}
	git, err := b.runner(ctx)
	if err != nil {
		return nil, err
	}
	l, err := b.lock()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Release() }()

	rep, err := engine.Scan(ctx, b.sess().arc.Root, b.sess().scanOptions(), git, b.sess().st)
	if err != nil {
		return nil, err
	}
	listen(tui.SyncUpdate{Scanned: rep})

	opts := engine.SyncOptions(b.sess().cfg.Sync)
	opts.Gate = req.Gate
	onInterrupted := req.OnInterrupted
	if onInterrupted == "" {
		onInterrupted = b.sess().cfg.Sync.OnInterrupted
	}
	return engine.Sync(ctx, engine.SyncRequest{
		Root: b.sess().arc.Root, IDs: req.IDs, OnInterrupted: onInterrupted, Options: opts,
		Listen:  func(e syncer.Event) { listen(tui.SyncUpdate{Event: &e}) },
		Planned: func(n int) { listen(tui.SyncUpdate{Planned: &n}) },
	}, git, b.sess().st)
}
