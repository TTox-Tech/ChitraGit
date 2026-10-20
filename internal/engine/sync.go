package engine

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// SyncRequest selects repos and says how to sync them.
type SyncRequest struct {
	Root    string
	Only    string // glob on the repo path, e.g. "torvalds/*"; empty = all
	Options syncer.Options
	Listen  func(syncer.Event)
}

// SyncReport summarises a run.
type SyncReport struct {
	RunID       int64           `json:"run_id"`
	Mode        string          `json:"mode"`
	Interrupted bool            `json:"interrupted"`
	Counts      map[string]int  `json:"counts"`
	Results     []syncer.Result `json:"-"`
	Duration    time.Duration   `json:"-"`
}

// Sync updates every selected repo and records each result as soon as it
// finishes, so an interrupted run keeps everything already done.
func Sync(ctx context.Context, req SyncRequest, git *gitx.Runner, st *store.Store) (*SyncReport, error) {
	if req.Only != "" {
		if _, err := path.Match(req.Only, ""); err != nil {
			return nil, &core.Error{Category: core.CatConfig, Message: "invalid --only pattern " + req.Only, Err: err}
		}
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	var jobs []syncer.Job
	for _, r := range repos {
		if r.Missing {
			continue
		}
		if req.Only != "" {
			if ok, _ := path.Match(req.Only, r.Path); !ok {
				continue
			}
		}
		jobs = append(jobs, syncer.Job{Repo: r, Dir: filepath.Join(req.Root, filepath.FromSlash(r.Path))})
	}

	host, _ := os.Hostname()
	mode := req.Options.Mode
	runID, err := st.StartRun(ctx, "sync", mode, len(jobs), os.Getpid(), host)
	if err != nil {
		return nil, err
	}

	// The database must record results even after Ctrl+C.
	dbctx := context.WithoutCancel(ctx)
	var recordErr error
	listen := func(e syncer.Event) {
		if e.Kind == syncer.RepoFinished && e.Result.Outcome != "" && mode != "dry-run" {
			if err := st.RecordItem(dbctx, runID, item(e.Result)); err != nil && recordErr == nil {
				recordErr = err
			}
		}
		if req.Listen != nil {
			req.Listen(e)
		}
	}

	start := time.Now()
	results := syncer.Run(ctx, git, jobs, req.Options, listen)
	report := &SyncReport{RunID: runID, Mode: mode, Interrupted: ctx.Err() != nil, Counts: map[string]int{},
		Duration: time.Since(start)}
	for _, r := range results {
		if r.Outcome != "" {
			report.Results = append(report.Results, r)
			report.Counts[r.Outcome]++
		}
	}
	state := "finished"
	if report.Interrupted {
		state = "interrupted"
	}
	if err := st.FinishRun(dbctx, runID, state, report.Counts); err != nil {
		return report, err
	}
	return report, recordErr
}

func item(r *syncer.Result) store.RunItem {
	it := store.RunItem{
		RepoID: r.Repo.ID, Outcome: r.Outcome, Status: r.Status, Reason: r.Reason, Category: r.Category,
		StartedAt: r.Started.UTC().Format(time.RFC3339Nano), FinishedAt: r.Finished.UTC().Format(time.RFC3339Nano),
		Changes: r.Changes,
	}
	if r.Remotes != nil {
		rr := core.Repo{ID: r.Repo.ID, Remotes: r.Remotes}
		setOrigin(&rr)
		it.Remotes = &rr
	}
	return it
}
