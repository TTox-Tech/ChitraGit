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
	"time"

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
	// CacheFile keeps each folder's listing between walks. A folder whose
	// modification time has not changed is not read again, so a rescan
	// costs one stat per folder. Empty means no cache. Not used when
	// following symlinks.
	CacheFile string
	Full      bool // read every folder, ignoring the cache (the cache is still rewritten)
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

	start time.Time
	old   map[string]listing // from the last walk; read-only
	new   map[string]listing // this walk's, under mu
}

// Walk finds every repository under root.
func Walk(ctx context.Context, root string, opts Options) (*Result, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4 * runtime.NumCPU()
	}
	w := &walker{root: root, opts: opts, sem: make(chan struct{}, opts.Workers), seenReal: map[string]bool{},
		start: time.Now(), new: map[string]listing{}}
	cached := opts.CacheFile != "" && !opts.FollowSymlinks
	if cached && !opts.Full {
		w.old = loadCache(opts.CacheFile)
	}
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
	if cached {
		if err := saveCache(opts.CacheFile, w.new); err != nil {
			w.res.Warnings = append(w.res.Warnings, "cannot save the scan cache, so the next scan reads every folder: "+plainFSError(err))
		}
	}
	sort.Slice(w.res.Repos, func(i, j int) bool { return w.res.Repos[i].Path < w.res.Repos[j].Path })
	sort.Strings(w.res.Warnings)
	return &w.res, nil
}

// inlineDepth is how deep the walk starts a goroutine per folder even when
// the folder is unchanged. Below it, unchanged folders are walked in their
// parent's goroutine; the folders above keep the walk parallel.
const inlineDepth = 2

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
	l, hit, err := w.list(abs, rel)
	<-w.sem
	if err != nil {
		w.warn("cannot read " + rel + ": " + plainFSError(err))
		return
	}

	switch {
	case l.Git != gitNone:
		kind := w.gitEntryKind(abs, l, insideRepo)
		if kind == "" {
			return // submodule, or a .git file that cannot be read
		}
		w.add(rel, kind)
		insideRepo = true
	case l.Bare:
		w.add(rel, core.KindBare)
		return // a bare repo has no working tree to search
	}

	if w.opts.MaxDepth > 0 && depth >= w.opts.MaxDepth {
		return
	}
	children := l.Dirs
	if w.opts.FollowSymlinks {
		for _, name := range l.Links {
			if info, err := os.Stat(filepath.Join(abs, name)); err == nil && info.IsDir() {
				children = append(children, name)
			}
		}
		sort.Strings(children)
	}
	for _, name := range children {
		if rel == "." && name == ".chitragit" {
			continue
		}
		childRel := name
		if rel != "." {
			childRel = rel + "/" + name
		}
		if w.excluded(name, childRel) {
			continue
		}
		w.wg.Add(1)
		if hit && depth >= inlineDepth {
			// An unchanged folder's children cost a stat each, less than
			// starting a goroutine for them.
			w.visit(ctx, filepath.Join(abs, name), childRel, depth+1, insideRepo)
		} else {
			go w.visit(ctx, filepath.Join(abs, name), childRel, depth+1, insideRepo)
		}
	}
}

// list returns folder abs's listing: from the last walk when the folder
// has not changed since, else read from disk. Either way it is kept for
// the next walk.
func (w *walker) list(abs, rel string) (l listing, hit bool, err error) {
	info, err := os.Stat(abs)
	if err != nil {
		return listing{}, false, err
	}
	mtime := info.ModTime()
	l, hit = w.old[rel]
	if hit = hit && l.MTime != 0 && l.MTime == mtime.UnixNano(); !hit {
		if l, err = readListing(abs, mtime, w.start); err != nil {
			return listing{}, false, err
		}
	}
	w.mu.Lock()
	w.new[rel] = l
	w.mu.Unlock()
	return l, hit, nil
}

// gitEntryKind classifies a folder that contains ".git". It returns "" for
// a submodule, which is not a top-level repo.
func (w *walker) gitEntryKind(abs string, l listing, insideRepo bool) string {
	if l.Git == gitDir {
		if insideRepo {
			return core.KindNested
		}
		return core.KindNormal
	}
	if l.GitErr != "" {
		w.warn("cannot read " + filepath.Join(abs, ".git") + ": " + l.GitErr)
		return ""
	}
	// A .git file says "gitdir: <path>". Linked worktrees point into
	// <main>/.git/worktrees/<name>; submodules into <parent>/.git/modules/.
	switch gitdir := l.GitDir; {
	case strings.Contains(gitdir, "/worktrees/"):
		return core.KindWorktree
	case insideRepo || strings.Contains(gitdir, "/modules/"):
		return ""
	}
	return core.KindNormal // repo with a separate git dir (git init --separate-git-dir)
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
