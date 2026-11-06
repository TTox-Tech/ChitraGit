package engine

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// AuditRequest says what KodeKavach should check.
type AuditRequest struct {
	Root        string
	Repos       []string // repo paths; empty = every repo
	Only        string   // glob on the repo path
	Checks      []string // empty = all
	History     string   // audit.History*
	Workers     int      // repos audited in parallel; 0 = 4
	MaxFileSize int64
	// VulnDB is the offline vulnerability database for the deps check.
	VulnDB *audit.VulnDB
	// Rules is the pack for the actions and agents checks; nil = built-in.
	Rules *audit.RulesPack
	// Zizmor and SkillSpector, when set, also run over each repo's files.
	Zizmor, SkillSpector *audit.Tool
	// Progress, when set, is called after each repo (from several goroutines).
	Progress func(done, total int, repo string)
}

// AuditFailure is a repo the audit could not read.
type AuditFailure struct {
	Repo  string `json:"repo"`
	Error string `json:"error"`
}

// AuditReport is the result of Audit.
type AuditReport struct {
	RunID    int64           `json:"run_id"`
	Checks   []string        `json:"checks"`
	History  string          `json:"history"`
	Repos    int             `json:"repos"`
	Commits  int             `json:"commits_read"`
	Skipped  int             `json:"files_skipped"`
	Deps     *DepsStats      `json:"deps,omitempty"`
	Failed   []AuditFailure  `json:"failed,omitempty"`
	Findings []audit.Finding `json:"findings"` // everything seen by this run
	Rules    string          `json:"rules_pack,omitempty"`
	Tools    []ToolUse       `json:"tools,omitempty"`
	Summary  audit.Summary   `json:"summary"`
}

// Audit runs the security checks over the archive. It reads repos only;
// it never writes to one. Results and per-repo progress go to the archive
// database, so an interrupted audit loses at most the repos in flight.
func Audit(ctx context.Context, req AuditRequest, git *gitx.Runner, st *store.Store) (*AuditReport, error) {
	checks := req.Checks
	if len(checks) == 0 {
		checks = audit.AllChecks
	}
	history := req.History
	if history == "" {
		history = audit.HistoryIncremental
	}
	all, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	var repos []core.Repo
	for _, r := range all {
		if r.Missing || r.Kind == core.KindWorktree { // a worktree shares its main repo's history
			continue
		}
		if len(req.Repos) > 0 && !slices.Contains(req.Repos, r.Path) {
			continue
		}
		if req.Only != "" {
			if ok, _ := path.Match(req.Only, r.Path); !ok {
				continue
			}
		}
		repos = append(repos, r)
	}

	if req.Rules == nil {
		if req.Rules, err = audit.BuiltinRules(); err != nil {
			return nil, err
		}
	}
	req.History = history
	tools := map[string]*ToolUse{}
	for _, t := range []*audit.Tool{req.Zizmor, req.SkillSpector} {
		if t != nil {
			tools[t.Name] = &ToolUse{Name: t.Name, Version: t.Version}
		}
	}

	runID, err := st.BeginAudit(ctx, checks, history)
	if err != nil {
		return nil, err
	}
	rep := &AuditReport{RunID: runID, Checks: checks, History: history, Repos: len(repos), Findings: []audit.Finding{}}
	if slices.ContainsFunc(checks, isHeadCheck) {
		rep.Rules = req.Rules.Version
	}

	workers := req.Workers
	if workers <= 0 {
		workers = 4
	}
	var (
		mu   sync.Mutex // guards rep and serialises database writes
		wg   sync.WaitGroup
		done int
		sem  = make(chan struct{}, workers)
	)
	for _, r := range repos {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r core.Repo) {
			defer func() { <-sem; wg.Done() }()
			fail := func(err error) {
				mu.Lock()
				rep.Failed = append(rep.Failed, AuditFailure{Repo: r.Path, Error: core.Message(err)})
				mu.Unlock()
			}
			dir := filepath.Join(req.Root, filepath.FromSlash(r.Path))
			if _, err := os.Stat(dir); err != nil {
				fail(audit.ErrNoRepo)
				return
			}
			if slices.ContainsFunc(checks, isHeadCheck) {
				results, skipped, err := runHeadChecks(ctx, req, git, st, &mu, tools, r, dir, checks)
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						fail(err)
					}
					return
				}
				for _, hr := range results {
					mu.Lock()
					saved, err := st.SaveRepoAudit(ctx, runID, r.ID, hr.check, hr.findings, hr.tips)
					if err == nil {
						rep.Findings = append(rep.Findings, saved...)
					}
					mu.Unlock()
					if err != nil {
						fail(err)
						return
					}
				}
				mu.Lock()
				rep.Skipped += skipped
				mu.Unlock()
			}
			for _, check := range checks {
				switch check {
				case audit.CheckSecrets:
				case audit.CheckDeps:
					if err := auditDeps(ctx, req, git, st, runID, r, dir, rep, &mu); err != nil {
						if !errors.Is(err, context.Canceled) {
							fail(err)
						}
						return
					}
					continue
				default:
					continue
				}
				o := audit.SecretOptions{RepoID: r.ID, RepoPath: r.Path, Dir: dir, History: history, MaxFileSize: req.MaxFileSize}
				if history == audit.HistoryIncremental {
					mu.Lock()
					seen, err := st.AuditCursor(ctx, r.ID, check)
					o.Seen = seen
					mu.Unlock()
					if err != nil {
						fail(err)
						return
					}
				}
				res, err := audit.ScanSecrets(ctx, git, o)
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						fail(err)
					}
					return
				}
				var tips []string
				if history != audit.HistoryHead {
					tips = res.Tips
					if tips == nil {
						tips = []string{}
					}
				}
				mu.Lock()
				saved, err := st.SaveRepoAudit(ctx, runID, r.ID, check, res.Findings, tips)
				if err == nil {
					rep.Findings = append(rep.Findings, saved...)
					rep.Commits += res.Commits
					rep.Skipped += res.Skipped
				}
				mu.Unlock()
				if err != nil {
					fail(err)
					return
				}
			}
			mu.Lock()
			done++
			n := done
			mu.Unlock()
			if req.Progress != nil {
				req.Progress(n, len(repos), r.Path)
			}
		}(r)
	}
	wg.Wait()

	slices.SortStableFunc(rep.Findings, func(a, b audit.Finding) int {
		if c := b.Severity.Rank() - a.Severity.Rank(); c != 0 {
			return c
		}
		if a.RepoPath != b.RepoPath {
			if a.RepoPath < b.RepoPath {
				return -1
			}
			return 1
		}
		return 0
	})
	rep.Summary = audit.Summarise(rep.Findings)
	for _, t := range []*audit.Tool{req.Zizmor, req.SkillSpector} {
		if t != nil {
			rep.Tools = append(rep.Tools, *tools[t.Name])
		}
	}
	slices.SortFunc(rep.Failed, func(a, b AuditFailure) int {
		if a.Repo < b.Repo {
			return -1
		}
		return 1
	})
	// Record the run even when interrupted, with what was finished.
	if err := st.FinishAudit(context.WithoutCancel(ctx), runID, len(repos), rep.Summary.New, len(rep.Failed)); err != nil {
		return rep, err
	}
	return rep, ctx.Err()
}
