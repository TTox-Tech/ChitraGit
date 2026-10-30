package engine

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// This file reads what the Repo Detail page shows (PRD F8). Every call is
// read-only: it looks at git objects and refs, never at or into the working
// tree, so it is safe while a sync runs and works the same for bare repos.

// RepoDir is the folder of a repo inside the archive.
func RepoDir(root string, r core.Repo) string {
	return filepath.Join(root, filepath.FromSlash(r.Path))
}

// Commit is one line of `git log --graph`. Graph-only lines (the joins and
// forks between commits) have an empty SHA.
type Commit struct {
	Graph   string   `json:"graph,omitempty"`
	SHA     string   `json:"sha,omitempty"`
	Author  string   `json:"author,omitempty"`
	Date    string   `json:"date,omitempty"` // ISO 8601 committer date
	Subject string   `json:"subject,omitempty"`
	Refs    []string `json:"refs,omitempty"` // branch and tag names pointing here
}

// Commits returns up to limit commits reachable from rev (HEAD when empty),
// with the ASCII graph. Refs under refs/chitragit/ are hidden from the
// decorations, but their commits stay reachable through --all if asked.
func Commits(ctx context.Context, git *gitx.Runner, dir, rev string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 200
	}
	if rev == "" {
		rev = "HEAD"
	}
	if empty, err := noCommits(ctx, git, dir, rev); empty || err != nil {
		return nil, err
	}
	const sep = "\x1f"
	out, err := git.Output(ctx, dir, "log", "--graph", "--no-color", "--decorate=full",
		"--decorate-refs-exclude=refs/chitragit/", "--max-count="+strconv.Itoa(limit),
		"--format="+sep+"%H"+sep+"%an"+sep+"%cI"+sep+"%D"+sep+"%s", rev, "--")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		graph, rest, ok := strings.Cut(line, sep)
		c := Commit{Graph: strings.TrimRight(graph, " ")}
		if ok {
			f := strings.SplitN(rest, sep, 5)
			if len(f) == 5 {
				c.SHA, c.Author, c.Date, c.Subject = f[0], f[1], f[2], f[4]
				c.Refs = decorations(f[3])
				c.Graph = strings.TrimRight(graph, " ")
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// decorations turns %D output ("HEAD -> refs/heads/main, tag: refs/tags/v1")
// into short names.
func decorations(d string) []string {
	if d == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(d, ", ") {
		p = strings.TrimPrefix(p, "tag: ")
		if head, target, ok := strings.Cut(p, " -> "); ok {
			out = append(out, head)
			p = target
		}
		out = append(out, shortRef(p))
	}
	return out
}

func shortRef(ref string) string {
	for _, p := range []string{"refs/heads/", "refs/remotes/", "refs/tags/"} {
		if strings.HasPrefix(ref, p) {
			return strings.TrimPrefix(ref, p)
		}
	}
	return ref
}

// Ref kinds for the Branches & Tags tab.
const (
	RefBranch    = "branch"    // local branch
	RefRemote    = "remote"    // remote-tracking branch
	RefTag       = "tag"       // tag
	RefArchived  = "archived"  // deleted upstream, kept by ChitraGit
	RefRewritten = "rewritten" // replaced by an upstream force-push, kept by ChitraGit
	RefBackup    = "backup"    // safety point of an unfinished stash flow
)

// Ref is one branch or tag.
type Ref struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"` // short name, e.g. main, origin/main, v1.0
	Full     string `json:"ref"`
	SHA      string `json:"sha"`
	Date     string `json:"date,omitempty"` // committer date of the tip (tagger date for annotated tags)
	Upstream string `json:"upstream,omitempty"`
	Track    string `json:"track,omitempty"` // e.g. "ahead 2, behind 1", "gone"
	Current  bool   `json:"current,omitempty"`
}

// Refs lists branches, tags and the history ChitraGit has kept, newest
// first within each kind.
func Refs(ctx context.Context, git *gitx.Runner, dir string) ([]Ref, error) {
	const sep = "\x1f"
	out, err := git.Output(ctx, dir, "for-each-ref", "--sort=-creatordate",
		"--format=%(refname)"+sep+"%(objectname)"+sep+"%(creatordate:iso-strict)"+sep+
			"%(upstream:short)"+sep+"%(upstream:track,nobracket)"+sep+"%(HEAD)",
		"refs/heads/", "refs/remotes/", "refs/tags/", "refs/chitragit/")
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, sep)
		if len(f) != 6 {
			continue
		}
		r := Ref{Full: f[0], SHA: f[1], Date: f[2], Upstream: f[3], Track: f[4], Current: f[5] == "*"}
		switch {
		case strings.HasPrefix(r.Full, "refs/heads/"):
			r.Kind, r.Name = RefBranch, strings.TrimPrefix(r.Full, "refs/heads/")
		case strings.HasPrefix(r.Full, "refs/remotes/"):
			r.Kind, r.Name = RefRemote, strings.TrimPrefix(r.Full, "refs/remotes/")
			if strings.HasSuffix(r.Name, "/HEAD") {
				continue
			}
		case strings.HasPrefix(r.Full, "refs/tags/"):
			r.Kind, r.Name = RefTag, strings.TrimPrefix(r.Full, "refs/tags/")
		default:
			kind, name, _ := strings.Cut(strings.TrimPrefix(r.Full, "refs/chitragit/"), "/")
			switch kind {
			case "archived":
				r.Kind = RefArchived
			case "rewritten":
				r.Kind = RefRewritten
			case "backup":
				r.Kind = RefBackup
			default:
				continue
			}
			r.Name = name
		}
		refs = append(refs, r)
	}
	order := []string{RefBranch, RefRemote, RefTag, RefArchived, RefRewritten, RefBackup}
	slices.SortStableFunc(refs, func(a, b Ref) int {
		return slices.Index(order, a.Kind) - slices.Index(order, b.Kind)
	})
	return refs, nil
}

// MaxReadme caps how much of a README is read; huge files are cut.
const MaxReadme = 512 << 10

// Readme is a repo's README as committed at HEAD.
type Readme struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Markdown  bool   `json:"markdown"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ReadReadme finds the README at the top of HEAD's tree. It reads the
// committed file, not the working tree, so it works for bare repos and
// never follows a symlink out of the repo. Returns nil when there is none.
func ReadReadme(ctx context.Context, git *gitx.Runner, dir string) (*Readme, error) {
	if empty, err := noCommits(ctx, git, dir, "HEAD"); empty || err != nil {
		return nil, err
	}
	// Default format, as --format needs git 2.36: "<mode> <type> <sha>\t<name>".
	out, err := git.Output(ctx, dir, "ls-tree", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	type entry struct{ name, sha string }
	var best *entry
	bestRank := 1 << 30
	for _, e := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[0] != "100644" && f[0] != "100755" {
			continue // only regular files: no symlinks, no submodules
		}
		sha := f[2]
		if rank := readmeRank(name); rank < bestRank {
			bestRank, best = rank, &entry{name, sha}
		}
	}
	if best == nil {
		return nil, nil
	}
	res, err := git.Run(ctx, dir, "cat-file", "blob", best.sha)
	if err != nil {
		return nil, err
	}
	data := res.Stdout
	rd := &Readme{Name: best.name}
	if len(data) > MaxReadme {
		data, rd.Truncated = data[:MaxReadme], true
	}
	rd.Text = strings.ToValidUTF8(string(data), "�")
	ext := strings.ToLower(path.Ext(best.name))
	rd.Markdown = ext == ".md" || ext == ".markdown" || ext == ".mdown"
	return rd, nil
}

// readmeRank prefers README.md, then other README variants; it returns a
// large number for files that are not READMEs.
func readmeRank(name string) int {
	lower := strings.ToLower(name)
	base := strings.TrimSuffix(lower, path.Ext(lower))
	if base != "readme" && lower != "readme" {
		return 1 << 30
	}
	switch path.Ext(lower) {
	case ".md":
		return 0
	case ".markdown", ".mdown":
		return 1
	case "":
		return 2
	case ".txt", ".rst", ".adoc", ".org":
		return 3
	}
	return 4
}

// noCommits reports whether HEAD is unborn (a repo with no commits yet),
// which is a normal state, not an error. Other revisions must exist.
func noCommits(ctx context.Context, git *gitx.Runner, dir, rev string) (bool, error) {
	if rev != "HEAD" {
		return false, nil
	}
	res, err := git.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil && res != nil && res.ExitCode == 1 {
		return true, nil
	}
	return false, err
}

// MeasureSize adds up the bytes of every file under a repo's folder,
// including .git, without following symlinks. Nested repos are counted in
// their parent too, as they are on disk.
func MeasureSize(ctx context.Context, dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// MeasureSizes measures each repo and stores the result, for the disk
// usage card. It stops early when ctx is cancelled; sizes already measured
// are kept.
func MeasureSizes(ctx context.Context, root string, repos []core.Repo, st *store.Store) error {
	for _, r := range repos {
		if r.Missing || r.Kind == core.KindWorktree {
			continue
		}
		n, err := MeasureSize(ctx, RepoDir(root, r))
		if err != nil {
			return err
		}
		if err := st.SetSize(ctx, r.ID, n); err != nil {
			return err
		}
	}
	return nil
}
