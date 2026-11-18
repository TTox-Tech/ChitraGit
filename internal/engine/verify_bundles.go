package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// CheckBundles is the verify check that every bundle backup can still be
// read in full (CG-07.3): each file under .chitragit/bundles, and each one
// a rescue record points at, must pass `git bundle verify`. A rescued repo
// may be gone upstream, so its bundle can be the only copy left.
const CheckBundles = "bundles"

func verifyBundles(ctx context.Context, root string, git *gitx.Runner, st *store.Store) (VerifyCheck, error) {
	c := VerifyCheck{Name: CheckBundles}
	dir := filepath.Join(root, archive.MetaDir, "bundles")
	files := map[string]string{} // clean path -> what to call it in a problem
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".bundle") {
			rel, _ := filepath.Rel(root, p)
			files[filepath.Clean(p)] = filepath.ToSlash(rel)
		}
		return nil
	})
	if err != nil {
		c.Problems = append(c.Problems, "cannot read the bundles folder: "+err.Error())
	}

	recorded, err := st.RecordedBundles(ctx)
	if err != nil {
		return c, err
	}
	paths := map[string]string{}
	if repos, err := st.Repos(ctx); err == nil {
		for _, r := range repos {
			paths[r.ID] = r.Path
		}
	}
	for id, p := range recorded {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		p = filepath.Clean(p)
		if _, ok := files[p]; ok {
			continue
		}
		name := paths[id]
		if name == "" {
			name = id
		}
		if _, err := os.Stat(p); err != nil {
			c.Problems = append(c.Problems, fmt.Sprintf("%s: its rescue bundle %s is gone", name, p))
			continue
		}
		files[p] = p // recorded, but kept outside the bundles folder
	}

	if len(files) > 0 {
		probs, err := checkBundles(ctx, git, files)
		if err != nil {
			return c, err
		}
		c.Problems = append(c.Problems, probs...)
	}
	sort.Strings(c.Problems)
	c.OK = len(c.Problems) == 0
	switch {
	case !c.OK:
		c.Summary = fmt.Sprintf("%d problems with bundle backups", len(c.Problems))
	case len(files) == 0:
		c.Summary = "no bundle backups yet"
	default:
		c.Summary = fmt.Sprintf("%d bundle backups, all readable", len(files))
	}
	return c, nil
}

// checkBundles reads each file in full. `git bundle verify` only checks
// the header and the objects it needs from a repo, so each bundle is also
// unpacked into its own empty scratch repo: index-pack checks the pack's
// checksum and every object in it. The scratch repo is removed right after,
// so at most verifyWorkers bundles take disk space at once.
func checkBundles(ctx context.Context, git *gitx.Runner, files map[string]string) ([]string, error) {
	var probs []string
	var firstErr error
	var mu sync.Mutex
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(verifyWorkers, len(files)) {
		wg.Go(func() {
			for p := range jobs {
				why, err := checkBundle(ctx, git, p)
				mu.Lock()
				switch {
				case err != nil && firstErr == nil:
					firstErr = err
				case why != "":
					probs = append(probs, fmt.Sprintf("%s: does not verify: %s", files[p], why))
				}
				mu.Unlock()
			}
		})
	}
	for p := range files {
		if ctx.Err() != nil {
			break
		}
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return probs, firstErr
}

// checkBundle returns why the bundle at p is unreadable, or "". err is a
// failure to check at all, such as no space for the scratch repo.
func checkBundle(ctx context.Context, git *gitx.Runner, p string) (why string, err error) {
	scratch, err := os.MkdirTemp("", "chitragit-verify-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	if _, err := git.Run(ctx, scratch, "init", "-q", "--bare"); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"bundle", "verify", "-q", p}, {"bundle", "unbundle", p}} {
		res, err := git.Run(ctx, scratch, args...)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		why := core.Message(err)
		if res != nil {
			if line, _, _ := strings.Cut(strings.TrimSpace(string(res.Stderr)), "\n"); line != "" {
				why = strings.TrimPrefix(strings.TrimPrefix(line, "fatal: "), "error: ")
			}
		}
		return why, nil
	}
	return "", nil
}
