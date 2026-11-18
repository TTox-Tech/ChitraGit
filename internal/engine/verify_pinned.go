package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// CheckPinned is the verify check that every object a snapshot points at
// is still in its repo, and still pinned under refs/chitragit/snap/ so git
// gc cannot remove it (CG-07.2). Without the object, restoring that
// snapshot is impossible.
const CheckPinned = "pinned"

// snapRefPrefix is where the syncer pins snapshot objects (see syncer/snap.go).
const snapRefPrefix = "refs/chitragit/snap/"

// verifyWorkers is how many repos verify reads at once.
const verifyWorkers = 4

// shown is how many SHAs a problem line names before "and N more".
const shown = 3

func verifyPinned(ctx context.Context, root string, git *gitx.Runner, st *store.Store) (VerifyCheck, error) {
	c := VerifyCheck{Name: CheckPinned}
	objects, err := st.SnapshotObjects(ctx)
	if err != nil {
		return c, err
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		return c, err
	}
	var todo []core.Repo
	total, skipped := 0, 0
	for _, r := range repos {
		if len(objects[r.ID]) == 0 {
			continue
		}
		if r.Missing {
			skipped++ // not on disk at its path; scan already reports it
			continue
		}
		todo = append(todo, r)
		total += len(objects[r.ID])
	}

	var mu sync.Mutex
	jobs := make(chan core.Repo)
	var wg sync.WaitGroup
	for range min(verifyWorkers, len(todo)) {
		wg.Go(func() {
			for r := range jobs {
				probs := pinnedProblems(ctx, git, filepath.Join(root, filepath.FromSlash(r.Path)), r.Path, objects[r.ID])
				mu.Lock()
				c.Problems = append(c.Problems, probs...)
				mu.Unlock()
			}
		})
	}
	for _, r := range todo {
		if ctx.Err() != nil {
			break
		}
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return c, err
	}
	sort.Strings(c.Problems)

	c.OK = len(c.Problems) == 0
	switch {
	case !c.OK:
		c.Summary = fmt.Sprintf("%d problems with the objects snapshots point at", len(c.Problems))
	case total == 0:
		c.Summary = "no snapshots yet"
	default:
		c.Summary = fmt.Sprintf("%d snapshot objects in %d repos, all present and pinned", total, len(todo))
	}
	if skipped > 0 {
		c.Summary += fmt.Sprintf(" (%d repos not on disk, not checked)", skipped)
	}
	return c, nil
}

// pinnedProblems checks one repo: which of shas are missing from its
// object store, and which have no pin ref.
func pinnedProblems(ctx context.Context, git *gitx.Runner, dir, path string, shas []string) []string {
	out, err := git.Output(ctx, dir, "for-each-ref", "--format=%(objectname)", snapRefPrefix)
	if err != nil {
		return []string{path + ": cannot read its refs: " + core.Message(err)}
	}
	pinned := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		pinned[strings.TrimSpace(line)] = true
	}
	res, err := git.RunInput(ctx, dir, strings.Join(shas, "\n")+"\n", "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return []string{path + ": cannot read its objects: " + core.Message(err)}
	}
	var missing, unpinned []string
	for line := range strings.SplitSeq(string(res.Stdout), "\n") {
		if sha, ok := strings.CutSuffix(strings.TrimSpace(line), " missing"); ok {
			missing = append(missing, sha)
		}
	}
	gone := map[string]bool{}
	for _, sha := range missing {
		gone[sha] = true
	}
	for _, sha := range shas {
		if !pinned[sha] && !gone[sha] {
			unpinned = append(unpinned, sha)
		}
	}
	var probs []string
	if len(missing) > 0 {
		probs = append(probs, fmt.Sprintf("%s: %d snapshot objects are missing, so those snapshots cannot be restored: %s",
			path, len(missing), shortList(missing)))
	}
	if len(unpinned) > 0 {
		probs = append(probs, fmt.Sprintf("%s: %d snapshot objects are not pinned, so git gc may remove them: %s",
			path, len(unpinned), shortList(unpinned)))
	}
	return probs
}

// shortList names the first few SHAs, shortened, and counts the rest.
func shortList(shas []string) string {
	var parts []string
	for i, sha := range shas {
		if i == shown {
			parts = append(parts, fmt.Sprintf("and %d more", len(shas)-shown))
			break
		}
		parts = append(parts, sha[:min(12, len(sha))])
	}
	return strings.Join(parts, ", ")
}
