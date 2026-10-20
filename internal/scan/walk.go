// Package scan discovers repositories under an archive root (PRD F1).
//
// The walker only reads the file system. It never starts git, so walking an
// archive of thousands of repos costs one directory listing per folder.
package scan

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// IgnoreFile holds extra exclude patterns, one per line, in the archive root.
const IgnoreFile = ".chitragitignore"

// Options controls the walk.
type Options struct {
	MaxDepth       int      // 0 = unlimited; depth 1 is a direct child of the root
	Exclude        []string // folder names or globs; a pattern with "/" matches the path from the root
	FollowSymlinks bool
	Workers        int // parallel directory reads; 0 = 4×CPU
}

// Found is one discovered repository.
type Found struct {
	Path string // relative to the root, '/' separated; "." for the root itself
	Kind string // core.Kind*
}

// Result is everything a walk found.
type Result struct {
	Repos    []Found
	Warnings []string // folders that could not be read, ignored patterns, ...
}

type walker struct {
	root     string
	opts     Options
	patterns []string
	sem      chan struct{}
	wg       sync.WaitGroup

	mu       sync.Mutex
	res      Result
	seenReal map[string]bool // real paths already visited, only when following symlinks
}

// Walk finds every repository under root.
func Walk(ctx context.Context, root string, opts Options) (*Result, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4 * runtime.NumCPU()
	}
	w := &walker{root: root, opts: opts, sem: make(chan struct{}, opts.Workers), seenReal: map[string]bool{}}
	w.patterns = append(w.patterns, opts.Exclude...)
	extra, warns, err := readIgnoreFile(filepath.Join(root, IgnoreFile))
	if err != nil {
		return nil, &core.Error{Category: core.CatConfig, Message: "cannot read " + IgnoreFile, Err: err}
	}
	w.patterns = append(w.patterns, extra...)
	w.res.Warnings = append(w.res.Warnings, warns...)
	for _, p := range w.patterns {
		if _, err := path.Match(p, ""); err != nil {
			return nil, &core.Error{Category: core.CatConfig, Message: "invalid exclude pattern " + p, Err: err}
		}
	}

	w.wg.Add(1)
	go w.visit(ctx, root, ".", 0, false)
	w.wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(w.res.Repos, func(i, j int) bool { return w.res.Repos[i].Path < w.res.Repos[j].Path })
	sort.Strings(w.res.Warnings)
	return &w.res, nil
}

// visit lists one folder, records it if it is a repo, and queues its children.
// insideRepo is true below a repo's working tree, where a .git folder means a
// nested repo and a .git file means a submodule.
func (w *walker) visit(ctx context.Context, abs, rel string, depth int, insideRepo bool) {
	defer w.wg.Done()
	if ctx.Err() != nil {
		return
	}
	if w.opts.FollowSymlinks && !w.firstVisit(abs) {
		return
	}

	w.sem <- struct{}{}
	entries, err := os.ReadDir(abs)
	<-w.sem
	if err != nil {
		w.warn("cannot read " + rel + ": " + plainFSError(err))
		return
	}

	names := make(map[string]fs.DirEntry, len(entries))
	for _, e := range entries {
		names[e.Name()] = e
	}

	if g, ok := names[".git"]; ok {
		switch kind := w.gitEntryKind(abs, g, insideRepo); kind {
		case "":
			return // submodule: belongs to its parent repo
		default:
			w.add(rel, kind)
			insideRepo = true
		}
	} else if isBare(names) {
		w.add(rel, core.KindBare)
		return // a bare repo has no working tree to search
	}

	if w.opts.MaxDepth > 0 && depth >= w.opts.MaxDepth {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || (rel == "." && name == ".chitragit") {
			continue
		}
		childRel := name
		if rel != "." {
			childRel = rel + "/" + name
		}
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if !w.opts.FollowSymlinks {
				continue
			}
			info, err := os.Stat(filepath.Join(abs, name))
			isDir = err == nil && info.IsDir()
		}
		if !isDir || w.excluded(name, childRel) {
			continue
		}
		w.wg.Add(1)
		go w.visit(ctx, filepath.Join(abs, name), childRel, depth+1, insideRepo)
	}
}

// gitEntryKind classifies a folder that contains ".git". It returns "" for
// a submodule, which is not a top-level repo.
func (w *walker) gitEntryKind(abs string, g fs.DirEntry, insideRepo bool) string {
	if g.IsDir() {
		if insideRepo {
			return core.KindNested
		}
		return core.KindNormal
	}
	// A .git file says "gitdir: <path>". Linked worktrees point into
	// <main>/.git/worktrees/<name>; submodules into <parent>/.git/modules/.
	data, err := os.ReadFile(filepath.Join(abs, ".git"))
	if err != nil {
		w.warn("cannot read " + filepath.Join(abs, ".git") + ": " + plainFSError(err))
		return ""
	}
	gitdir := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:")))
	switch {
	case strings.Contains(gitdir, "/worktrees/"):
		return core.KindWorktree
	case insideRepo || strings.Contains(gitdir, "/modules/"):
		return ""
	}
	return core.KindNormal // repo with a separate git dir (git init --separate-git-dir)
}

func isBare(names map[string]fs.DirEntry) bool {
	head, objects, refs := names["HEAD"], names["objects"], names["refs"]
	return head != nil && !head.IsDir() && objects != nil && objects.IsDir() && refs != nil && refs.IsDir()
}

func (w *walker) excluded(name, rel string) bool {
	for _, p := range w.patterns {
		target := name
		if strings.Contains(p, "/") {
			target, p = rel, strings.TrimPrefix(p, "/")
		}
		if ok, _ := path.Match(p, target); ok {
			return true
		}
	}
	return false
}

// firstVisit de-duplicates folders by real path, which stops symlink loops
// and double counting when symlinks are followed.
func (w *walker) firstVisit(abs string) bool {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seenReal[real] {
		return false
	}
	w.seenReal[real] = true
	return true
}

func (w *walker) add(rel, kind string) {
	w.mu.Lock()
	w.res.Repos = append(w.res.Repos, Found{Path: rel, Kind: kind})
	w.mu.Unlock()
}

func (w *walker) warn(msg string) {
	w.mu.Lock()
	w.res.Warnings = append(w.res.Warnings, msg)
	w.mu.Unlock()
}

// readIgnoreFile reads exclude patterns. Blank lines and "#" comments are
// skipped, and a trailing "/" is allowed (only folders are matched anyway).
//
// shortcut: a subset of gitignore syntax (no "!" negation, no "**"); switch
// to a full gitignore matcher if users ask for those.
func readIgnoreFile(file string) (patterns, warnings []string, err error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "!") || strings.Contains(line, "**"):
			warnings = append(warnings, IgnoreFile+": pattern "+line+" is not supported and was ignored")
		default:
			patterns = append(patterns, strings.TrimSuffix(line, "/"))
		}
	}
	return patterns, warnings, sc.Err()
}

func plainFSError(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission denied"
	}
	return err.Error()
}
