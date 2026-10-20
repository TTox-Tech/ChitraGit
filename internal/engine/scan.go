// Package engine is ChitraGit's UI-free API. The CLI and the TUI call it;
// it wires the scanner, the syncer, git and the archive database together.
package engine

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// idKey is the git config key that stores a repo's stable ChitraGit ID
// inside the repo itself, so the ID travels with the folder.
const idKey = "chitragit.id"

// ScanReport is the result of Scan.
type ScanReport struct {
	Found    int      `json:"found"`
	Added    int      `json:"added"`
	Moved    int      `json:"moved"`
	Missing  int      `json:"missing"`
	Returned int      `json:"returned"`
	Warnings []string `json:"warnings,omitempty"`
}

// Scan walks the archive and brings the database up to date.
//
// Repos already known at the same path cost no git call. New paths are
// opened with git once to read (or create) their ID and remotes. An ID
// lets a moved repo keep its history, and a copied folder (two paths with
// the same ID) gets a fresh ID so the two never merge.
func Scan(ctx context.Context, root string, opts scan.Options, git *gitx.Runner, st *store.Store) (*ScanReport, error) {
	res, err := scan.Walk(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	known, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]core.Repo{}
	pathOfID := map[string]string{}
	for _, r := range known {
		byPath[r.Path] = r
		pathOfID[r.ID] = r.Path
	}
	onDisk := map[string]bool{}
	for _, f := range res.Repos {
		onDisk[f.Path] = true
	}

	report := &ScanReport{Found: len(res.Repos), Warnings: res.Warnings}
	present := make([]core.Repo, len(res.Repos))
	var fresh []int
	for i, f := range res.Repos {
		if r, ok := byPath[f.Path]; ok && r.Kind == f.Kind {
			present[i] = core.Repo{ID: r.ID, Path: f.Path, Kind: f.Kind}
			continue
		}
		fresh = append(fresh, i)
	}

	// Identify new paths in parallel; each costs one or two git calls.
	var mu sync.Mutex
	claimed := map[string]bool{}
	sem := make(chan struct{}, max(opts.Workers, 8))
	var wg sync.WaitGroup
	for _, i := range fresh {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			f := res.Repos[i]
			r, warn := identify(ctx, git, filepath.Join(root, filepath.FromSlash(f.Path)), f)

			mu.Lock()
			// The ID is taken if another folder still uses it: on disk at its
			// known path, or claimed earlier in this scan. That is a copy.
			if old, ok := pathOfID[r.ID]; r.ID == "" || claimed[r.ID] || (ok && old != f.Path && onDisk[old]) {
				r.ID = ""
			} else {
				claimed[r.ID] = true
			}
			mu.Unlock()

			if r.ID == "" {
				r.ID = uuid.NewString()
				if f.Kind != core.KindWorktree {
					if _, err := git.Run(ctx, filepath.Join(root, filepath.FromSlash(f.Path)), "config", idKey, r.ID); err != nil && warn == "" {
						warn = f.Path + ": cannot save its ChitraGit ID, so a move will look like a new repo: " + err.Error()
					}
				}
			}
			mu.Lock()
			present[i] = r
			if warn != "" {
				report.Warnings = append(report.Warnings, warn)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch, err := st.ApplyScan(ctx, present)
	if err != nil {
		return nil, err
	}
	report.Added, report.Moved, report.Missing, report.Returned = ch.Added, ch.Moved, ch.Missing, ch.Returned
	return report, nil
}

// identify reads a repo's ID and remotes in one git call. The ID is empty
// when the repo has none yet.
//
// shortcut: linked worktrees share their main repo's config, so they get a
// new ID that is not saved in the repo; a moved worktree shows up as a new
// entry. Use per-worktree config (extensions.worktreeConfig) if that matters.
func identify(ctx context.Context, git *gitx.Runner, dir string, f scan.Found) (core.Repo, string) {
	r := core.Repo{Path: f.Path, Kind: f.Kind}
	cfg, err := readConfig(ctx, git, dir, `^(chitragit\.id|remote\..*\.url)$`)
	if err != nil {
		return r, f.Path + ": " + err.Error()
	}
	r.Remotes = map[string]string{}
	for key, val := range cfg {
		if key == idKey && f.Kind != core.KindWorktree {
			r.ID = val
		} else if name, ok := strings.CutPrefix(key, "remote."); ok {
			r.Remotes[strings.TrimSuffix(name, ".url")] = val
		}
	}
	setOrigin(&r)
	return r, ""
}

// setOrigin fills provider/host/owner/name from "origin", or from the only
// remote when there is just one.
func setOrigin(r *core.Repo) {
	url, ok := r.Remotes["origin"]
	if !ok && len(r.Remotes) == 1 {
		for _, u := range r.Remotes {
			url, ok = u, true
		}
	}
	if !ok {
		return
	}
	p := provider.Parse(url)
	r.Provider, r.Host, r.Owner, r.Name = p.Provider, p.Host, p.Owner, p.Name
}

// readConfig returns git config entries matching pattern. Keys are
// lower-cased by git except the middle part (the remote name).
func readConfig(ctx context.Context, git *gitx.Runner, dir, pattern string) (map[string]string, error) {
	res, err := git.Run(ctx, dir, "config", "--null", "--get-regexp", pattern)
	if err != nil {
		if res != nil && res.ExitCode == 1 && len(res.Stderr) == 0 {
			return map[string]string{}, nil // no matching keys
		}
		return nil, err
	}
	out := map[string]string{}
	for _, entry := range strings.Split(string(res.Stdout), "\x00") {
		if k, v, ok := strings.Cut(entry, "\n"); ok {
			out[k] = v
		}
	}
	return out, nil
}
