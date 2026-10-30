package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/conflict"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// Conflicts lists repos whose local changes clash with an update.
func Conflicts(ctx context.Context, st *store.Store) ([]store.Conflict, error) {
	return st.Conflicts(ctx)
}

// FindRepo resolves a repo the way store.FindRepo does, and also from a
// folder on disk inside the archive (such as "."). It returns the repo and
// its folder.
func FindRepo(ctx context.Context, root, arg string, st *store.Store) (core.Repo, string, error) {
	q := arg
	if abs, err := filepath.Abs(arg); err == nil {
		if _, serr := os.Stat(abs); serr == nil {
			if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				q = filepath.ToSlash(rel)
			}
		}
	}
	r, err := st.FindRepo(ctx, q)
	if err != nil {
		return core.Repo{}, "", err
	}
	return r, filepath.Join(root, filepath.FromSlash(r.Path)), nil
}

// StartResolve opens a conflict-resolution session in a repo (PRD F4).
// When nothing clashes any more, the update is applied at once and the
// returned session is Done.
func StartResolve(ctx context.Context, repo core.Repo, dir string, git *gitx.Runner, st *store.Store) (*conflict.Session, error) {
	s, err := conflict.Start(ctx, git, dir)
	if errors.Is(err, conflict.ErrNothingToResolve) {
		_ = st.ClearConflict(ctx, repo.ID, syncer.StatusOK, "")
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if s.Done {
		return s, st.ClearConflict(ctx, repo.ID, syncer.StatusOK, "applied the update and kept your local changes")
	}
	files, err := s.Files(ctx)
	if err != nil {
		return s, err
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return s, st.SetConflict(ctx, repo.ID, syncer.StatusResolving,
		"resolving conflicts; finish or abort with `chitragit conflicts`", paths, s.BackupRef(), s.StashName())
}

// OpenResolve returns the session open in a repo, or an error if none is.
func OpenResolve(ctx context.Context, dir string, git *gitx.Runner) (*conflict.Session, error) {
	s, err := conflict.Open(ctx, git, dir)
	if err == nil && s == nil {
		err = &core.Error{Category: core.CatConfig, Message: "no conflict resolution is open in this repo",
			Hint: "start one with `chitragit conflicts start <repo>`"}
	}
	return s, err
}

// FinishResolve ends a session whose files are all resolved.
func FinishResolve(ctx context.Context, repo core.Repo, s *conflict.Session, st *store.Store) error {
	if err := s.Finish(ctx); err != nil {
		return err
	}
	return st.ClearConflict(ctx, repo.ID, syncer.StatusOK, "conflicts resolved; your changes are kept as uncommitted changes")
}

// AbortResolve puts a repo back as it was before its session (or before an
// interrupted update). The conflict stays listed so it can be tried again.
func AbortResolve(ctx context.Context, repo core.Repo, dir string, git *gitx.Runner, st *store.Store) (bool, error) {
	ok, err := conflict.Abort(ctx, git, dir)
	if err != nil || !ok {
		return ok, err
	}
	return ok, st.EndConflictSession(ctx, repo.ID, syncer.StatusConflict,
		"conflict resolution aborted; the repo is exactly as it was and the update is downloaded")
}

// DismissConflict takes a repo off the conflict list without touching it.
// The repo already is as it was; the next sync tries the update again.
func DismissConflict(ctx context.Context, repo core.Repo, dir string, git *gitx.Runner, st *store.Store) error {
	if s, err := conflict.Open(ctx, git, dir); err != nil {
		return err
	} else if s != nil {
		return &core.Error{Category: core.CatConfig, Message: "a conflict resolution is open in this repo",
			Hint: "finish it, or abort it with `chitragit conflicts abort <repo>`"}
	}
	return st.ClearConflict(ctx, repo.ID, syncer.StatusDirty, "conflict dismissed; your changes are untouched and the update is downloaded but not applied")
}
