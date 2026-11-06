package engine

import (
	"context"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// DepsStats sums up the dependency check over a run.
type DepsStats struct {
	Lockfiles int `json:"lockfiles"`
	Packages  int `json:"packages"`
	Fixed     int `json:"fixed"` // findings from earlier audits that are gone now
	// Unreadable lists lockfiles that could not be parsed, as "repo: file: error".
	Unreadable []string `json:"unreadable,omitempty"`
}

// auditDeps runs the dependency check on one repo. It always reads HEAD,
// whatever the history mode, because only the current lockfiles say what
// a checkout of the repo would install. Findings that an earlier audit
// saw and this one does not are marked fixed, never deleted.
func auditDeps(ctx context.Context, req AuditRequest, git *gitx.Runner, st *store.Store, runID int64,
	r core.Repo, dir string, rep *AuditReport, mu *sync.Mutex) error {
	res, err := audit.ScanDeps(ctx, git, req.VulnDB, audit.DepsOptions{
		RepoID: r.ID, RepoPath: r.Path, Dir: dir, MaxFileSize: req.MaxFileSize,
	})
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	saved, err := st.SaveRepoAudit(ctx, runID, r.ID, audit.CheckDeps, res.Findings, nil)
	if err != nil {
		return err
	}
	ids := make([]string, len(saved))
	for i, f := range saved {
		ids[i] = f.ID
	}
	fixed, err := st.MarkFixed(ctx, r.ID, audit.CheckDeps, ids)
	if err != nil {
		return err
	}
	if rep.Deps == nil {
		rep.Deps = &DepsStats{}
	}
	rep.Findings = append(rep.Findings, saved...)
	rep.Skipped += res.Skipped
	rep.Deps.Lockfiles += res.Lockfiles
	rep.Deps.Packages += res.Packages
	rep.Deps.Fixed += fixed
	for _, p := range res.Problems {
		rep.Deps.Unreadable = append(rep.Deps.Unreadable, r.Path+": "+p)
	}
	return nil
}
