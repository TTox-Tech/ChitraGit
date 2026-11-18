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

// CheckFsck is the verify check that runs git fsck on every repo
// (CG-07.4). By default it checks connectivity only: every ref reaches a
// complete history, which is quick and finds missing objects. With
// VerifyOptions.Full it also reads and hashes every object, which finds
// damaged ones but takes as long as reading the whole archive.
const CheckFsck = "fsck"

// VerifyOptions picks how deep verify looks.
type VerifyOptions struct {
	Full bool // git fsck reads every object, not just connectivity
}

// fsckLines is how many lines of git's complaint a problem keeps.
const fsckLines = 3

func verifyFsck(ctx context.Context, root string, git *gitx.Runner, st *store.Store, o VerifyOptions) (VerifyCheck, error) {
	c := VerifyCheck{Name: CheckFsck}
	repos, err := st.Repos(ctx)
	if err != nil {
		return c, err
	}
	var todo []core.Repo
	skipped := 0
	for _, r := range repos {
		switch {
		case r.Kind == core.KindWorktree:
			// Shares its main repo's objects; that repo is checked.
		case r.Missing:
			skipped++
		default:
			todo = append(todo, r)
		}
	}
	args := []string{"fsck", "--no-dangling", "--no-progress"}
	if !o.Full {
		args = append(args, "--connectivity-only")
	}

	var mu sync.Mutex
	jobs := make(chan core.Repo)
	var wg sync.WaitGroup
	for range min(verifyWorkers, len(todo)) {
		wg.Go(func() {
			for r := range jobs {
				res, err := git.Run(ctx, filepath.Join(root, filepath.FromSlash(r.Path)), args...)
				if err == nil || ctx.Err() != nil {
					continue
				}
				mu.Lock()
				c.Problems = append(c.Problems, r.Path+": "+fsckWhy(res, err))
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

	depth := "connectivity"
	if o.Full {
		depth = "every object"
	}
	c.OK = len(c.Problems) == 0
	if c.OK {
		c.Summary = fmt.Sprintf("%d repos pass git fsck (%s)", len(todo), depth)
	} else {
		c.Summary = fmt.Sprintf("%d of %d repos fail git fsck (%s)", len(c.Problems), len(todo), depth)
	}
	if skipped > 0 {
		c.Summary += fmt.Sprintf("; %d repos not on disk, not checked", skipped)
	}
	return c, nil
}

// fsckWhy keeps the first lines git printed about what is broken.
func fsckWhy(res *gitx.Result, err error) string {
	var lines []string
	if res != nil {
		for _, out := range [][]byte{res.Stdout, res.Stderr} {
			for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "Checking ") || strings.HasPrefix(line, "notice:") {
					continue
				}
				lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "fatal: "), "error: "))
			}
		}
	}
	if len(lines) == 0 {
		return core.Message(err)
	}
	if len(lines) > fsckLines {
		lines = append(lines[:fsckLines], fmt.Sprintf("and %d more lines", len(lines)-fsckLines))
	}
	return strings.Join(lines, "; ")
}
