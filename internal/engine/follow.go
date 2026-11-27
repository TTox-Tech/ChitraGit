package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Filters narrow what a followed source clones (PRD F6).
type Filters struct {
	Forks     bool   `json:"forks,omitempty"`       // include forks (default: skip them)
	Archived  *bool  `json:"archived,omitempty"`    // include archived repos (default: yes, they are at risk)
	MaxSizeMB int64  `json:"max_size_mb,omitempty"` // 0 = no limit
	Name      string `json:"name,omitempty"`        // glob on the repo name
	Language  string `json:"language,omitempty"`    // e.g. Go; case-insensitive
}

// ParseFilters reads filters stored as JSON.
func ParseFilters(s string) (Filters, error) {
	var f Filters
	if s == "" {
		return f, nil
	}
	err := json.Unmarshal([]byte(s), &f)
	return f, err
}

// JSON returns the filters as stored, or "" when none are set.
func (f Filters) JSON() string {
	if f == (Filters{}) {
		return ""
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// Match reports whether a repo passes the filters, and why not.
func (f Filters) Match(r provider.Repo) (bool, string) {
	switch {
	case r.Fork && !f.Forks:
		return false, "fork"
	case r.Archived && f.Archived != nil && !*f.Archived:
		return false, "archived"
	case f.MaxSizeMB > 0 && r.SizeKB > f.MaxSizeMB*1024:
		return false, "too large"
	case f.Language != "" && !strings.EqualFold(f.Language, r.Language):
		return false, "language"
	}
	if f.Name != "" {
		if ok, _ := path.Match(strings.ToLower(f.Name), strings.ToLower(r.Name)); !ok {
			return false, "name"
		}
	}
	return true, ""
}

// CloneOptions says where and how new repos are cloned.
type CloneOptions struct {
	Root    string // archive root
	TmpDir  string // <archive>/.chitragit/tmp: clones land here first
	Layout  string // e.g. {owner}/{repo} or {host}/{owner}/{repo}
	Mirror  bool   // bare mirror clone instead of a normal one
	Timeout time.Duration
	Shield  *ShieldOptions // nil: no KodeKavach gate
}

// FollowReport is what checking the followed sources did.
type FollowReport struct {
	Sources  []SourceReport `json:"sources"`
	Cloned   int            `json:"cloned"`
	Warnings []string       `json:"warnings,omitempty"`
}

// SourceReport is one source's outcome.
type SourceReport struct {
	Spec    string   `json:"spec"`
	Listed  int      `json:"listed"`
	New     int      `json:"new"`
	Cloned  []string `json:"cloned,omitempty"`
	Blocked []string `json:"blocked,omitempty"` // "owner/repo: quarantine path"
	Failed  []string `json:"failed,omitempty"`
	Problem string   `json:"problem,omitempty"`
}

// Follow checks every followed source and clones repos the archive does
// not have yet. Repos that disappear from a source are never deleted:
// their next fetch fails and the rescue flow takes over.
func Follow(ctx context.Context, opts CloneOptions, reg *provider.Registry, git *gitx.Runner, st *store.Store) (*FollowReport, error) {
	sources, err := st.Sources(ctx)
	if err != nil {
		return nil, err
	}
	rep := &FollowReport{}
	if len(sources) == 0 {
		return rep, nil
	}
	cl, err := newCloner(ctx, opts, git, st)
	if err != nil {
		return nil, err
	}
	cl.reg = reg
	for _, s := range sources {
		if ctx.Err() != nil {
			break
		}
		sr := SourceReport{Spec: s.Spec}
		problem := cl.source(ctx, reg, s, &sr)
		if problem != "" {
			sr.Problem = problem
		}
		_ = st.SourceChecked(context.WithoutCancel(ctx), s.ID, problem)
		rep.Cloned += len(sr.Cloned)
		rep.Sources = append(rep.Sources, sr)
	}
	rep.Warnings = cl.warnings
	return rep, ctx.Err()
}

func (c *cloner) source(ctx context.Context, reg *provider.Registry, row store.SourceRow, sr *SourceReport) string {
	src, err := provider.ParseSource(row.Spec)
	if err != nil {
		return err.Error()
	}
	filters, err := ParseFilters(row.Filters)
	if err != nil {
		return "unreadable filters: " + err.Error()
	}
	client := reg.For(src.Host)
	if client == nil {
		return src.Host + " is not a " + src.Kind + " server; add it under [providers] hosts in the config"
	}
	repos, err := client.List(ctx, src)
	sr.Listed = len(repos)
	if err != nil {
		return core.Message(err)
	}
	// The list answer also describes repos we already have: keep that for
	// offline use, at no extra cost (CG-32).
	meta := map[string]core.RepoMeta{}
	defer func() {
		if err := c.st.SetRepoMeta(context.WithoutCancel(ctx), meta); err != nil {
			c.warnings = append(c.warnings, core.Message(err))
		}
	}()
	for _, r := range repos {
		id, have := c.have(r)
		if id != "" {
			meta[id] = r.Meta()
		}
		if ok, _ := filters.Match(r); !ok || have {
			continue
		}
		sr.New++
		rel, err := c.clone(ctx, r)
		var be *BlockedError
		switch {
		case errors.As(err, &be) && be.Report.Stage == StageAPI:
			sr.Blocked = append(sr.Blocked, r.FullName()+": blocked before cloning, nothing downloaded")
		case errors.As(err, &be) && be.Report.Quarantine == "":
			sr.Blocked = append(sr.Blocked, r.FullName()+": over a size limit, download deleted")
		case errors.As(err, &be):
			sr.Blocked = append(sr.Blocked, r.FullName()+": "+be.Report.Quarantine)
		case err != nil:
			sr.Failed = append(sr.Failed, r.FullName()+": "+core.Message(err))
		case rel != "":
			sr.Cloned = append(sr.Cloned, rel)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return ""
}

// cloner knows which repos the archive already has.
type cloner struct {
	opts     CloneOptions
	reg      *provider.Registry // for the pre-clone scan; nil skips it
	noAPI    map[string]bool    // hosts whose API refused the pre-clone scan this run
	git      *gitx.Runner
	st       *store.Store
	known    map[string]string // lower-case host/owner/name or remote URL -> repo id ("" for a clone of this run)
	paths    map[string]bool   // lower-case archive paths in use
	warnings []string
}

func newCloner(ctx context.Context, opts CloneOptions, git *gitx.Runner, st *store.Store) (*cloner, error) {
	repos, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	c := &cloner{opts: opts, git: git, st: st, known: map[string]string{}, paths: map[string]bool{}, noAPI: map[string]bool{}}
	for _, r := range repos {
		if r.Host != "" && r.Name != "" {
			c.known[strings.ToLower(r.Host+"/"+r.Owner+"/"+r.Name)] = r.ID
		}
		c.paths[strings.ToLower(r.Path)] = true
	}
	remotes, err := st.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	for id, rs := range remotes {
		for _, u := range rs {
			c.known[strings.ToLower(strings.TrimSuffix(u, "/"))] = id
		}
	}
	// Leftovers of clones that were interrupted: ChitraGit's own temporary
	// folders, never user data. The archive lock means none is in use.
	if entries, err := os.ReadDir(opts.TmpDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "clone-") {
				_ = os.RemoveAll(filepath.Join(opts.TmpDir, e.Name()))
			}
		}
	}
	return c, nil
}

// have reports whether the archive already has r, by host/owner/name or by
// any remote URL, and its repo id when it was known before this run.
func (c *cloner) have(r provider.Repo) (id string, ok bool) {
	for _, k := range []string{r.Host + "/" + r.Owner + "/" + r.Name, r.CloneURL, r.SSHURL} {
		if id, ok := c.known[strings.ToLower(strings.TrimSuffix(k, "/"))]; k != "" && ok {
			return id, true
		}
	}
	return "", false
}

// Clone clones one repo given as a URL or provider:owner/name into the
// archive, and returns its archive path.
func Clone(ctx context.Context, target string, opts CloneOptions, reg *provider.Registry, git *gitx.Runner, st *store.Store) (string, error) {
	r, err := resolveTarget(ctx, target, reg)
	if err != nil {
		return "", err
	}
	c, err := newCloner(ctx, opts, git, st)
	if err != nil {
		return "", err
	}
	c.reg = reg
	if _, ok := c.have(r); ok {
		return "", &core.Error{Category: core.CatConfig, Message: r.FullName() + " is already in the archive"}
	}
	return c.clone(ctx, r)
}

// resolveTarget turns "github:owner/name" or a URL into a repo to clone.
func resolveTarget(ctx context.Context, target string, reg *provider.Registry) (provider.Repo, error) {
	if prefix, rest, ok := strings.Cut(target, ":"); ok && !strings.Contains(target, "://") && !strings.Contains(prefix, "@") {
		if src, err := provider.ParseSource(prefix + ":" + path.Dir(rest)); err == nil && path.Dir(rest) != "." {
			name := path.Base(rest)
			if client := reg.For(src.Host); client != nil {
				l, err := client.Lookup(ctx, src.Owner, name)
				if err != nil {
					return provider.Repo{}, err
				}
				if l.Repo == nil {
					return provider.Repo{}, &core.Error{Category: core.CatNotFound, Message: src.Host + " has no repo " + src.Owner + "/" + name}
				}
				return *l.Repo, nil
			}
		}
	}
	p := provider.Parse(target)
	if p.Host == "" || p.Name == "" {
		return provider.Repo{}, &core.Error{Category: core.CatConfig, Message: "cannot clone " + target,
			Hint: "give a URL like https://github.com/owner/repo or provider:owner/repo"}
	}
	return provider.Repo{Host: p.Host, Owner: p.Owner, Name: p.Name, CloneURL: target}, nil
}

// clone clones r into the layout path. It clones into a temporary folder
// first and renames it into place, so an interrupted clone never leaves a
// half-made repo in the archive.
func (c *cloner) clone(ctx context.Context, r provider.Repo) (string, error) {
	if err := checkCloneURL(r.CloneURL); err != nil {
		return "", err
	}
	rel, err := c.pathFor(r)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(c.opts.Root, filepath.FromSlash(rel))
	if _, err := os.Lstat(dest); err == nil {
		c.warnings = append(c.warnings, rel+" already exists on disk but is not a known repo; run `chitragit scan`")
		return "", nil
	}
	if err := os.MkdirAll(c.opts.TmpDir, 0o700); err != nil {
		return "", &core.Error{Category: core.CatPermission, Message: "cannot create " + c.opts.TmpDir, Err: err}
	}
	tmp := filepath.Join(c.opts.TmpDir, "clone-"+uuid.NewString())
	defer os.RemoveAll(tmp) // no-op after the rename

	cctx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}
	gated := c.opts.Shield != nil
	// The pre-clone scan blocks before a byte is downloaded. Anything it
	// cannot judge, or passes, goes on to the clone gate below. A mirror
	// writes no files, so the gate only warns about it, and needs no L0.
	if gated && !c.opts.Mirror && !c.noAPI[r.Host] {
		rep, err := preCheck(cctx, c.reg, r, c.opts.Shield)
		switch cat := core.CategoryOf(err); {
		case err == nil && rep.Verdict == audit.VerdictBlock:
			rep.Stage = StageAPI
			return "", c.dropOversize(ctx, r, rep)
		case cat == core.CatRateLimited || cat == core.CatAuth:
			// Leave the API's quota to listing and smart skip.
			c.noAPI[r.Host] = true
		}
	}
	args := []string{"-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=60", "clone", "--quiet"}
	switch {
	case c.opts.Mirror:
		args = append(args, "--mirror")
	case gated:
		args = append(args, "--no-checkout") // no file is written before the verdict
	}
	// A clone that grows past the size limit is stopped while it downloads.
	var over atomic.Int64
	runCtx := cctx
	if gated && c.opts.Shield.Limits.MaxRepoBytes > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithCancel(cctx)
		watchCtx, done := context.WithCancel(cctx)
		go watchSize(watchCtx, tmp, c.opts.Shield.Limits.MaxRepoBytes, sizeTick, &over, stop)
		defer done()
		defer stop()
	}
	// "--" stops a URL that starts with "-" from being read as an option.
	_, err = c.git.Run(runCtx, c.opts.TmpDir, append(args, "--", r.CloneURL, tmp)...)
	if n := over.Load(); n > 0 {
		lim := c.opts.Shield.Limits
		rep := &ShieldReport{Repo: r.FullName(), URL: r.CloneURL, Findings: lim.Check("", r.FullName(), "",
			audit.TreeStats{}, audit.Size{Bytes: n, Refs: -1})}
		return "", c.dropOversize(ctx, r, rep)
	}
	if err != nil {
		return "", err
	}
	if gated {
		if err := c.gate(cctx, tmp, r); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", &core.Error{Category: core.CatPermission, Message: "cannot create " + filepath.Dir(dest), Err: err}
	}
	if err := os.Rename(tmp, dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", nil
		}
		return "", &core.Error{Category: core.CatDisk, Message: "cannot move the new clone into place", Err: err}
	}
	c.known[strings.ToLower(r.Host+"/"+r.Owner+"/"+r.Name)] = ""
	c.paths[strings.ToLower(rel)] = true
	return rel, nil
}

// pathFor fills the layout template. Names that differ only in case from a
// path already in use get a suffix (PRD §8.1), so the archive still works
// on case-insensitive disks.
func (c *cloner) pathFor(r provider.Repo) (string, error) {
	rel := strings.NewReplacer("{host}", r.Host, "{owner}", r.Owner, "{repo}", r.Name).Replace(c.opts.Layout)
	if c.opts.Mirror && !strings.HasSuffix(rel, ".git") {
		rel += ".git"
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || part == ".git" || part == ".chitragit" ||
			strings.ContainsAny(part, `\:*?"<>|`) || strings.TrimRight(part, ". ") != part {
			return "", &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("cannot make a safe folder name for %s (%q)", r.FullName(), rel)}
		}
	}
	base := rel
	for n := 2; c.paths[strings.ToLower(rel)]; n++ {
		rel = base + "-" + fmt.Sprint(n)
		if n == 2 {
			c.warnings = append(c.warnings, base+" differs only in letter case from a repo already in the archive; cloned as "+rel)
		}
	}
	return rel, nil
}

// sizeTick is how often a clone's size is measured against the limit.
var sizeTick = 250 * time.Millisecond

// allowLocalClones lets tests clone from local folders.
var allowLocalClones = false

// checkCloneURL allows only network URLs. Provider APIs are not trusted to
// hand us file:// or ext:: URLs.
func checkCloneURL(raw string) error {
	if allowLocalClones && filepath.IsAbs(raw) {
		return nil
	}
	if !strings.Contains(raw, "://") {
		// "ext::" and "fd::" are git's command-running transports.
		if provider.Parse(raw).Host != "" && !strings.HasPrefix(raw, "-") && !strings.Contains(raw, "::") &&
			!strings.ContainsAny(raw, " \t\n") {
			return nil // scp-like SSH, git@host:owner/repo
		}
	} else if u, err := url.Parse(raw); err == nil {
		switch u.Scheme {
		case "https", "http", "ssh", "git":
			return nil
		}
	}
	return &core.Error{Category: core.CatConfig, Message: "refusing to clone from " + raw, Hint: "use an https:// or SSH URL"}
}
