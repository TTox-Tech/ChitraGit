package cli

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/conflict"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/ui/tui"
)

// The Conflict Screen's backend. Every call takes the archive lock for
// its own duration only: a session lives in the repo (its marker), not in
// this process, so nothing is held between key presses.

func (b *localBackend) Conflicts(ctx context.Context) ([]store.Conflict, error) {
	return engine.Conflicts(ctx, b.sess().st)
}

type repoOp func(git *gitx.Runner, repo core.Repo, dir string) error

func (b *localBackend) inRepo(ctx context.Context, repoID string, fn repoOp) error {
	git, err := b.runner(ctx)
	if err != nil {
		return err
	}
	l, err := b.lock()
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()
	repo, dir, err := engine.FindRepo(ctx, b.sess().arc.Root, repoID, b.sess().st)
	if err != nil {
		return err
	}
	return fn(git, repo, dir)
}

// inSession runs fn on the repo's open session and returns its new state.
func (b *localBackend) inSession(ctx context.Context, repoID string, fn func(*conflict.Session) error) (*tui.ConflictState, error) {
	var st *tui.ConflictState
	err := b.inRepo(ctx, repoID, func(git *gitx.Runner, repo core.Repo, dir string) error {
		s, err := engine.OpenResolve(ctx, dir, git)
		if err != nil {
			return err
		}
		ferr := fn(s)
		// Show the state even when fn failed, e.g. markers left after an edit.
		st, err = conflictState(ctx, repo, s)
		return errors.Join(ferr, err)
	})
	return st, err
}

func conflictState(ctx context.Context, repo core.Repo, s *conflict.Session) (*tui.ConflictState, error) {
	files, err := s.Files(ctx)
	if err != nil {
		return nil, err
	}
	st := &tui.ConflictState{Repo: repo, Files: files, Hunks: map[string][]conflict.Hunk{}}
	for _, f := range files {
		if !f.Resolved && f.Hunks > 0 {
			hs, err := s.Hunks(ctx, f.Path)
			if err != nil {
				return nil, err
			}
			st.Hunks[f.Path] = hs
		}
	}
	return st, nil
}

func (b *localBackend) ConflictOpen(ctx context.Context, repoID string, start bool) (*tui.ConflictState, error) {
	var st *tui.ConflictState
	err := b.inRepo(ctx, repoID, func(git *gitx.Runner, repo core.Repo, dir string) error {
		var s *conflict.Session
		var err error
		if start {
			s, err = engine.StartResolve(ctx, repo, dir, git, b.sess().st)
		} else {
			s, err = engine.OpenResolve(ctx, dir, git)
		}
		if errors.Is(err, conflict.ErrNothingToResolve) || (err == nil && s.Done) {
			st = &tui.ConflictState{Repo: repo, Done: true}
			return nil
		}
		if err != nil {
			return err
		}
		st, err = conflictState(ctx, repo, s)
		return err
	})
	return st, err
}

func (b *localBackend) ConflictResolve(ctx context.Context, repoID, path string, hunk int, c conflict.Choice) (*tui.ConflictState, error) {
	return b.inSession(ctx, repoID, func(s *conflict.Session) error {
		if hunk < 0 {
			return s.ResolveFile(ctx, path, c)
		}
		return s.ResolveHunk(ctx, path, hunk, c)
	})
}

func (b *localBackend) ConflictMarkResolved(ctx context.Context, repoID, path string) (*tui.ConflictState, error) {
	return b.inSession(ctx, repoID, func(s *conflict.Session) error { return s.MarkResolved(ctx, path) })
}

func (b *localBackend) ConflictFinish(ctx context.Context, repoID string) error {
	return b.inRepo(ctx, repoID, func(git *gitx.Runner, repo core.Repo, dir string) error {
		s, err := engine.OpenResolve(ctx, dir, git)
		if err != nil {
			return err
		}
		return engine.FinishResolve(ctx, repo, s, b.sess().st)
	})
}

func (b *localBackend) ConflictAbort(ctx context.Context, repoID string) error {
	return b.inRepo(ctx, repoID, func(git *gitx.Runner, repo core.Repo, dir string) error {
		_, err := engine.AbortResolve(ctx, repo, dir, git, b.sess().st)
		return err
	})
}

func (b *localBackend) ConflictDismiss(ctx context.Context, repoID string) error {
	return b.inRepo(ctx, repoID, func(git *gitx.Runner, repo core.Repo, dir string) error {
		return engine.DismissConflict(ctx, repo, dir, git, b.sess().st)
	})
}

func (b *localBackend) ConflictEditor(ctx context.Context, repoID, path string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	err := b.inRepo(ctx, repoID, func(git *gitx.Runner, _ core.Repo, dir string) error {
		s, err := engine.OpenResolve(ctx, dir, git)
		if err != nil {
			return err
		}
		files, err := s.Files(ctx)
		if err != nil {
			return err
		}
		// Only a file of the session: the path comes from the screen.
		if !slices.ContainsFunc(files, func(f conflict.File) bool { return f.Path == path }) {
			return &core.Error{Category: core.CatConfig, Message: path + " is not one of the conflicted files"}
		}
		editor := findEditor()
		if editor == "" {
			return &core.Error{Category: core.CatEnvironment, Message: i18n.T("tui.conflicts.no_editor")}
		}
		fields := strings.Fields(editor)
		cmd = exec.CommandContext(ctx, fields[0], append(fields[1:], filepath.Join(dir, filepath.FromSlash(path)))...)
		return nil
	})
	return cmd, err
}

var _ tui.ConflictBackend = (*localBackend)(nil)
