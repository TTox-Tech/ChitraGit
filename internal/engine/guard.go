package engine

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/guardian"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// GuardianOptions configures rescue detection (PRD F5).
type GuardianOptions struct {
	Confirmations int    // failed runs before a repo is rescued; 0 = 2
	AutoBundle    bool   // bundle a repo when it is rescued
	BundlesDir    string // <archive>/.chitragit/bundles
}

// Rescue is a repo the guardian marked rescued in this run.
type Rescue struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Bundle  string `json:"bundle,omitempty"`
}

// Move is a repo whose upstream was renamed or transferred; its remote URL
// now points to the new place.
type Move struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// guard runs after the fetches: for every repo that failed with "not
// found" or "access denied", it asks the provider why. Hosts whose API
// cannot answer (rate limit, rejected token) are left alone for the rest
// of the run, with one warning each.
type guard struct {
	git  *gitx.Runner
	st   *store.Store
	reg  *provider.Registry
	opts GuardianOptions
	run  int64

	mu       sync.Mutex
	down     map[string]bool
	rescued  []Rescue
	moved    []Move
	resync   []syncer.Job
	warnings []string
}

func (g *guard) check(ctx context.Context, results []syncer.Result, jobs map[string]syncer.Job) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, r := range results {
		if r.Outcome != syncer.Failed || !guardian.Worth(r.Category) || len(r.Remotes) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r syncer.Result) {
			defer func() { <-sem; wg.Done() }()
			g.one(ctx, r, jobs[r.Repo.ID])
		}(r)
	}
	wg.Wait()
}

func (g *guard) one(ctx context.Context, res syncer.Result, job syncer.Job) {
	r := res.Repo
	var client provider.Client
	if g.reg != nil && g.reg.KindOf(r.Host) != provider.Generic {
		g.mu.Lock()
		down := g.down[r.Host]
		g.mu.Unlock()
		if down {
			return
		}
		client = g.reg.For(r.Host)
	}
	d, err := guardian.Diagnose(ctx, client, r, res.Category)
	if err != nil {
		if c := core.CategoryOf(err); c == core.CatAuth || c == core.CatRateLimited || c == core.CatNetwork {
			g.mu.Lock()
			if !g.down[r.Host] {
				g.down[r.Host] = true
				g.warnings = append(g.warnings, "could not ask "+r.Host+" why some repos failed: "+core.Message(err)+
					"; rescue checks for that host wait for the next sync")
			}
			g.mu.Unlock()
		}
		return
	}
	if d.Repo != nil {
		_ = g.st.SetArchivedUpstream(ctx, r.ID, d.Repo.Archived)
	}
	switch {
	case d.Moved != nil:
		g.follow(ctx, res, job, d.Moved)
	case d.Reason != "":
		g.note(ctx, r, job, d)
	}
}

// follow points the remote at a renamed or transferred repo (PRD F5: no
// rescue, update the URL, log it) and queues the repo for another try.
func (g *guard) follow(ctx context.Context, res syncer.Result, job syncer.Job, to *provider.Repo) {
	name, oldURL := originRemote(res.Remotes)
	if name == "" {
		return
	}
	newURL := guardian.NewURL(oldURL, to)
	if newURL == "" || newURL == oldURL {
		return
	}
	if _, err := g.git.Run(ctx, job.Dir, "remote", "set-url", "--", name, newURL); err != nil {
		g.mu.Lock()
		g.warnings = append(g.warnings, res.Repo.Path+": upstream moved to "+to.FullName()+" but the remote could not be updated: "+core.Message(err))
		g.mu.Unlock()
		return
	}
	updated := core.Repo{ID: res.Repo.ID, Remotes: map[string]string{}}
	for k, v := range res.Remotes {
		updated.Remotes[k] = v
	}
	updated.Remotes[name] = newURL
	setOrigin(&updated)
	if err := g.st.RepoMoved(ctx, updated, oldURL, newURL); err != nil {
		g.mu.Lock()
		g.warnings = append(g.warnings, res.Repo.Path+": "+core.Message(err))
		g.mu.Unlock()
	}
	job.Repo.Host, job.Repo.Owner, job.Repo.Name = updated.Host, updated.Owner, updated.Name
	g.mu.Lock()
	g.moved = append(g.moved, Move{Path: res.Repo.Path, From: oldURL, To: newURL})
	g.resync = append(g.resync, job)
	g.mu.Unlock()
}

func (g *guard) note(ctx context.Context, r core.Repo, job syncer.Job, d guardian.Diagnosis) {
	threshold := g.opts.Confirmations
	if threshold <= 0 {
		threshold = 2
	}
	rescue, newly, err := g.st.NoteFailure(ctx, r.ID, g.run, d.Reason, d.Detail, threshold)
	if err != nil || !newly {
		return
	}
	out := Rescue{Path: r.Path, Reason: rescue.Reason, Message: store.RescueMessage(rescue.Reason)}
	if g.opts.AutoBundle && g.opts.BundlesDir != "" {
		dest := guardian.BundleFile(g.opts.BundlesDir, r.Path)
		switch err := guardian.Bundle(ctx, g.git, job.Dir, dest); {
		case err == nil:
			_ = g.st.SetBundle(ctx, r.ID, dest)
			out.Bundle = dest
		case !errors.Is(err, guardian.ErrEmpty):
			g.mu.Lock()
			g.warnings = append(g.warnings, r.Path+": rescued, but the bundle backup failed: "+core.Message(err))
			g.mu.Unlock()
		}
	}
	g.mu.Lock()
	g.rescued = append(g.rescued, out)
	g.mu.Unlock()
}

// originRemote returns "origin", or the only remote.
func originRemote(remotes map[string]string) (string, string) {
	if u, ok := remotes["origin"]; ok {
		return "origin", u
	}
	if len(remotes) == 1 {
		for n, u := range remotes {
			return n, u
		}
	}
	return "", ""
}

// BundleRequest selects repos to back up.
type BundleRequest struct {
	Root       string
	BundlesDir string
	Repos      []core.Repo
}

// BundleResult is one repo's backup.
type BundleResult struct {
	Path   string `json:"path"`
	Bundle string `json:"bundle,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Bundle writes git bundle backups of the given repos, one at a time
// (bundling is disk-bound).
func Bundle(ctx context.Context, req BundleRequest, git *gitx.Runner, st *store.Store) []BundleResult {
	var out []BundleResult
	for _, r := range req.Repos {
		if ctx.Err() != nil {
			break
		}
		dest := guardian.BundleFile(req.BundlesDir, r.Path)
		res := BundleResult{Path: r.Path}
		if err := guardian.Bundle(ctx, git, filepath.Join(req.Root, filepath.FromSlash(r.Path)), dest); err != nil {
			res.Error = core.Message(err)
		} else {
			res.Bundle = dest
			_ = st.SetBundle(ctx, r.ID, dest)
		}
		out = append(out, res)
	}
	return out
}
