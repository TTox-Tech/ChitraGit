package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

// NamedSnapshot is the result of naming one repo's refs now.
type NamedSnapshot struct {
	RepoID string `json:"repo_id"`
	Path   string `json:"path"`
	ID     int64  `json:"snapshot,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CheckSnapshotName refuses names that could be mistaken for a snapshot
// ID or a flag, or that are awkward to type: a name is 1 to 100 letters,
// digits, '.', '_' or '-', starts with a letter or digit, and is not only
// digits.
func CheckSnapshotName(name string) error {
	bad := name == "" || len(name) > 100 || strings.Trim(name, "0123456789") == ""
	for i, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && strings.ContainsRune("._-", c)
		bad = bad || !ok
	}
	if bad {
		return &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%q cannot name a snapshot", name),
			Hint: "use letters, digits, '.', '_' and '-', not only digits, e.g. pre-move or 2026-11-27"}
	}
	return nil
}

// NameSnapshots gives each repo's refs now the name, pinning the objects
// first, so the name can be passed to restore and diff later. A repo that
// fails is reported and the rest carry on.
func NameSnapshots(ctx context.Context, root, name string, repos []core.Repo, git *gitx.Runner, st *store.Store) ([]NamedSnapshot, error) {
	if err := CheckSnapshotName(name); err != nil {
		return nil, err
	}
	out := make([]NamedSnapshot, 0, len(repos))
	for _, r := range repos {
		n := NamedSnapshot{RepoID: r.ID, Path: r.Path}
		refs, err := syncer.Snapshot(ctx, git, RepoDir(root, r))
		if err == nil {
			n.ID, err = st.NameSnapshot(ctx, r.ID, name, refs)
		}
		if err != nil {
			n.Error = core.Message(err)
		}
		out = append(out, n)
	}
	return out, nil
}

// SnapshotID turns what a user typed for repo r's snapshot, an ID or a
// name from `tag`, into the snapshot ID.
func SnapshotID(ctx context.Context, st *store.Store, r core.Repo, arg string) (int64, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		if id <= 0 {
			return 0, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%q is not a snapshot ID", arg)}
		}
		return id, nil
	}
	if err := CheckSnapshotName(arg); err != nil {
		return 0, err
	}
	named, err := st.NamedSnapshots(ctx, arg)
	if err != nil {
		return 0, err
	}
	id, ok := named[r.ID]
	switch {
	case ok:
		return id, nil
	case len(named) == 0:
		return 0, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no snapshot is named %q", arg),
			Hint: "list the names with `chitragit tag -l`"}
	}
	return 0, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%s has no snapshot named %q", r.Path, arg),
		Hint: "the name was given while this repo was left out; see `chitragit history " + r.Path + "`"}
}
