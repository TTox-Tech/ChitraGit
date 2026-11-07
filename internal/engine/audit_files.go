package engine

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// ToolUse reports how an external scanner did during an audit.
type ToolUse struct {
	Name    string   `json:"name"`
	Version string   `json:"version,omitempty"`
	Repos   int      `json:"repos"`            // repos it scanned
	Errors  []string `json:"errors,omitempty"` // first few failures, "repo: message"
}

const maxToolErrors = 10

// Limits on what SkillSpector gets to read from one repo's skill folders.
const (
	maxSkillFiles = 400
	maxSkillBytes = 8 << 20
)

// headCheckResult is what one HEAD-based check found in one repo.
type headCheckResult struct {
	check    string
	findings []audit.Finding
	tips     []string
}

// findingIDs lists the IDs of findings.
func findingIDs(fs []audit.Finding) []string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	return ids
}

// isHeadCheck reports checks that look at the files at HEAD only.
func isHeadCheck(c string) bool { return c == audit.CheckActions || c == audit.CheckAgents }

// headCheckCursor is what an incremental audit compares to skip a repo:
// the HEAD commit, the rules pack and the tool versions it was read with.
func headCheckCursor(head string, req AuditRequest, check string) []string {
	tips := []string{head, "rules@" + req.Rules.Version}
	t := req.Zizmor
	if check == audit.CheckAgents {
		t = req.SkillSpector
	}
	if t != nil {
		tips = append(tips, "tool@"+t.Name+"@"+t.Version)
	}
	return tips
}

// runHeadChecks reads a repo's workflow and agent files at HEAD once and
// runs the actions and agents checks (and the external tools) over them.
// It only reads: files come from the object store, and tools get a copy
// in a temporary folder. mu guards st and tools.
func runHeadChecks(ctx context.Context, req AuditRequest, git *gitx.Runner, st *store.Store, mu *sync.Mutex,
	tools map[string]*ToolUse, r core.Repo, dir string, checks []string) ([]headCheckResult, int, error) {
	head := audit.HeadCommitOf(ctx, git, dir)
	if head == "" {
		return nil, 0, nil // empty repo
	}
	var todo []string
	for _, c := range checks {
		if !isHeadCheck(c) {
			continue
		}
		if req.History == audit.HistoryIncremental {
			mu.Lock()
			seen, err := st.AuditCursor(ctx, r.ID, c)
			mu.Unlock()
			if err != nil {
				return nil, 0, err
			}
			if slices.Equal(seen, headCheckCursor(head, req, c)) {
				continue // nothing changed since the last audit
			}
		}
		todo = append(todo, c)
	}
	if len(todo) == 0 {
		return nil, 0, nil
	}
	pack := req.Rules
	files, skipped, err := audit.ReadHeadFiles(ctx, git, dir, head, func(p string) bool {
		if slices.Contains(todo, audit.CheckActions) && audit.IsWorkflowFile(p) {
			return true
		}
		if slices.Contains(todo, audit.CheckAgents) {
			_, ok := pack.AgentFile(p)
			return ok
		}
		return false
	}, req.MaxFileSize)
	if err != nil {
		return nil, 0, err
	}

	toolFailed := func(t *audit.Tool, err error) {
		mu.Lock()
		defer mu.Unlock()
		u := tools[t.Name]
		if len(u.Errors) < maxToolErrors {
			u.Errors = append(u.Errors, r.Path+": "+err.Error())
		}
	}
	toolRan := func(t *audit.Tool) {
		mu.Lock()
		tools[t.Name].Repos++
		mu.Unlock()
	}

	var out []headCheckResult
	for _, c := range todo {
		res := headCheckResult{check: c, tips: headCheckCursor(head, req, c)}
		switch c {
		case audit.CheckActions:
			res.findings = audit.ScanActions(pack, r.ID, r.Path, head, files)
			if t := req.Zizmor; t != nil && slices.ContainsFunc(files, func(f audit.HeadFile) bool { return audit.IsWorkflowFile(f.Path) }) {
				got, err := audit.RunZizmor(ctx, t, r.ID, r.Path, head, files)
				if err != nil {
					if ctx.Err() != nil {
						return nil, skipped, ctx.Err()
					}
					toolFailed(t, err)
					res.tips = nil // try the repo again next time
				} else {
					toolRan(t)
					res.findings = append(audit.DropCovered(res.findings, got, pack.Actions.ZizmorEquivalents), got...)
				}
			}
		case audit.CheckAgents:
			res.findings = audit.ScanAgents(pack, r.ID, r.Path, head, files)
			if t := req.SkillSpector; t != nil {
				if dirs := audit.SkillDirs(pack.AgentFiles(files)); len(dirs) > 0 {
					got, err := skillSpector(ctx, req, git, t, r, dir, head, dirs)
					if err != nil {
						if ctx.Err() != nil {
							return nil, skipped, ctx.Err()
						}
						toolFailed(t, err)
						res.tips = nil
					} else {
						toolRan(t)
						res.findings = append(res.findings, got...)
					}
				}
			}
		}
		out = append(out, res)
	}
	return out, skipped, nil
}

// skillSpector gives SkillSpector every file in the repo's skill folders
// (scripts included, since it checks code too), within size limits.
func skillSpector(ctx context.Context, req AuditRequest, git *gitx.Runner, t *audit.Tool, r core.Repo, dir, head string, dirs []string) ([]audit.Finding, error) {
	n, total := 0, 0
	files, _, err := audit.ReadHeadFiles(ctx, git, dir, head, func(p string) bool {
		if n >= maxSkillFiles {
			return false
		}
		for _, d := range dirs {
			if d == "." || strings.HasPrefix(p, d+"/") {
				n++
				return true
			}
		}
		return false
	}, req.MaxFileSize)
	if err != nil {
		return nil, err
	}
	var keep []audit.HeadFile
	for _, f := range files {
		if total += len(f.Data); total > maxSkillBytes {
			break
		}
		keep = append(keep, f)
	}
	got, err := audit.RunSkillSpector(ctx, t, r.ID, r.Path, head, keep)
	for i := range got {
		got[i].File = path.Clean(got[i].File)
	}
	return got, err
}
