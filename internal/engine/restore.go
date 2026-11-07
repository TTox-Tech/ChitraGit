package engine

import (
	"context"
	"fmt"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// RefMove is one ref a restore changes.
type RefMove = syncer.RefMove

// RestorePlan is what restoring one repo to a snapshot changes.
type RestorePlan struct {
	RepoID   string    `json:"repo_id"`
	Path     string    `json:"path"`
	Snapshot int64     `json:"snapshot"`
	Pre      int64     `json:"pre_snapshot,omitempty"` // the refs just before the restore
	Moves    []RefMove `json:"moves"`
}

// PlanRestore lists the ref moves that would put repo r back to snapshot
// id. It changes nothing.
func PlanRestore(ctx context.Context, root string, r core.Repo, id int64, git *gitx.Runner, st *store.Store) (*RestorePlan, error) {
	sn, err := st.Snapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if sn.RepoID != r.ID {
		return nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("snapshot %d is of %s, not %s", id, sn.Path, r.Path),
			Hint: "list this repo's snapshots with `chitragit history " + r.Path + "`"}
	}
	moves, _, err := syncer.PlanRestore(ctx, git, RepoDir(root, r), sn.Refs)
	if err != nil {
		return nil, err
	}
	return &RestorePlan{RepoID: r.ID, Path: r.Path, Snapshot: id, Moves: moves}, nil
}

// Restore carries out a plan; stash lets it put uncommitted changes aside
// and back (syncer.ApplyRestore). It first records the repo's current refs as
// a snapshot, pinned against gc, so the restore itself can be undone by
// restoring that one; then it moves the refs and logs a "restored" event.
func Restore(ctx context.Context, root string, r core.Repo, p *RestorePlan, stash bool, git *gitx.Runner, st *store.Store) error {
	if len(p.Moves) == 0 {
		return nil
	}
	dir := RepoDir(root, r)
	refs, err := syncer.Snapshot(ctx, git, dir)
	if err != nil {
		return err
	}
	if p.Pre, _, err = st.TakeSnapshot(ctx, r.ID, refs); err != nil {
		return err
	}
	if err := syncer.ApplyRestore(ctx, git, dir, p.Moves, stash); err != nil {
		return err
	}
	return st.AddEvent(ctx, r.ID, "restored", map[string]any{"snapshot": p.Snapshot, "pre": p.Pre, "refs": len(p.Moves),
		"message": fmt.Sprintf("restored to snapshot %d (%d refs); snapshot %d has the refs from before", p.Snapshot, len(p.Moves), p.Pre)})
}
