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
	Root string
	Only string   // glob on the repo path, e.g. "torvalds/*"; empty = all
	IDs  []string // if set, only these repos (a selection made in the TUI)
	// OnInterrupted decides what happens when the last sync did not finish:
	// "resume" syncs only the repos it had not done yet, "fresh" starts over.
	OnInterrupted string
	Options       syncer.Options
	Listen        func(syncer.Event)
	// Planned, if set, is told how many repos the run will sync, before
	// the first one starts.
	Planned func(total int)
}

// SyncReport summarises a run.
type SyncReport struct {
	RunID       int64           `json:"run_id"`
	ResumedFrom int64           `json:"resumed_from,omitempty"` // the interrupted run this one continued
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
	mode := req.Options.Mode
	var resume map[string]bool
	var resumedFrom int64
	if mode != "dry-run" {
		last, err := st.LastUnfinishedRun(ctx)
		if err != nil {
			return nil, err
		}
		if last != nil {
			state := "discarded"
			if req.OnInterrupted == "resume" {
				state, resumedFrom, resume = "resumed", last.ID, map[string]bool{}
				for _, id := range last.Pending {
					resume[id] = true
				}
			}
			if err := st.SetRunState(ctx, last.ID, state); err != nil {
				return nil, err
			}
		}
	}
	var only map[string]bool
	if len(req.IDs) > 0 && resume == nil {
		only = map[string]bool{}
		for _, id := range req.IDs {
			only[id] = true
		}
	}
	var jobs []syncer.Job
	var ids []string
	for _, r := range repos {
		if r.Missing || (resume != nil && !resume[r.ID]) || (only != nil && !only[r.ID]) {
			continue
		}
		if req.Only != "" && resume == nil {
			if ok, _ := path.Match(req.Only, r.Path); !ok {
				continue
			}
		}
		jobs = append(jobs, syncer.Job{Repo: r, Dir: filepath.Join(req.Root, filepath.FromSlash(r.Path))})
		ids = append(ids, r.ID)
	}

	// A dry run changes nothing, so it leaves no journal to resume.
	var runID int64
	if mode != "dry-run" {
		host, _ := os.Hostname()
		if runID, err = st.StartRun(ctx, "sync", mode, ids, os.Getpid(), host); err != nil {
			return nil, err
		}
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

	if req.Planned != nil {
		req.Planned(len(jobs))
	}
	start := time.Now()
	results := syncer.Run(ctx, git, jobs, req.Options, listen)
	report := &SyncReport{RunID: runID, ResumedFrom: resumedFrom, Mode: mode, Interrupted: ctx.Err() != nil, Counts: map[string]int{},
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
	if runID != 0 {
		if err := st.FinishRun(dbctx, runID, state, report.Counts); err != nil {
			return report, err
		}
	}
	return report, recordErr
}

func item(r *syncer.Result) store.RunItem {
	it := store.RunItem{
		RepoID: r.Repo.ID, Outcome: r.Outcome, Status: r.Status, Reason: r.Reason, Category: r.Category,
		StartedAt: r.Started.UTC().Format(time.RFC3339Nano), FinishedAt: r.Finished.UTC().Format(time.RFC3339Nano),
		Changes:  r.Changes,
		Conflict: r.Status == syncer.StatusConflict || r.Status == syncer.StatusResolving, ConflictFiles: r.ConflictFiles,
	}
	if r.Remotes != nil {
		rr := core.Repo{ID: r.Repo.ID, Remotes: r.Remotes}
		setOrigin(&rr)
		it.Remotes = &rr
	}
	return it
}
