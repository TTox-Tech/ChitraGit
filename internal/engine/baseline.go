package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Baseline scan (KodeKavach Shield, CG-42.17): the clone and sync gates
// judge what comes in, but repos already on disk, such as every repo of an
// archive made before Shield, were never judged. Baseline gives each repo
// without a verdict one, from its HEAD's objects alone. A blocked repo is
// held: later syncs still download into it, but never move its branches
// or write its files. Baseline only reads repos; it never deletes, moves
// or changes a file.

// EventShieldBaseline is the ledger event of a repo the baseline blocked.
const EventShieldBaseline = "shield_baseline_blocked"

// baselineWorkers is how many repos are judged at once.
const baselineWorkers = 4

// BaselineReport is what one baseline pass did.
type BaselineReport struct {
	Judged  int      `json:"judged"`
	Blocked []string `json:"blocked,omitempty"` // repo paths
	Failed  []string `json:"failed,omitempty"`  // "path: reason"; judged again next time
}

// Baseline judges every repo in repos that needs it: one with no verdict,
// or a held one whose HEAD moved since (its owner may have removed what was
// found). Bare repos are judged too, but are never held: they have no files.
func Baseline(ctx context.Context, root string, repos []core.Repo, o *ShieldOptions, git *gitx.Runner, st *store.Store) (*BaselineReport, error) {
	verdicts, err := st.ShieldVerdicts(ctx)
	if err != nil {
		return nil, err
	}
	var todo []core.Repo
	for _, r := range repos {
		if r.Missing || r.Kind == core.KindWorktree {
			continue
		}
		v, ok := verdicts[r.ID]
		if ok && v.Verdict != string(audit.VerdictBlock) {
			continue
		}
		todo = append(todo, r)
	}
	rep := &BaselineReport{}
	var mu sync.Mutex
	jobs := make(chan core.Repo)
	var wg sync.WaitGroup
	for range min(baselineWorkers, len(todo)) {
		wg.Go(func() {
			for r := range jobs {
				blocked, judged, err := baselineOne(ctx, root, r, verdicts, o, git, st)
				mu.Lock()
				switch {
				case err != nil:
					rep.Failed = append(rep.Failed, r.Path+": "+core.Message(err))
				case judged:
					rep.Judged++
					if blocked {
						rep.Blocked = append(rep.Blocked, r.Path)
					}
				}
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
	sort.Strings(rep.Blocked)
	sort.Strings(rep.Failed)
	return rep, ctx.Err()
}

// baselineOne judges one repo's HEAD and saves the verdict. judged is false
// when a held repo's HEAD has not moved, so there is nothing new to judge.
func baselineOne(ctx context.Context, root string, r core.Repo, verdicts map[string]store.ShieldVerdict, o *ShieldOptions,
	git *gitx.Runner, st *store.Store) (blocked, judged bool, err error) {
	dir := filepath.Join(root, filepath.FromSlash(r.Path))
	head := audit.HeadCommitOf(ctx, git, dir)
	if old, ok := verdicts[r.ID]; ok && old.Commit == head {
		return old.Verdict == string(audit.VerdictBlock) && r.Kind != core.KindBare, false, nil
	}
	v := store.ShieldVerdict{RepoID: r.ID, Commit: head, Verdict: string(audit.VerdictPass), Report: "[]"}
	var fs []Finding
	if head != "" {
		cr, err := audit.ScanCommit(ctx, git, audit.CommitOptions{RepoID: r.ID, RepoPath: r.Path, Dir: dir, Commit: head, Pack: o.Pack})
		if err != nil {
			return false, false, err
		}
		// The repo's size and refs grew over years in the archive; only its
		// tree is held to the limits, as on sync.
		fs = append(o.Limits.Check(r.ID, r.Path, head, cr.Stats, audit.Size{Bytes: -1, Refs: -1}), cr.Findings...)
		v.Verdict = string(audit.Judge(fs, o.BlockAt))
		b, err := json.Marshal(fs)
		if err != nil {
			return false, false, err
		}
		v.Report, v.Findings = string(b), len(fs)
	}
	held := v.Verdict == string(audit.VerdictBlock) && r.Kind != core.KindBare
	if v.Verdict == string(audit.VerdictBlock) && r.Kind == core.KindBare {
		v.Verdict = string(audit.VerdictWarn) // no files to hold back
	}
	event, payload := "", any(nil)
	if held {
		v.Reason = fmt.Sprintf("KodeKavach blocked the files already on disk at %.12s (%d findings: %s); synced but held: "+
			"no branch moves and no file changes. Nothing was deleted.", head, len(fs), fs[0].Title)
		event, payload = EventShieldBaseline, map[string]any{
			"message": v.Reason, "repo": r.Path, "commit": head, "findings": len(fs), "rules": rules(fs),
		}
	}
	if err := st.SetShieldVerdict(context.WithoutCancel(ctx), v, event, payload); err != nil {
		return false, false, err
	}
	return held, true, nil
}

// Holds returns the reason each held repo is held, by repo id.
func Holds(ctx context.Context, st *store.Store) (map[string]string, error) {
	verdicts, err := st.ShieldVerdicts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for id, v := range verdicts {
		if v.Verdict == string(audit.VerdictBlock) {
			out[id] = v.Reason
		}
	}
	return out, nil
}
