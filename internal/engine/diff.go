package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// How a ref changed between two points.
const (
	DiffAdded     = "added"
	DiffDeleted   = "deleted"
	DiffForward   = "forward"   // new commits on top, nothing dropped
	DiffBack      = "back"      // moved back to an older commit
	DiffRewritten = "rewritten" // history replaced, e.g. a force-push
	DiffMoved     = "moved"     // points at something other than a commit
)

// RefChange is one ref that differs between two points.
type RefChange struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	New     int    `json:"new_commits"`     // on To and not on From
	Dropped int    `json:"dropped_commits"` // on From and not on To
}

// RepoDiff is what changed in one repo between snapshot From and To
// (To 0: the repo's refs now).
type RepoDiff struct {
	RepoID  string      `json:"repo_id"`
	Path    string      `json:"path"`
	From    int64       `json:"from"`
	To      int64       `json:"to,omitempty"`
	Changes []RefChange `json:"changes"`

	// New counts commits any To ref reaches and no From ref reaches.
	// Removed counts the other way round: commits no ref reaches any more.
	// Snapshots keep those, so they can still be restored.
	New     int `json:"new_commits"`
	Removed int `json:"removed_commits"`
}

// DiffSnapshots compares snapshot from with snapshot to of repo r, or with
// the repo's refs now when to is 0. From 0 stands for no refs at all, as
// before a clone. It changes nothing.
func DiffSnapshots(ctx context.Context, root string, r core.Repo, from, to int64, git *gitx.Runner, st *store.Store) (*RepoDiff, error) {
	a, err := repoSnapshot(ctx, st, r, from)
	if err != nil {
		return nil, err
	}
	dir := RepoDir(root, r)
	var b map[string]string
	if to == 0 {
		b, err = syncer.Refs(ctx, git, dir)
	} else {
		b, err = repoSnapshot(ctx, st, r, to)
	}
	if err != nil {
		return nil, err
	}
	d, err := DiffRefs(ctx, git, dir, a, b)
	if err != nil {
		return nil, err
	}
	d.RepoID, d.Path, d.From, d.To = r.ID, r.Path, from, to
	return d, nil
}

func repoSnapshot(ctx context.Context, st *store.Store, r core.Repo, id int64) (map[string]string, error) {
	if id == 0 {
		return map[string]string{}, nil
	}
	sn, err := st.Snapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if sn.RepoID != r.ID {
		return nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("snapshot %d is of %s, not %s", id, sn.Path, r.Path),
			Hint: "list this repo's snapshots with `chitragit history " + r.Path + "`"}
	}
	return sn.Refs, nil
}

// DiffRun is what sync run runID changed: one diff per repo whose refs it
// moved, by path. Repos that are gone from the archive are skipped, and a
// repo that fails to diff is reported in errs while the rest carry on.
func DiffRun(ctx context.Context, root string, runID int64, git *gitx.Runner, st *store.Store) (diffs []RepoDiff, errs []string, err error) {
	pairs, err := st.RunChanges(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]core.Repo, len(repos))
	for _, r := range repos {
		byID[r.ID] = r
	}
	for _, p := range pairs {
		r, ok := byID[p.RepoID]
		if !ok {
			continue
		}
		d, err := DiffSnapshots(ctx, root, r, p.From, p.To, git, st)
		if err != nil {
			errs = append(errs, r.Path+": "+core.Message(err))
			continue
		}
		diffs = append(diffs, *d)
	}
	return diffs, errs, nil
}

// DiffRefs compares two ref sets of the repo in dir. Every object either
// side names must be in the repo, which pinning guarantees for snapshots.
func DiffRefs(ctx context.Context, git *gitx.Runner, dir string, from, to map[string]string) (*RepoDiff, error) {
	d := &RepoDiff{Changes: []RefChange{}}
	refs := slices.Concat(slices.Collect(maps.Keys(from)), slices.Collect(maps.Keys(to)))
	slices.Sort(refs)
	refs = slices.Compact(refs)
	var changed []RefChange
	for _, ref := range refs {
		if a, b := from[ref], to[ref]; a != b {
			changed = append(changed, RefChange{Ref: ref, From: a, To: b})
		}
	}
	if len(changed) == 0 {
		return d, nil
	}
	commit, err := peelCommits(ctx, git, dir, from, to)
	if err != nil {
		return nil, err
	}
	tips := func(refs map[string]string) []string {
		var out []string
		for _, sha := range refs {
			if c := commit[sha]; c != "" {
				out = append(out, c)
			}
		}
		return out
	}
	fromTips, toTips := tips(from), tips(to)
	for _, c := range changed {
		a, b := commit[c.From], commit[c.To]
		switch {
		case c.From == "":
			// A new ref counts what no old ref had, not its whole history.
			c.Kind = DiffAdded
			if b != "" {
				c.New, err = countNotIn(ctx, git, dir, []string{b}, fromTips)
			}
		case c.To == "":
			c.Kind = DiffDeleted
			if a != "" {
				c.Dropped, err = countNotIn(ctx, git, dir, []string{a}, toTips)
			}
		case a == "" || b == "":
			c.Kind = DiffMoved
		default:
			c.Dropped, c.New, err = countBetween(ctx, git, dir, a, b)
			switch {
			case c.Dropped == 0:
				c.Kind = DiffForward
			case c.New == 0:
				c.Kind = DiffBack
			default:
				c.Kind = DiffRewritten
			}
		}
		if err != nil {
			return nil, err
		}
		d.Changes = append(d.Changes, c)
	}
	if d.New, err = countNotIn(ctx, git, dir, toTips, fromTips); err != nil {
		return nil, err
	}
	if d.Removed, err = countNotIn(ctx, git, dir, fromTips, toTips); err != nil {
		return nil, err
	}
	return d, nil
}

// peelCommits maps every object the two sets name to the commit it is or
// points at (a tag), in one git call. Objects that are no commit, or are
// missing, map to "".
func peelCommits(ctx context.Context, git *gitx.Runner, dir string, sets ...map[string]string) (map[string]string, error) {
	var shas []string
	for _, set := range sets {
		shas = append(shas, slices.Collect(maps.Values(set))...)
	}
	slices.Sort(shas)
	shas = slices.Compact(shas)
	var in strings.Builder
	for _, sha := range shas {
		in.WriteString(sha + "^{commit}\n")
	}
	out, err := git.RunInput(ctx, dir, in.String(), "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(out.Stdout), "\n"), "\n")
	if len(lines) != len(shas) {
		return nil, fmt.Errorf("cat-file: %d answers for %d objects", len(lines), len(shas))
	}
	peeled := make(map[string]string, len(shas))
	for i, sha := range shas {
		if l := lines[i]; !strings.HasSuffix(l, " missing") && !strings.Contains(l, " ") {
			peeled[sha] = l
		}
	}
	return peeled, nil
}

// countBetween returns how many commits only a has and only b has.
func countBetween(ctx context.Context, git *gitx.Runner, dir, a, b string) (onlyA, onlyB int, err error) {
	out, err := git.Output(ctx, dir, "rev-list", "--left-right", "--count", a+"..."+b)
	if err != nil {
		return 0, 0, err
	}
	l, r, _ := strings.Cut(strings.TrimSpace(out), "\t")
	if onlyA, err = strconv.Atoi(l); err == nil {
		onlyB, err = strconv.Atoi(r)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("rev-list: unexpected output %q", out)
	}
	return onlyA, onlyB, nil
}

// countNotIn counts commits reachable from tips and from none of not.
func countNotIn(ctx context.Context, git *gitx.Runner, dir string, tips, not []string) (int, error) {
	if len(tips) == 0 {
		return 0, nil
	}
	var in strings.Builder
	for _, t := range tips {
		in.WriteString(t + "\n")
	}
	for _, t := range not {
		in.WriteString("^" + t + "\n")
	}
	out, err := git.RunInput(ctx, dir, in.String(), "rev-list", "--count", "--stdin")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out.Stdout)))
	if err != nil {
		return 0, fmt.Errorf("rev-list: unexpected output %q", out.Stdout)
	}
	return n, nil
}
