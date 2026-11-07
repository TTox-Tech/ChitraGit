package syncer

import (
	"context"
	"maps"
	"slices"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// RefMove is one ref a restore changes. From is empty for a ref the
// restore creates, To is empty for one it deletes.
type RefMove struct {
	Ref  string `json:"ref"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// PlanRestore compares the repo's refs now with target (a snapshot's refs)
// and returns the moves that make them equal, sorted by ref, plus the refs
// now. It changes nothing.
func PlanRestore(ctx context.Context, git *gitx.Runner, dir string, target map[string]string) (moves []RefMove, now map[string]string, err error) {
	now, _, err = readRefs(ctx, git, dir)
	if err != nil {
		return nil, nil, err
	}
	refs := append(slices.Collect(maps.Keys(now)), slices.Collect(maps.Keys(target))...)
	slices.Sort(refs)
	for _, ref := range slices.Compact(refs) {
		if from, to := now[ref], target[ref]; from != to {
			moves = append(moves, RefMove{Ref: ref, From: from, To: to})
		}
	}
	return moves, now, nil
}
