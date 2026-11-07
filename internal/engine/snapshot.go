package engine

import (
	"context"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// TakenSnapshot is the result of a manual snapshot of one repo.
type TakenSnapshot struct {
	RepoID  string `json:"repo_id"`
	Path    string `json:"path"`
	ID      int64  `json:"snapshot,omitempty"`
	Created bool   `json:"created"` // false: nothing changed since snapshot ID
	Error   string `json:"error,omitempty"`
}

// TakeSnapshots records the current refs of each repo as a manual
// snapshot, pinning the objects first. A repo that fails is reported and
// the rest carry on.
func TakeSnapshots(ctx context.Context, root string, repos []core.Repo, git *gitx.Runner, st *store.Store) []TakenSnapshot {
	out := make([]TakenSnapshot, 0, len(repos))
	for _, r := range repos {
		t := TakenSnapshot{RepoID: r.ID, Path: r.Path}
		refs, err := syncer.Snapshot(ctx, git, RepoDir(root, r))
		if err == nil {
			t.ID, t.Created, err = st.TakeSnapshot(ctx, r.ID, refs)
		}
		if err != nil {
			t.Error = core.Message(err)
		}
		out = append(out, t)
	}
	return out
}
