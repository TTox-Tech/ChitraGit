package conflict

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// Refs a session leaves behind so nothing is ever lost.
const (
	resolvedPrefix = "refs/chitragit/resolved/" // your original changes (the stash), after a finished session
	abortedPrefix  = "refs/chitragit/aborted/"  // the working tree as it was when a session was aborted
)

// Session is an open conflict-resolution session in one repo.
type Session struct {
	git  *gitx.Runner
	dir  string
	flow *syncer.Flow
	// Done is set when the session ended on its own because nothing
	// clashed any more; the update and your changes are both in place.
	Done bool
}

// ErrNothingToResolve means the repo has no update waiting to be applied.
var ErrNothingToResolve = &core.Error{Category: core.CatConflict,
	Message: "there is no update waiting for this repo", Hint: "run `chitragit sync` first"}

// Open returns the session open in the repo at dir, or nil.
func Open(ctx context.Context, git *gitx.Runner, dir string) (*Session, error) {
	f, err := syncer.OpenFlow(ctx, git, dir)
	if err != nil || f == nil || f.Kind != syncer.FlowResolve || f.Phase != syncer.PhaseResolving {
		return nil, err
	}
	return &Session{git: git, dir: dir, flow: f}, nil
}

// Start opens a session: it puts your changes aside, applies the update
// already downloaded for the current branch, and brings your changes back
// on top, leaving every clash in the working tree. Nothing is fetched.
//
// If a session is already open, Start returns it. If nothing clashes, the
// session finishes at once and Done is set.
func Start(ctx context.Context, git *gitx.Runner, dir string) (*Session, error) {
	if s, err := Open(ctx, git, dir); s != nil || err != nil {
		return s, err
	}
	out, err := git.Output(ctx, dir, "rev-parse", "--absolute-git-dir", "--is-bare-repository")
	if err != nil {
		return nil, err
	}
	gitDir, bare, _ := strings.Cut(out, "\n")
	if bare == "true" {
		return nil, &core.Error{Category: core.CatUnsafeState, Message: "a bare repo has no working tree, so it has no conflicts"}
	}
	if why := syncer.Busy(gitDir); why != "" {
		return nil, &core.Error{Category: core.CatUnsafeState, Message: why}
	}
	// An update interrupted earlier is rolled back first, as a sync would.
	if f, err := syncer.OpenFlow(ctx, git, dir); err != nil {
		return nil, err
	} else if f != nil {
		if err := f.Rollback(ctx); err != nil {
			return nil, err
		}
	}
	if u, _ := git.Output(ctx, dir, "ls-files", "--unmerged"); u != "" {
		return nil, &core.Error{Category: core.CatUnsafeState, Message: "this repo already has unresolved conflicts of its own",
			Hint: "finish or undo them with git first"}
	}

	target, err := upstreamTarget(ctx, git, dir)
	if err != nil {
		return nil, err
	}
	status, err := git.Output(ctx, dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if status == "" {
		// No local changes: a plain fast-forward cannot clash.
		_, err := git.Run(ctx, dir, "merge", "--ff-only", "--quiet", "--no-stat", target)
		return &Session{git: git, dir: dir, Done: true}, err
	}

	f, err := syncer.BeginFlow(ctx, git, dir, syncer.Marker{Kind: syncer.FlowResolve, Phase: syncer.PhaseStarting, Target: target})
	if err != nil {
		return nil, err
	}
	s := &Session{git: git, dir: dir, flow: f}
	if f.StashSHA == "" {
		_, err := git.Run(ctx, dir, "merge", "--ff-only", "--quiet", "--no-stat", target)
		s.Done = true
		return s, err
	}
	if err := s.layOut(ctx); err != nil {
		if rerr := f.Rollback(ctx); rerr != nil {
			return nil, rerr
		}
		return nil, err
	}
	files, err := s.Files(ctx)
	if err != nil {
		return s, err
	}
	if !slices.ContainsFunc(files, func(f File) bool { return !f.Resolved }) {
		if err := s.Finish(ctx); err != nil {
			return s, err
		}
		s.Done = true
	}
	return s, nil
}

// upstreamTarget returns the commit the current branch should move to. The
// branch must be strictly behind its upstream.
func upstreamTarget(ctx context.Context, git *gitx.Runner, dir string) (string, error) {
	branch, err := git.Output(ctx, dir, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return "", &core.Error{Category: core.CatUnsafeState, Message: "detached HEAD: there is no branch to update"}
	}
	upstream, err := git.Output(ctx, dir, "for-each-ref", "--format=%(upstream)", branch)
	if err != nil {
		return "", err
	}
	if upstream == "" {
		return "", &core.Error{Category: core.CatUnsafeState, Message: "the current branch does not track a remote branch"}
	}
	target, err := git.Output(ctx, dir, "rev-parse", "-q", "--verify", upstream+"^{commit}")
	if err != nil {
		return "", &core.Error{Category: core.CatUnsafeState, Message: "the branch it tracks no longer exists upstream"}
	}
	counts, err := git.Output(ctx, dir, "rev-list", "--left-right", "--count", "HEAD..."+target)
	if err != nil {
		return "", err
	}
	ahead, behind, _ := strings.Cut(counts, "\t")
	switch {
	case ahead != "0":
		return "", &core.Error{Category: core.CatUnsafeState,
			Message: "this branch has local commits as well as new upstream commits; merge or rebase it with git"}
	case behind == "0":
		return "", ErrNothingToResolve
	}
	return target, nil
}

// layOut applies the update and re-applies the stash with clashes left in
// the working tree, then records the session as resolving. Any error here
// is rolled back by the caller; a crash is rolled back by the next sync.
func (s *Session) layOut(ctx context.Context) error {
	f := s.flow
	clashes, err := s.clashes(ctx)
	if err != nil {
		return err
	}
	if _, err := s.git.Run(ctx, s.dir, "merge", "--ff-only", "--quiet", "--no-stat", f.Target); err != nil {
		return err
	}
	step("start.updated")
	stash := f.StashSHA
	if len(clashes) > 0 {
		if stash, err = s.stashWithout(ctx, clashes); err != nil {
			return err
		}
	}
	// Without --index: the result becomes plain uncommitted changes. diff3
	// markers show the lines both sides started from.
	_, applyErr := s.git.Run(ctx, s.dir, "-c", "merge.conflictStyle=diff3", "stash", "apply", "--quiet", stash)
	step("start.applied")
	unmerged, err := s.unmerged(ctx)
	if err != nil {
		return err
	}
	if applyErr != nil && len(unmerged) == 0 {
		return applyErr
	}
	for _, p := range clashes {
		if err := s.layOutClash(ctx, p); err != nil {
			return err
		}
	}
	f.Files = append(slices.Collect(maps.Keys(unmerged)), clashes...)
	slices.Sort(f.Files)
	f.Clashes = clashes
	f.Phase = syncer.PhaseResolving
	return f.Save()
}

// clashes lists your untracked files that the update adds too. git cannot
// put those back by itself, so they are laid out by the session. A file
// that would have to become a folder (or the reverse) cannot be laid out.
func (s *Session) clashes(ctx context.Context) ([]string, error) {
	untracked, err := s.lsTree(ctx, s.flow.StashSHA+"^3")
	if err != nil || len(untracked) == 0 {
		return nil, err
	}
	theirs, err := s.lsTree(ctx, s.flow.Target)
	if err != nil {
		return nil, err
	}
	inTheirs := map[string]bool{}
	dirs := map[string]bool{}
	for _, p := range theirs {
		inTheirs[p] = true
		for d := p; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			dirs[d] = true
		}
	}
	var out []string
	for _, p := range untracked {
		if dirs[p] {
			return nil, folderClash(p)
		}
		for d := p; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			if inTheirs[d] {
				return nil, folderClash(d)
			}
		}
		if inTheirs[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func folderClash(p string) error {
	return &core.Error{Category: core.CatConflict,
		Message: "the update turns " + p + " into a folder or a file where you have the opposite; resolve it with git by hand"}
}

// lsTree lists the files of a commit; empty if rev does not exist.
func (s *Session) lsTree(ctx context.Context, rev string) ([]string, error) {
	if _, err := s.git.Run(ctx, s.dir, "rev-parse", "-q", "--verify", rev+"^{commit}"); err != nil {
		return nil, nil
	}
	res, err := s.git.Run(ctx, s.dir, "ls-tree", "-r", "-z", "--name-only", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	return splitZ(res.Stdout), nil
}

// stashWithout builds a copy of the stash commit whose untracked part
// leaves out paths. It works on a scratch index; the real one is untouched.
func (s *Session) stashWithout(ctx context.Context, paths []string) (string, error) {
	stash := s.flow.StashSHA
	tmp, err := os.MkdirTemp("", "chitragit-index-")
	if err != nil {
		return "", &core.Error{Category: core.CatDisk, Message: "cannot create a temporary folder", Err: err}
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := s.git.RunEnv(ctx, s.dir, env, "", "read-tree", stash+"^3"); err != nil {
		return "", err
	}
	if _, err := s.git.RunEnv(ctx, s.dir, env, strings.Join(paths, "\x00")+"\x00",
		"update-index", "--force-remove", "-z", "--stdin"); err != nil {
		return "", err
	}
	tree, err := s.out(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	msg, err := s.git.Output(ctx, s.dir, "log", "-1", "--format=%B", stash)
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", stash + "^{tree}", "-p", stash + "^1", "-p", stash + "^2", "-m", msg}
	if rest, err := s.git.Output(ctx, s.dir, "ls-tree", tree); err != nil {
		return "", err
	} else if rest != "" {
		u, err := s.git.Output(ctx, s.dir, "commit-tree", tree, "-m", "untracked files")
		if err != nil {
			return "", err
		}
		args = append(args, "-p", u)
	}
	return s.git.Output(ctx, s.dir, args...)
}

func (s *Session) out(ctx context.Context, env []string, args ...string) (string, error) {
	res, err := s.git.RunEnv(ctx, s.dir, env, "", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// stages maps a conflicted path to its index stages: 1 = the common
// ancestor, 2 = upstream, 3 = yours.
type stages map[int]string

// unmerged reads the conflicted entries of the index.
func (s *Session) unmerged(ctx context.Context) (map[string]stages, error) {
	res, err := s.git.Run(ctx, s.dir, "ls-files", "--unmerged", "-z")
	if err != nil {
		return nil, err
	}
	m := map[string]stages{}
	for _, e := range splitZ(res.Stdout) {
		info, path, ok := strings.Cut(e, "\t")
		f := strings.Fields(info)
		if !ok || len(f) != 3 || len(f[2]) != 1 {
			continue
		}
		if m[path] == nil {
			m[path] = stages{}
		}
		m[path][int(f[2][0]-'0')] = f[1]
	}
	return m, nil
}

// layOutClash puts both versions of a clashing untracked file into it,
// with conflict markers, using git merge-file against an empty base. A
// binary clash keeps upstream's file until the user picks a side.
func (s *Session) layOutClash(ctx context.Context, p string) error {
	theirs, err := os.ReadFile(s.abs(p))
	if err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot read " + p, Err: err}
	}
	// --filters gives the file as it was on disk (line endings included).
	res, err := s.git.Run(ctx, s.dir, "cat-file", "--filters", s.flow.StashSHA+"^3:"+p)
	if err != nil {
		return err
	}
	mine := res.Stdout
	if isBinary(theirs) || isBinary(mine) {
		return nil
	}
	tmp, err := os.MkdirTemp("", "chitragit-merge-")
	if err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot create a temporary folder", Err: err}
	}
	defer os.RemoveAll(tmp)
	base, minePath := filepath.Join(tmp, "base"), filepath.Join(tmp, "mine")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write a temporary file", Err: err}
	}
	if err := os.WriteFile(minePath, mine, 0o600); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write a temporary file", Err: err}
	}
	res, err = s.git.Run(ctx, s.dir, "merge-file", "-p", "--diff3",
		"-L", "Updated upstream", "-L", "Stash base", "-L", "Stashed changes", s.abs(p), base, minePath)
	if err != nil && (res == nil || res.ExitCode <= 0 || res.ExitCode >= 128) {
		return err // a positive exit code below 128 is the number of conflicts
	}
	return writeFile(s.abs(p), res.Stdout)
}

func (s *Session) abs(p string) string { return filepath.Join(s.dir, filepath.FromSlash(p)) }

// Files lists every file that clashed, resolved or not.
func (s *Session) Files(ctx context.Context) ([]File, error) {
	um, err := s.unmerged(ctx)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(s.flow.Files))
	for _, p := range s.flow.Files {
		f := File{Path: p, Kind: s.kind(ctx, p, um[p])}
		f.Resolved = f.Kind == ""
		if f.Kind == KindContent || f.Kind == KindAddedBoth {
			if b, err := os.ReadFile(s.abs(p)); err == nil {
				segs, _ := Parse(b)
				f.Hunks = len(Hunks(segs))
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// kind tells what kind of conflict p still has, or "" once it is resolved.
func (s *Session) kind(ctx context.Context, p string, st stages) string {
	switch {
	case slices.Contains(s.flow.Clashes, p):
		if b, err := os.ReadFile(s.abs(p)); err == nil && isBinary(b) {
			return KindBinary
		}
		return KindAddedBoth
	case st == nil:
		return ""
	case st[3] == "":
		return KindDeletedMine
	case st[2] == "":
		return KindDeletedTheirs
	}
	for _, n := range []int{2, 3} {
		if res, err := s.git.Run(ctx, s.dir, "cat-file", "blob", st[n]); err == nil && isBinary(res.Stdout) {
			return KindBinary
		}
	}
	return KindContent
}

// file returns the unresolved conflicted file p, or a usage error.
func (s *Session) file(ctx context.Context, p string) (File, stages, error) {
	um, err := s.unmerged(ctx)
	if err != nil {
		return File{}, nil, err
	}
	if !slices.Contains(s.flow.Files, p) {
		return File{}, nil, &core.Error{Category: core.CatConfig, Message: p + " is not one of the conflicted files",
			Hint: "run `chitragit conflicts show <repo>` to list them"}
	}
	f := File{Path: p, Kind: s.kind(ctx, p, um[p])}
	if f.Kind == "" {
		return f, nil, &core.Error{Category: core.CatConfig, Message: p + " is already resolved"}
	}
	return f, um[p], nil
}

// Hunks returns the unresolved hunks of a conflicted text file.
func (s *Session) Hunks(ctx context.Context, p string) ([]Hunk, error) {
	if _, _, err := s.file(ctx, p); err != nil {
		return nil, err
	}
	segs, err := s.parse(p)
	if err != nil {
		return nil, err
	}
	return Hunks(segs), nil
}

func (s *Session) parse(p string) ([]Segment, error) {
	b, err := os.ReadFile(s.abs(p))
	if err != nil {
		return nil, &core.Error{Category: core.CatDisk, Message: "cannot read " + p, Err: err}
	}
	segs, err := Parse(b)
	if err != nil {
		return nil, &core.Error{Category: core.CatConflict, Message: p + ": " + err.Error(),
			Hint: "fix the file in your editor, then mark it resolved"}
	}
	return segs, nil
}

// ResolveHunk settles hunk i (0-based, among the hunks still unresolved)
// of a text file. When no hunk is left, the file is marked resolved.
func (s *Session) ResolveHunk(ctx context.Context, p string, i int, c Choice) error {
	if _, _, err := s.file(ctx, p); err != nil {
		return err
	}
	segs, err := s.parse(p)
	if err != nil {
		return err
	}
	if !Resolve(segs, i, c) {
		return &core.Error{Category: core.CatConfig, Message: "there is no hunk " + itoa(i+1) + " in " + p}
	}
	if err := writeFile(s.abs(p), Render(segs)); err != nil {
		return err
	}
	if len(Hunks(segs)) == 0 {
		return s.markResolved(ctx, p, false)
	}
	return nil
}

// ResolveFile settles a whole file. Both works only for text files.
func (s *Session) ResolveFile(ctx context.Context, p string, c Choice) error {
	f, st, err := s.file(ctx, p)
	if err != nil {
		return err
	}
	if c == Both {
		if f.Kind != KindContent && f.Kind != KindAddedBoth {
			return &core.Error{Category: core.CatConfig, Message: "keeping both sides works only for text files; pick mine or theirs for " + p}
		}
		segs, err := s.parse(p)
		if err != nil {
			return err
		}
		for Resolve(segs, 0, Both) {
		}
		if err := writeFile(s.abs(p), Render(segs)); err != nil {
			return err
		}
		return s.markResolved(ctx, p, false)
	}

	clash := f.Kind == KindAddedBoth || (f.Kind == KindBinary && st == nil)
	var args []string
	switch {
	case clash && c == Mine:
		args = []string{"checkout", s.flow.StashSHA + "^3", "--", p}
	case clash:
		args = []string{"checkout", s.flow.Target, "--", p}
	case c == Mine && st[3] != "":
		args = []string{"checkout", "--theirs", "--", p} // in a stash apply, "theirs" is the stash
	case c == Theirs && st[2] != "":
		args = []string{"checkout", "--ours", "--", p}
	}
	if args == nil {
		// The chosen side deleted the file.
		if err := os.Remove(s.abs(p)); err != nil && !os.IsNotExist(err) {
			return &core.Error{Category: core.CatDisk, Message: "cannot delete " + p, Err: err}
		}
	} else if _, err := s.git.Run(ctx, s.dir, args...); err != nil {
		return err
	}
	return s.markResolved(ctx, p, false)
}

// MarkResolved marks a file resolved after it was fixed by hand, in an
// editor or a merge tool. It refuses while conflict markers are left.
func (s *Session) MarkResolved(ctx context.Context, p string) error {
	if _, _, err := s.file(ctx, p); err != nil {
		return err
	}
	return s.markResolved(ctx, p, true)
}

func (s *Session) markResolved(ctx context.Context, p string, check bool) error {
	if b, err := os.ReadFile(s.abs(p)); err == nil {
		if check {
			if segs, err := Parse(b); err != nil || len(Hunks(segs)) > 0 {
				return &core.Error{Category: core.CatConflict, Message: p + " still has conflict markers",
					Hint: "remove every <<<<<<<, |||||||, ======= and >>>>>>> block first"}
			}
		}
		_, err = s.git.Run(ctx, s.dir, "add", "--", p)
		if err != nil {
			return err
		}
	} else if _, err := s.git.Run(ctx, s.dir, "rm", "-q", "--cached", "--ignore-unmatch", "--", p); err != nil {
		return err
	}
	if i := slices.Index(s.flow.Clashes, p); i >= 0 {
		s.flow.Clashes = slices.Delete(s.flow.Clashes, i, i+1)
		return s.flow.Save()
	}
	return nil
}

// Finish ends the session once every file is resolved. Your changes,
// merged with the update, stay in the working tree as uncommitted changes,
// as before the sync. Your original changes are kept under
// refs/chitragit/resolved/ in case a resolution was wrong.
func (s *Session) Finish(ctx context.Context) error {
	files, err := s.Files(ctx)
	if err != nil {
		return err
	}
	var open []string
	for _, f := range files {
		if !f.Resolved {
			open = append(open, f.Path)
		}
	}
	if len(open) > 0 {
		return &core.Error{Category: core.CatConflict, Message: plural(len(open), "file") + " still to resolve: " + strings.Join(open, ", ")}
	}
	if _, err := s.git.Run(ctx, s.dir, "reset", "-q"); err != nil {
		return err
	}
	step("finish.unstaged")
	return s.flow.Finish(ctx, resolvedPrefix+syncer.Stamp())
}

// Abort puts the repo back exactly as it was before the session. Whatever
// the working tree held at that moment, hand edits included, is first
// saved as a commit under refs/chitragit/aborted/.
func (s *Session) Abort(ctx context.Context) error {
	if err := s.saveWorkTree(ctx); err != nil {
		return err
	}
	step("abort.saved")
	return s.flow.Rollback(ctx)
}

// Abort ends whatever session or interrupted update the repo at dir has,
// putting it back as it was. It reports false when there was none.
func Abort(ctx context.Context, git *gitx.Runner, dir string) (bool, error) {
	if s, err := Open(ctx, git, dir); err != nil {
		return false, err
	} else if s != nil {
		return true, s.Abort(ctx)
	}
	f, err := syncer.OpenFlow(ctx, git, dir)
	if err != nil || f == nil {
		return false, err
	}
	return true, f.Rollback(ctx)
}

// saveWorkTree commits the whole working tree (except ignored files) on a
// scratch copy of the index and keeps it under refs/chitragit/aborted/.
func (s *Session) saveWorkTree(ctx context.Context) error {
	tmp, err := os.MkdirTemp("", "chitragit-index-")
	if err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot create a temporary folder", Err: err}
	}
	defer os.RemoveAll(tmp)
	index := filepath.Join(tmp, "index")
	// Starting from a copy of the real index keeps git's file-stat cache,
	// so only changed files are read again.
	if b, err := os.ReadFile(filepath.Join(s.flow.GitDir, "index")); err == nil {
		if err := os.WriteFile(index, b, 0o600); err != nil {
			return &core.Error{Category: core.CatDisk, Message: "cannot write a temporary file", Err: err}
		}
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := s.git.RunEnv(ctx, s.dir, env, "", "add", "-A"); err != nil {
		return err
	}
	tree, err := s.out(ctx, env, "write-tree")
	if err != nil {
		return err
	}
	commit, err := s.git.Output(ctx, s.dir, "commit-tree", tree, "-p", "HEAD", "-m", "chitragit: working tree when conflict resolution was aborted")
	if err != nil {
		return err
	}
	_, err = s.git.Run(ctx, s.dir, "update-ref", abortedPrefix+syncer.Stamp(), commit)
	return err
}

// writeFile replaces a file through a temporary file and a rename, so a
// crash never leaves it half written. The file keeps its permissions.
func writeFile(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".chitragit-*")
	if err == nil {
		_, err = tmp.Write(data)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(tmp.Name(), mode)
		}
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}
	if err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write " + filepath.Base(path), Err: err}
	}
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func step(name string) { syncer.Step(name) }

func splitZ(b []byte) []string {
	var out []string
	for _, p := range strings.Split(string(b), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// BackupRef is the ref that holds HEAD as it was before the session.
func (s *Session) BackupRef() string {
	if s.flow == nil {
		return ""
	}
	return s.flow.Backup
}

// StashName is the message of the stash entry holding your changes.
func (s *Session) StashName() string {
	if s.flow == nil {
		return ""
	}
	return s.flow.Stash
}
