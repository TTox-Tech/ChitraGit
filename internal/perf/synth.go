// Package perf builds synthetic archives for the performance budget
// (PRD §11: scan and sync budgets for 5,000 repos).
//
// A few dozen real repos are made with git, each with its own history,
// branches and tags, and pushed to a bare upstream outside the archive.
// The archive is then filled by copying those templates, so 5,000 repos
// take seconds to lay out instead of minutes of git calls. Every copy is a
// complete, independent repo whose origin is its template's upstream, so
// scan identifies it and sync fetches it like any other plain-URL repo.
package perf

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Options shapes a synthetic archive. The zero value of each field takes
// the default in its comment.
type Options struct {
	Repos     int    // repos in the archive; default 5000
	Templates int    // distinct template repos; default 40
	Seed      uint64 // same seed, same archive; default 1
	Workers   int    // parallel copies; default GOMAXPROCS
	Git       string // git binary; default "git" on PATH
}

// Mix is how many repos of each kind an archive holds.
type Mix struct {
	Normal, Bare, Mirror, Nested, Dirty int
}

// Archive is a generated archive.
type Archive struct {
	Root      string   // the archive folder: scan this
	Upstreams []string // one bare repo per template, outside Root
	Paths     []string // every repo, relative to Root, slash-separated
	Mix       Mix
}

// Kinds by share of the archive, per 1,000 repos. The rest are normal
// clones. Dirty repos are normal clones with an uncommitted change.
const (
	barePerMille   = 30
	mirrorPerMille = 10
	nestedPerMille = 10
	dirtyPerMille  = 50
)

// Generate lays out a synthetic archive under dir: dir/archive holds the
// repos and dir/upstream their bare remotes. dir must be empty or absent.
func Generate(ctx context.Context, dir string, opts Options) (*Archive, error) {
	if opts.Repos <= 0 {
		opts.Repos = 5000
	}
	if opts.Templates <= 0 {
		opts.Templates = 40
	}
	opts.Templates = min(opts.Templates, opts.Repos)
	if opts.Seed == 0 {
		opts.Seed = 1
	}
	if opts.Workers <= 0 {
		opts.Workers = runtime.GOMAXPROCS(0)
	}
	if opts.Git == "" {
		opts.Git = "git"
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("perf: %s is not empty", dir)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	a := &Archive{Root: filepath.Join(dir, "archive")}
	tmplDir := filepath.Join(dir, "templates")
	for _, d := range []string{a.Root, tmplDir, filepath.Join(dir, "upstream")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	g := &gen{git: opts.Git, rng: rand.New(rand.NewPCG(opts.Seed, 0x5eed))}
	clones := make([]string, opts.Templates) // working clone of each template
	mirrors := make([]string, opts.Templates)
	for i := range opts.Templates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		up := filepath.Join(dir, "upstream", fmt.Sprintf("t%03d.git", i))
		clones[i] = filepath.Join(tmplDir, fmt.Sprintf("t%03d", i))
		mirrors[i] = filepath.Join(tmplDir, fmt.Sprintf("t%03d.git", i))
		if err := g.template(ctx, i, up, clones[i], mirrors[i]); err != nil {
			return nil, fmt.Errorf("perf: template %d: %w", i, err)
		}
		a.Upstreams = append(a.Upstreams, up)
	}

	jobs := g.plan(opts.Repos, opts.Templates)
	for _, j := range jobs {
		a.Paths = append(a.Paths, j.rel)
		switch j.kind {
		case kindBare:
			a.Mix.Bare++
		case kindMirror:
			a.Mix.Mirror++
		case kindNested:
			a.Mix.Nested++
		case kindDirty:
			a.Mix.Dirty++
		default:
			a.Mix.Normal++
		}
	}

	// Parents go first: a nested repo is copied into a repo that exists.
	var first, nested []job
	for _, j := range jobs {
		if j.kind == kindNested {
			nested = append(nested, j)
		} else {
			first = append(first, j)
		}
	}
	for _, batch := range [][]job{first, nested} {
		if err := runAll(ctx, opts.Workers, batch, func(j job) error {
			return place(a.Root, j, clones[j.tmpl], mirrors[j.tmpl])
		}); err != nil {
			return nil, err
		}
	}
	if err := os.RemoveAll(tmplDir); err != nil {
		return nil, err
	}
	return a, nil
}

type kind int

const (
	kindNormal kind = iota
	kindBare
	kindMirror
	kindNested
	kindDirty
)

type job struct {
	rel  string // slash-separated, relative to the archive root
	tmpl int
	kind kind
}

type gen struct {
	git string
	rng *rand.Rand
}

// plan names every repo and picks its template and kind. Owners hold
// between 1 and about 200 repos, like a real mix of orgs and users.
func (g *gen) plan(n, templates int) []job {
	jobs := make([]job, 0, n)
	owner, left := 0, 0
	var normals []int // indexes of normal repos, parents for nested ones
	for i := range n {
		if left == 0 {
			owner++
			left = 1 + int(g.rng.ExpFloat64()*25)
		}
		left--
		j := job{rel: fmt.Sprintf("owner-%04d/repo-%05d", owner, i), tmpl: g.rng.IntN(templates)}
		switch r := g.rng.IntN(1000); {
		case r < barePerMille:
			j.kind, j.rel = kindBare, j.rel+".git"
		case r < barePerMille+mirrorPerMille:
			j.kind, j.rel = kindMirror, j.rel+".git"
		case r < barePerMille+mirrorPerMille+nestedPerMille && len(normals) > 0:
			parent := jobs[normals[g.rng.IntN(len(normals))]]
			j.kind, j.rel = kindNested, fmt.Sprintf("%s/vendor/dep-%05d", parent.rel, i)
		case r < barePerMille+mirrorPerMille+nestedPerMille+dirtyPerMille:
			j.kind = kindDirty
		default:
			normals = append(normals, len(jobs))
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// template makes one upstream with its own history, a working clone of
// it and a mirror clone. Sizes vary so repos are not all alike: 1 to 30
// commits, 0 to 12 extra branches, 0 to 5 tags.
func (g *gen) template(ctx context.Context, i int, upstream, clone, mirror string) error {
	src := clone + ".src"
	if err := g.run(ctx, "", "init", "-q", "--template=", "-b", "main", src); err != nil {
		return err
	}
	commits := 1 + g.rng.IntN(30)
	for c := range commits {
		name := fmt.Sprintf("src/file-%02d.txt", g.rng.IntN(8))
		body := strings.Repeat(fmt.Sprintf("template %d commit %d\n", i, c), 1+g.rng.IntN(60))
		if err := writeFile(filepath.Join(src, filepath.FromSlash(name)), body); err != nil {
			return err
		}
		if err := g.commit(ctx, src, c, fmt.Sprintf("change %d of template %d", c, i)); err != nil {
			return err
		}
	}
	for b := range g.rng.IntN(13) {
		if err := g.run(ctx, src, "branch", fmt.Sprintf("feature-%02d", b), fmt.Sprintf("HEAD~%d", g.rng.IntN(commits))); err != nil {
			return err
		}
	}
	for t := range g.rng.IntN(6) {
		if err := g.run(ctx, src, "tag", fmt.Sprintf("v0.%d.0", t), fmt.Sprintf("HEAD~%d", g.rng.IntN(commits))); err != nil {
			return err
		}
	}
	// --no-local packs the objects and --template= leaves out the sample
	// hooks, so each copy in the archive is a few dozen files, not hundreds.
	clone1 := func(args ...string) error {
		return g.run(ctx, "", append([]string{"clone", "-q", "--no-local", "--template="}, args...)...)
	}
	if err := clone1("--bare", src, upstream); err != nil {
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	if err := clone1(upstream, clone); err != nil {
		return err
	}
	return clone1("--mirror", upstream, mirror)
}

// commit commits everything with a fixed author and date, so the same
// seed always gives the same object IDs.
func (g *gen) commit(ctx context.Context, dir string, n int, msg string) error {
	if err := g.run(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	date := fmt.Sprintf("2026-01-01T00:%02d:%02dZ", n/60, n%60)
	cmd := g.cmd(ctx, dir, "commit", "-q", "--no-verify", "-m", msg)
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	return runCmd(cmd)
}

func (g *gen) run(ctx context.Context, dir string, args ...string) error {
	return runCmd(g.cmd(ctx, dir, args...))
}

func (g *gen) cmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	// The generator only runs git on repos it made itself.
	cmd := exec.CommandContext(ctx, g.git, append([]string{"-c", "core.hooksPath=", "-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false"}, args...)...) // nosemgrep: dangerous-exec-command
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=perf", "GIT_AUTHOR_EMAIL=perf@example.com",
		"GIT_COMMITTER_NAME=perf", "GIT_COMMITTER_EMAIL=perf@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func runCmd(cmd *exec.Cmd) error {
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(cmd.Args[1:], " "), err, out)
	}
	return nil
}

// place copies a template into the archive as one repo.
func place(root string, j job, clone, mirror string) error {
	dest := filepath.Join(root, filepath.FromSlash(j.rel))
	switch j.kind {
	case kindBare, kindMirror:
		// A plain bare repo has no remote; a mirror keeps its origin.
		if err := copyTree(mirror, dest); err != nil {
			return err
		}
		if j.kind == kindBare {
			return dropRemote(filepath.Join(dest, "config"))
		}
		return nil
	case kindDirty:
		if err := copyTree(clone, dest); err != nil {
			return err
		}
		return writeFile(filepath.Join(dest, "src", "local-notes.txt"), "work in progress\n")
	default:
		return copyTree(clone, dest)
	}
}

// dropRemote removes the [remote "origin"] section from a git config.
func dropRemote(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var out []string
	skip := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "[") {
			skip = strings.HasPrefix(line, `[remote "origin"]`)
		}
		if !skip {
			out = append(out, line)
		}
	}
	return os.WriteFile(file, []byte(strings.Join(out, "\n")), 0o644)
}

// copyTree copies a folder of regular files. Template repos hold no
// symlinks; anything else is an error rather than a silent skip.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(path, target)
		default:
			return fmt.Errorf("perf: unexpected file type at %s", path)
		}
	})
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// runAll runs fn over jobs on n workers and returns the first error.
func runAll(ctx context.Context, n int, jobs []job, fn func(job) error) error {
	ch := make(chan job)
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if err := fn(j); err != nil {
					once.Do(func() { first = err; cancel() })
				}
			}
		}()
	}
outer:
	for _, j := range jobs {
		select {
		case ch <- j:
		case <-ctx.Done():
			break outer
		}
	}
	close(ch)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
