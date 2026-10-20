// Package syncer updates repositories (PRD F2): a worker pool runs the
// per-repo pipeline preflight → fetch → integrate → record.
//
// The pipeline never loses data. Local changes go through the safe stash
// flow (safety.go), which either succeeds or puts the repo back exactly as
// it was. History that a fetch would drop (branches deleted upstream, tips
// replaced by a force-push) is kept under refs/chitragit/. When a repo
// needs a decision (diverged branches, local commits on a rewritten
// branch), it downloads the updates, leaves branches alone and says why.
package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// Outcomes of one repo.
const (
	Updated   = "updated"   // at least one ref changed
	Unchanged = "unchanged" // nothing new upstream
	Skipped   = "skipped"   // not touched on purpose; Reason says why
	Failed    = "failed"    // an error; Category and Reason say what
)

// Statuses stored on the repo after a sync.
const (
	StatusOK          = "ok"
	StatusAhead       = "ahead"         // local commits not pushed; nothing to apply
	StatusDirty       = "dirty"         // uncommitted changes; updates downloaded, not applied
	StatusDiverged    = "diverged"      // local and upstream both have new commits
	StatusRewritten   = "rewritten"     // upstream force-pushed; local branch left as is
	StatusDetached    = "detached"      // detached HEAD: fetch only
	StatusNoUpstream  = "no_upstream"   // current branch tracks nothing: fetch only
	StatusUpstreamGon = "upstream_gone" // tracked branch no longer exists upstream
	StatusEmpty       = "empty"         // no commits on the current branch yet
	StatusBusy        = "busy"          // merge/rebase in progress or git is running
	StatusBlocked     = "blocked"       // the update would overwrite local files
	StatusConflict    = "conflict"      // local changes clash with the update; rolled back
	StatusRecovered   = "recovered"     // an interrupted update was undone this run
	StatusLocalOnly   = "local_only"    // no remotes
	StatusWorktree    = "worktree"      // synced through its main repo
	StatusFailed      = "failed"
)

// inProgress are files in the git dir that mean the user is in the middle
// of something. ChitraGit never interferes with that.
var inProgress = map[string]string{
	"MERGE_HEAD":       "a merge is in progress",
	"rebase-merge":     "a rebase is in progress",
	"rebase-apply":     "a rebase or `git am` is in progress",
	"CHERRY_PICK_HEAD": "a cherry-pick is in progress",
	"REVERT_HEAD":      "a revert is in progress",
	"BISECT_LOG":       "a bisect is in progress",
	"index.lock":       "another git process is using this repo (index.lock exists)",
}

// repoSync holds one repo's pipeline state.
type repoSync struct {
	git    *gitx.Runner
	dir    string
	opts   *Options
	res    *Result
	gitDir string
	stop   context.Context                                 // cancelled on Ctrl+C: no more retries
	fetch  func(ctx context.Context, args ...string) error // fetch with retries
}

func (s *repoSync) run(ctx context.Context) {
	r := s.res
	if r.Repo.Kind == core.KindWorktree {
		s.skip(StatusWorktree, "linked worktree; it is updated through its main repo")
		return
	}

	remotes, err := s.remotes(ctx)
	if err != nil {
		s.fail(err)
		return
	}
	r.Remotes = remotes
	if len(remotes) == 0 {
		s.skip(StatusLocalOnly, "no remote configured; nothing to sync")
		return
	}

	// Ask git rather than trusting the stored kind: pruning a bare mirror
	// would be wrong, so this must be right even if the folder changed.
	out, err := s.out(ctx, "rev-parse", "--absolute-git-dir", "--is-bare-repository")
	if err != nil {
		s.fail(err)
		return
	}
	gitDir, isBare, _ := strings.Cut(out, "\n")
	s.gitDir = gitDir
	bare := isBare == "true"
	if !bare {
		for file, why := range inProgress {
			if _, err := os.Stat(filepath.Join(gitDir, file)); err == nil {
				s.skip(StatusBusy, why)
				return
			}
		}
		recovered, err := s.recover(ctx, gitDir)
		if err != nil {
			s.fail(err)
			return
		}
		if recovered {
			s.skip(StatusRecovered, "a previous update was interrupted; the repo was put back exactly as it was and will update on the next sync")
			return
		}
	}

	before, err := s.refs(ctx)
	if err != nil {
		s.fail(err)
		return
	}

	if s.opts.Mode == "dry-run" {
		s.dryRun(ctx, remotes, before)
		return
	}

	if err := s.fetch(ctx, s.fetchArgs(remotes, bare)...); err != nil {
		s.fail(err)
		return
	}
	if err := s.protect(ctx, before, !s.opts.Prune); err != nil {
		s.fail(err)
		return
	}

	r.Status = StatusOK
	if !bare && s.opts.Mode == "ff" {
		if err := s.integrate(ctx, before); err != nil {
			if core.CategoryOf(err) == core.CatUnsafeState {
				r.Outcome, r.Status, r.Reason, r.Category = Skipped, StatusBlocked, plain(err), string(core.CatUnsafeState)
				s.recordChanges(ctx, before)
				return
			}
			s.fail(err)
			s.recordChanges(ctx, before)
			return
		}
	}
	s.recordChanges(ctx, before)
}

func (s *repoSync) fetchArgs(remotes map[string]string, bare bool) []string {
	// Automatic gc stays off during the fetch so that objects a prune or a
	// force-push makes unreachable are still there for protect to keep.
	args := []string{"-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=60",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"fetch", "--quiet", "--tags", "--no-write-fetch-head"}
	if bare {
		// A mirror's refspec (+refs/*:refs/*) would let a prune delete
		// refs/chitragit/ itself, so bare repos are never pruned.
		args = append(args, "--no-prune")
	} else {
		// Deleted upstream branches are archived by protect unless the user
		// asked for plain pruning; tags fetched with --tags are never pruned.
		args = append(args, "--prune")
	}
	if s.opts.Submodules {
		args = append(args, "--recurse-submodules=on-demand")
	} else {
		args = append(args, "--recurse-submodules=no")
	}
	if _, ok := remotes["origin"]; s.opts.FetchAllRemotes || !ok {
		return append(args, "--all")
	}
	return append(args, "origin")
}

// integrate fast-forwards local branches to their upstream when that is
// safe. Anything else is reported through the result's status.
func (s *repoSync) integrate(ctx context.Context, before map[string]string) error {
	r := s.res
	branches, err := s.branches(ctx)
	if err != nil {
		return err
	}
	res, err := s.git.Run(ctx, s.dir, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		if res != nil && res.ExitCode == 1 && len(res.Stderr) == 0 {
			r.Status, r.Reason = StatusDetached, "detached HEAD: downloaded updates, did not move any branch"
			return s.fastForwardOthers(ctx, branches, "", before)
		}
		return err
	}
	current := strings.TrimSpace(string(res.Stdout))

	if err := s.integrateCurrent(ctx, current, branches[current], before); err != nil {
		return err
	}
	return s.fastForwardOthers(ctx, branches, current, before)
}

func (s *repoSync) integrateCurrent(ctx context.Context, current string, b branch, before map[string]string) error {
	r := s.res
	switch {
	case b.upstream == "":
		r.Status, r.Reason = StatusNoUpstream, "the current branch does not track a remote branch; downloaded updates only"
		return nil
	case b.upstreamSHA == "":
		r.Status, r.Reason = StatusUpstreamGon, "the branch it tracks ("+shortRef(b.upstream)+") no longer exists upstream"
		return nil
	case b.sha == "":
		r.Status, r.Reason = StatusEmpty, "the current branch has no commits yet"
		return nil
	case b.sha == b.upstreamSHA:
		return nil
	}

	ahead, behind, err := s.aheadBehind(ctx, b.sha, b.upstreamSHA)
	if err != nil {
		return err
	}
	switch {
	case behind == 0:
		r.Status, r.Reason = StatusAhead, plural(ahead, "local commit")+" not pushed yet"
		return nil
	case ahead > 0:
		old := before[b.upstream]
		if old != "" && old != b.upstreamSHA && !s.isAncestor(ctx, old, b.upstreamSHA) {
			return s.followRewrite(ctx, b, old)
		}
		r.Status, r.Reason = StatusDiverged, "you have "+plural(ahead, "local commit")+" and upstream has "+
			plural(behind, "new commit")+"; nothing was merged"
		return nil
	}

	tracked, untracked, err := s.changes(ctx)
	if err != nil {
		return err
	}
	switch {
	case (tracked || untracked) && s.opts.StashLocalChanges:
		return s.stashUpdate(ctx, s.gitDir, b.upstreamSHA, behind)
	case tracked:
		r.Status, r.Reason = StatusDirty, "has uncommitted changes; downloaded "+plural(behind, "new commit")+
			" but did not apply them (sync.stash_local_changes is off)"
		return nil
	}
	_, err = s.git.Run(ctx, s.dir, "merge", "--ff-only", "--quiet", "--no-stat", b.upstreamSHA)
	return err
}

// followRewrite handles an upstream force-push on the current branch. The
// old upstream tip is already saved under refs/chitragit/rewritten/. If the
// branch has nothing of its own beyond that old tip and the tree is clean,
// it follows the new history; otherwise it is left for the user.
func (s *repoSync) followRewrite(ctx context.Context, b branch, oldUpstream string) error {
	r := s.res
	tracked, untracked, err := s.changes(ctx)
	if err != nil {
		return err
	}
	if b.sha != oldUpstream || tracked || untracked {
		r.Status, r.Reason = StatusRewritten, "upstream rewrote its history (force-push) and this branch has local work; it was left as it was"
		return nil
	}
	if _, err := s.git.Run(ctx, s.dir, "reset", "--keep", "--quiet", b.upstreamSHA); err != nil {
		return err
	}
	r.Reason = "followed an upstream force-push"
	return nil
}

// changes reports uncommitted changes to tracked files and untracked
// (not ignored) files.
func (s *repoSync) changes(ctx context.Context) (tracked, untracked bool, err error) {
	out, err := s.out(ctx, "status", "--porcelain")
	if err != nil {
		return false, false, err
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "??"):
			untracked = true
		case line != "":
			tracked = true
		}
	}
	return tracked, untracked, nil
}

// fastForwardOthers moves local branches that are not checked out to their
// upstream, without touching any working tree, when they are strictly behind.
func (s *repoSync) fastForwardOthers(ctx context.Context, branches map[string]branch, current string, before map[string]string) error {
	var candidates []branch
	for name, b := range branches {
		if name != current && b.sha != "" && b.upstreamSHA != "" && b.sha != b.upstreamSHA {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	used, err := s.checkedOut(ctx)
	if err != nil {
		return err
	}
	for _, b := range candidates {
		// Strictly behind, or sitting exactly at the old tip of a rewritten
		// upstream (already saved by protect).
		movable := s.isAncestor(ctx, b.sha, b.upstreamSHA) || b.sha == before[b.upstream]
		if used[b.ref] || !movable {
			continue
		}
		// update-ref with the old value is atomic: it fails if the branch
		// moved since we looked.
		if _, err := s.git.Run(ctx, s.dir, "update-ref", "-m", "chitragit: fast-forward", b.ref, b.upstreamSHA, b.sha); err != nil {
			return err
		}
	}
	return nil
}

// dryRun compares the remote's refs with the local copies without
// downloading anything.
func (s *repoSync) dryRun(ctx context.Context, remotes map[string]string, local map[string]string) {
	r := s.res
	names := []string{"origin"}
	if _, ok := remotes["origin"]; s.opts.FetchAllRemotes || !ok {
		names = names[:0]
		for n := range remotes {
			names = append(names, n)
		}
	}
	pending := 0
	for _, remote := range names {
		var out string
		err := s.withRetry(ctx, func(ctx context.Context) error {
			var err error
			out, err = s.out(ctx, "ls-remote", "--heads", "--tags", remote)
			return err
		})
		if err != nil {
			s.fail(err)
			return
		}
		for _, line := range strings.Split(out, "\n") {
			sha, ref, ok := strings.Cut(line, "\t")
			if !ok || strings.HasSuffix(ref, "^{}") {
				continue
			}
			localRef := ref
			if b, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				localRef = "refs/remotes/" + remote + "/" + b
			}
			if local[localRef] != sha {
				pending++
			}
		}
	}
	r.Status = StatusOK
	if pending == 0 {
		r.Outcome = Unchanged
		return
	}
	r.Outcome, r.Reason = Updated, "would download "+plural(pending, "changed ref")
}

// recordChanges diffs the refs against the snapshot taken before the sync.
func (s *repoSync) recordChanges(ctx context.Context, before map[string]string) {
	r := s.res
	after, err := s.refs(ctx)
	if err != nil {
		if r.Outcome == "" {
			s.fail(err)
		}
		return
	}
	for ref, sha := range after {
		old, ok := before[ref]
		switch {
		case !ok:
			r.Changes = append(r.Changes, core.RefChange{Ref: ref, NewSHA: sha, Kind: "create"})
		case old != sha:
			c := core.RefChange{Ref: ref, OldSHA: old, NewSHA: sha, Kind: "update"}
			if !strings.HasPrefix(ref, "refs/tags/") {
				if s.isAncestor(ctx, old, sha) {
					c.Commits, _ = s.count(ctx, old+".."+sha)
				} else {
					c.Kind = "rewrite"
				}
			}
			r.Changes = append(r.Changes, c)
		}
	}
	for ref, old := range before {
		if _, ok := after[ref]; !ok {
			r.Changes = append(r.Changes, core.RefChange{Ref: ref, OldSHA: old, Kind: "delete"})
		}
	}
	if r.Outcome == "" {
		r.Outcome = Unchanged
		if len(r.Changes) > 0 {
			r.Outcome = Updated
		}
	}
	if len(r.Notes) > 0 {
		r.Reason = strings.Join(append(nonEmpty(r.Reason), r.Notes...), "; ")
	}
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// ---- small git helpers ----

func (s *repoSync) out(ctx context.Context, args ...string) (string, error) {
	return s.git.Output(ctx, s.dir, args...)
}

func (s *repoSync) remotes(ctx context.Context) (map[string]string, error) {
	res, err := s.git.Run(ctx, s.dir, "config", "--null", "--get-regexp", `^remote\..*\.url$`)
	out := map[string]string{}
	if err != nil {
		if res != nil && res.ExitCode == 1 && len(res.Stderr) == 0 {
			return out, nil
		}
		return nil, err
	}
	for _, entry := range strings.Split(string(res.Stdout), "\x00") {
		if k, v, ok := strings.Cut(entry, "\n"); ok {
			out[strings.TrimSuffix(strings.TrimPrefix(k, "remote."), ".url")] = v
		}
	}
	return out, nil
}

// refs returns every branch, remote-tracking ref and tag with its SHA.
func (s *repoSync) refs(ctx context.Context) (map[string]string, error) {
	out, err := s.out(ctx, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(line, " "); ok && !strings.HasSuffix(ref, "/HEAD") {
			m[ref] = sha
		}
	}
	return m, nil
}

type branch struct {
	ref, sha, upstream, upstreamSHA string
}

// branches lists local branches with their upstream. An upstream that no
// longer exists has an empty upstreamSHA.
func (s *repoSync) branches(ctx context.Context) (map[string]branch, error) {
	out, err := s.out(ctx, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(upstream)", "refs/heads")
	if err != nil {
		return nil, err
	}
	refs, err := s.refs(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]branch{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 3 {
			continue
		}
		m[f[0]] = branch{ref: f[0], sha: f[1], upstream: f[2], upstreamSHA: refs[f[2]]}
	}
	return m, nil
}

func (s *repoSync) checkedOut(ctx context.Context) (map[string]bool, error) {
	out, err := s.out(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if ref, ok := strings.CutPrefix(line, "branch "); ok {
			m[ref] = true
		}
	}
	return m, nil
}

func (s *repoSync) isAncestor(ctx context.Context, a, b string) bool {
	_, err := s.git.Run(ctx, s.dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func (s *repoSync) count(ctx context.Context, rangeSpec string) (int, error) {
	out, err := s.out(ctx, "rev-list", "--count", rangeSpec)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func (s *repoSync) aheadBehind(ctx context.Context, local, upstream string) (ahead, behind int, err error) {
	out, err := s.out(ctx, "rev-list", "--left-right", "--count", local+"..."+upstream)
	if err != nil {
		return 0, 0, err
	}
	l, r, _ := strings.Cut(out, "\t")
	ahead, err1 := strconv.Atoi(l)
	behind, err2 := strconv.Atoi(r)
	return ahead, behind, errors.Join(err1, err2)
}

func (s *repoSync) skip(status, reason string) {
	s.res.Outcome, s.res.Status, s.res.Reason = Skipped, status, reason
}

func (s *repoSync) fail(err error) {
	s.res.Outcome, s.res.Status, s.res.Reason = Failed, StatusFailed, plain(err)
	s.res.Category = string(core.CategoryOf(err))
	s.res.Err = err
}

// plain returns the user-facing message of err.
func plain(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func shortRef(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/remotes/"), "refs/heads/")
}
