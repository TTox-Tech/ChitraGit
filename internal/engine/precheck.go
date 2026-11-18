package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
)

// Pre-clone scan (KodeKavach Shield L0, CG-42.16): before a new repo is
// cloned, its default branch is read through the provider API and judged
// with the same checks as a clone. A block means not one object is
// downloaded. The API is a server Shield does not trust, so L0 only ever
// blocks early: whatever it passes is still cloned without a checkout and
// judged again from its own objects (L1).

// Limits of one pre-clone scan. A repo that needs more is left to L1.
const (
	preCheckBlobs   = 200     // blob reads through the API
	preCheckMaxBlob = 4 << 20 // bytes; larger blobs are not read
)

// PreCheckSkipped says the API could not judge a repo before cloning. It
// is never a reason to refuse the clone: L1 judges it from its objects.
type PreCheckSkipped struct{ Reason string }

func (e *PreCheckSkipped) Error() string { return "no pre-clone scan: " + e.Reason }

// Unwrap makes a skip an audit.ErrIncomplete, so a scan stops on it.
func (e *PreCheckSkipped) Unwrap() error { return audit.ErrIncomplete }

// PreCheck runs the pre-clone scan on target, a URL or provider:owner/repo,
// without cloning it. It returns a *PreCheckSkipped error when the host has
// no API or the API cannot answer in full.
func PreCheck(ctx context.Context, target string, reg *provider.Registry, o *ShieldOptions) (*ShieldReport, error) {
	r, err := resolveTarget(ctx, target, reg)
	if err != nil {
		return nil, err
	}
	return preCheck(ctx, reg, r, o)
}

func preCheck(ctx context.Context, reg *provider.Registry, r provider.Repo, o *ShieldOptions) (*ShieldReport, error) {
	if reg == nil {
		return nil, &PreCheckSkipped{Reason: "no provider API"}
	}
	client := reg.For(r.Host)
	if client == nil {
		return nil, &PreCheckSkipped{Reason: r.Host + " has no API ChitraGit knows"}
	}
	if r.DefaultBranch == "" {
		l, err := client.Lookup(ctx, r.Owner, r.Name)
		if err != nil {
			return nil, err
		}
		if l.Repo == nil {
			return nil, &core.Error{Category: core.CatNotFound, Message: r.Host + " has no repo " + r.FullName()}
		}
		r.DefaultBranch, r.SizeKB = l.Repo.DefaultBranch, l.Repo.SizeKB
	}
	rep := &ShieldReport{Repo: r.FullName(), URL: r.CloneURL, Verdict: audit.VerdictPass, Findings: []Finding{}}
	if r.DefaultBranch == "" {
		return rep, nil // an empty repo: nothing to check out
	}
	tree, err := client.Tree(ctx, r.Owner, r.Name, r.DefaultBranch)
	if err != nil {
		return nil, err
	}
	if tree.Truncated {
		return nil, &PreCheckSkipped{Reason: "the API sent only part of the file list"}
	}
	rep.Commit = tree.Commit
	src := &apiObjects{c: client, owner: r.Owner, name: r.Name, tree: tree, budget: preCheckBlobs}
	cr, err := audit.ScanObjects(ctx, src, audit.CommitOptions{RepoPath: r.FullName(), Commit: tree.Commit, Pack: o.Pack})
	if err != nil {
		return nil, err
	}
	// The API's size is the host's own estimate of the repo on disk; refs
	// are not listed by the API.
	size := audit.Size{Bytes: -1, Refs: -1}
	if r.SizeKB > 0 {
		size.Bytes = r.SizeKB << 10
	}
	rep.Findings = append(o.Limits.Check("", r.FullName(), tree.Commit, cr.Stats, size), cr.Findings...)
	rep.Verdict = audit.Judge(rep.Findings, o.BlockAt)
	return rep, nil
}

// apiObjects reads one commit through a provider API for audit.ScanObjects.
type apiObjects struct {
	c           provider.Client
	owner, name string
	tree        provider.Tree
	budget      int
	sizes       map[string]int64
}

// errBudget is a scan that needs more blob reads than one pre-check may make.
var errBudget = errors.New("the repo needs more file reads than a pre-clone scan makes")

func (a *apiObjects) Tree(context.Context) ([]gitx.TreeEntry, error) {
	a.sizes = map[string]int64{}
	var out []gitx.TreeEntry
	for _, e := range a.tree.Entries {
		if !gitx.SafeTreePath(e.Path) {
			continue
		}
		out = append(out, gitx.TreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.SHA, Size: e.Size})
		a.sizes[e.SHA] = e.Size
	}
	return out, nil
}

func (a *apiObjects) read(ctx context.Context, sha string, max int64) ([]byte, error) {
	if a.budget == 0 {
		return nil, &PreCheckSkipped{Reason: errBudget.Error()}
	}
	a.budget--
	data, err := a.c.Blob(ctx, a.owner, a.name, sha, max)
	if errors.Is(err, provider.ErrTooLarge) || core.CategoryOf(err) == core.CatNotFound {
		return nil, nil
	}
	return data, err
}

// Blobs reads each blob with one API call. Blobs over the pre-check cap
// are not read; L1 reads them from the clone.
func (a *apiObjects) Blobs(ctx context.Context, shas []string, maxSize int64, fn func(gitx.Blob) error) error {
	limit := int64(preCheckMaxBlob)
	if maxSize > 0 {
		limit = min(limit, maxSize)
	}
	for i, sha := range shas {
		if n, ok := a.sizes[sha]; ok && n > limit {
			continue
		}
		data, err := a.read(ctx, sha, limit)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		bin := bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
		if err := fn(gitx.Blob{Index: i, SHA: sha, Data: data, Binary: bin}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Submodules parses .gitmodules without git. A file the parser is unsure
// of is reported unreadable, as git's own parser would.
func (a *apiObjects) Submodules(ctx context.Context, sha string) ([][2]string, error) {
	data, err := a.read(ctx, sha, preCheckMaxBlob)
	if err == nil && data == nil {
		err = errors.New(".gitmodules is missing or too large")
	}
	if err != nil {
		var skip *PreCheckSkipped
		if errors.As(err, &skip) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", audit.ErrIncomplete, err)
	}
	return audit.ParseGitConfig(data)
}
