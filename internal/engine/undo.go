package engine

import (
	"context"
	"maps"
	"slices"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// UndoRepo is what undoing a run changes in one repo: the refs the run
// moved, put back to its pre snapshot (Snapshot). Kept lists refs the run
// moved that have moved again since; undo leaves those alone.
type UndoRepo struct {
	RestorePlan
	Kept  []string `json:"kept,omitempty"`
	Error string   `json:"error,omitempty"` // set by Undo when the repo was left as it was
}

// UndoPlan is what undoing sync run Run changes. Cloned lists repos the run
// cloned: undo never deletes a repo, so they stay.
type UndoPlan struct {
	Run    int64      `json:"run"`
	Repos  []UndoRepo `json:"repos"`
	Cloned []string   `json:"cloned,omitempty"`
}

// PlanUndo works out how to put back every ref sync run runID moved. Only
// refs still where the run left them move; anything changed since, such as
// a branch the user committed to, is kept. It changes nothing.
func PlanUndo(ctx context.Context, root string, runID int64, git *gitx.Runner, st *store.Store) (*UndoPlan, error) {
	pairs, err := st.RunChanges(ctx, runID)
	if err != nil {
		return nil, err
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]core.Repo, len(repos))
	for _, r := range repos {
		byID[r.ID] = r
	}
	p := &UndoPlan{Run: runID, Repos: []UndoRepo{}}
	for _, pair := range pairs {
		r, ok := byID[pair.RepoID]
		switch {
		case !ok: // gone from the archive
			continue
		case pair.From == 0:
			p.Cloned = append(p.Cloned, r.Path)
			continue
		}
		u := UndoRepo{RestorePlan: RestorePlan{RepoID: r.ID, Path: r.Path, Snapshot: pair.From, Moves: []RefMove{}}}
		if err := planUndoRepo(ctx, root, r, pair, &u, git, st); err != nil {
			u.Error = core.Message(err)
		}
		p.Repos = append(p.Repos, u)
	}
	return p, nil
}

func planUndoRepo(ctx context.Context, root string, r core.Repo, pair store.SnapshotPair, u *UndoRepo, git *gitx.Runner, st *store.Store) error {
	pre, err := st.Snapshot(ctx, pair.From)
	if err != nil {
		return err
	}
	post, err := st.Snapshot(ctx, pair.To)
	if err != nil {
		return err
	}
	now, err := syncer.Refs(ctx, git, RepoDir(root, r))
	if err != nil {
		return err
	}
	refs := slices.Concat(slices.Collect(maps.Keys(pre.Refs)), slices.Collect(maps.Keys(post.Refs)))
	slices.Sort(refs)
	for _, ref := range slices.Compact(refs) {
		before, after := pre.Refs[ref], post.Refs[ref]
		switch {
		case before == after:
		case now[ref] != after:
			u.Kept = append(u.Kept, ref)
		default:
			u.Moves = append(u.Moves, RefMove{Ref: ref, From: after, To: before})
		}
	}
	return nil
}

// Undo carries out a plan repo by repo through Restore, so each repo's refs
// from just before are snapshotted first and the undo can itself be undone
// with `restore`. A repo that fails is left as it was, with Error set, and
// the rest carry on. It returns how many repos failed.
func Undo(ctx context.Context, root string, p *UndoPlan, stash bool, git *gitx.Runner, st *store.Store) (failed int) {
	repos, err := st.Repos(ctx)
	byID := map[string]core.Repo{}
	for _, r := range repos {
		byID[r.ID] = r
	}
	for i := range p.Repos {
		u := &p.Repos[i]
		if u.Error != "" || len(u.Moves) == 0 {
			if u.Error != "" {
				failed++
			}
			continue
		}
		r, ok := byID[u.RepoID]
		switch {
		case err != nil:
			u.Error = core.Message(err)
		case !ok:
			u.Error = "no longer in the archive"
		default:
			if rerr := Restore(ctx, root, r, &u.RestorePlan, stash, git, st); rerr != nil {
				u.Error = core.Message(rerr)
			}
		}
		if u.Error != "" {
			failed++
		}
	}
	return failed
}
