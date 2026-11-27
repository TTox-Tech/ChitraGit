package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// DefaultMaxPatch is how many patch bytes a diff keeps when DiffOptions
// sets no cap: enough for any commit a person reads, small enough that a
// vendored-dependency commit cannot fill memory.
const DefaultMaxPatch = 4 << 20

// DiffOptions shapes Diff and Commit.
type DiffOptions struct {
	NoPatch  bool  // the file list and line counts only
	MaxBytes int64 // patch bytes kept in all; 0 means DefaultMaxPatch
}

// FileChange is one file a diff touches.
type FileChange struct {
	Status  string `json:"status"` // A added, M modified, D deleted, R renamed, C copied, T type changed
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // renames and copies
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
	Patch   string `json:"patch,omitempty"` // from the "diff --git" line on
}

// DiffResult is what changed between two trees. When Truncated, the patches
// stopped at the byte cap; the file list and counts are still complete.
type DiffResult struct {
	Files     []FileChange `json:"files"`
	Truncated bool         `json:"truncated,omitempty"`
}

// diffFlags make every diff plain and safe to show: no external diff
// program or textconv filter a repo's config or attributes could name,
// rename detection on, no colour.
var diffFlags = []string{"--no-ext-diff", "--no-textconv", "--no-color", "-M"}

// Diff lists what changed from base to head (commits, trees or anything
// that names one), with per-file patches unless opts.NoPatch.
func (r *Runner) Diff(ctx context.Context, dir, base, head string, opts DiffOptions) (*DiffResult, error) {
	for _, rev := range []string{base, head} {
		if err := checkRev(rev); err != nil {
			return nil, err
		}
	}
	args := append([]string{"diff", "--raw", "--numstat", "-z"}, diffFlags...)
	res, err := r.Run(ctx, dir, append(args, base, head, "--")...)
	if err != nil {
		return nil, err
	}
	files, err := parseRawNumstat(res.Stdout)
	if err != nil {
		return nil, err
	}
	out := &DiffResult{Files: files}
	if opts.NoPatch || len(files) == 0 {
		return out, nil
	}
	max := opts.MaxBytes
	if max <= 0 {
		max = DefaultMaxPatch
	}
	s, err := r.Stream(ctx, dir, "", append(append([]string{"diff", "-p"}, diffFlags...), base, head, "--")...)
	if err != nil {
		return nil, err
	}
	patches, truncated, rerr := splitPatches(io.LimitReader(s, max+1), max)
	if cerr := s.Close(); rerr == nil && !truncated {
		rerr = cerr
	}
	if rerr != nil {
		return nil, rerr
	}
	// git prints patches in the same order as the raw list.
	for i := range out.Files {
		if i < len(patches) {
			out.Files[i].Patch = patches[i]
		}
	}
	out.Truncated = truncated
	return out, nil
}

// parseRawNumstat reads `git diff --raw --numstat -z`: every raw record
// (":<modes> <shas> <status>\0<path>\0[<new path>\0]"), then one numstat
// record per file ("<added>\t<deleted>\t<path>\0", or with an empty path
// followed by "<old>\0<new>\0" for a rename).
func parseRawNumstat(b []byte) ([]FileChange, error) {
	fields := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	if len(fields) == 1 && fields[0] == "" {
		return nil, nil
	}
	bad := errors.New("unexpected git diff output")
	var files []FileChange
	i := 0
	for i < len(fields) && strings.HasPrefix(fields[i], ":") {
		meta := strings.Fields(fields[i])
		if len(meta) < 5 || i+1 >= len(fields) {
			return nil, bad
		}
		f := FileChange{Status: meta[4][:1], Path: fields[i+1]}
		i += 2
		if f.Status == "R" || f.Status == "C" {
			if i >= len(fields) {
				return nil, bad
			}
			f.OldPath, f.Path = f.Path, fields[i]
			i++
		}
		files = append(files, f)
	}
	for n := range files {
		if i >= len(fields) {
			return nil, bad
		}
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			return nil, bad
		}
		i++
		if parts[2] == "" { // rename: the two paths follow
			i += 2
		}
		if parts[0] == "-" {
			files[n].Binary = true
			continue
		}
		a, err1 := strconv.Atoi(parts[0])
		d, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return nil, bad
		}
		files[n].Added, files[n].Deleted = a, d
	}
	return files, nil
}

// splitPatches cuts a `git diff -p` stream into one patch per file. It
// reads at most max bytes; truncated reports that more was left, and the
// last patch then ends where the cap fell.
func splitPatches(rd io.Reader, max int64) (patches []string, truncated bool, err error) {
	br := bufio.NewReader(rd)
	var cur bytes.Buffer
	var read int64
	for {
		line, err := br.ReadBytes('\n')
		read += int64(len(line))
		if read > max {
			line = line[:len(line)-int(read-max)]
			truncated = true
		}
		if bytes.HasPrefix(line, []byte("diff --git ")) && cur.Len() > 0 {
			patches = append(patches, cur.String())
			cur.Reset()
		}
		cur.Write(line)
		if truncated || err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, err
		}
	}
	if cur.Len() > 0 {
		patches = append(patches, cur.String())
	}
	return patches, truncated, nil
}

// Commit is one commit's header.
type Commit struct {
	SHA            string   `json:"sha"`
	Tree           string   `json:"tree"`
	Parents        []string `json:"parents"`
	Author         string   `json:"author"`
	AuthorEmail    string   `json:"author_email"`
	AuthorDate     string   `json:"author_date"` // ISO 8601
	Committer      string   `json:"committer"`
	CommitterEmail string   `json:"committer_email"`
	CommitDate     string   `json:"commit_date"`
	Message        string   `json:"message"`
}

// CommitDetail is a commit with what it changed: against its first parent
// for a merge, as GitHub shows it, and against nothing for a root commit.
type CommitDetail struct {
	Commit
	DiffResult
}

// CommitInfo reads the header of the commit rev names.
func (r *Runner) CommitInfo(ctx context.Context, dir, rev string) (*Commit, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	const format = "%H%x00%T%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B"
	out, err := r.Output(ctx, dir, "-c", "log.showSignature=false", "show", "-s", "--no-patch", "--format="+format, rev+"^{commit}", "--")
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(out, "\x00", 10)
	if len(f) != 10 {
		return nil, &core.Error{Category: core.CatInternal, Message: "unexpected git show output", Detail: rev}
	}
	c := &Commit{SHA: f[0], Tree: f[1], Parents: strings.Fields(f[2]), Author: f[3], AuthorEmail: f[4], AuthorDate: f[5],
		Committer: f[6], CommitterEmail: f[7], CommitDate: f[8], Message: strings.TrimRight(f[9], "\n")}
	if c.Parents == nil {
		c.Parents = []string{}
	}
	return c, nil
}

// CommitDiff reads a commit and what it changed.
func (r *Runner) CommitDiff(ctx context.Context, dir, rev string, opts DiffOptions) (*CommitDetail, error) {
	c, err := r.CommitInfo(ctx, dir, rev)
	if err != nil {
		return nil, err
	}
	base := ""
	if len(c.Parents) > 0 {
		base = c.Parents[0]
	} else if base, err = r.emptyTree(ctx, dir); err != nil {
		return nil, err
	}
	d, err := r.Diff(ctx, dir, base, c.SHA, opts)
	if err != nil {
		return nil, err
	}
	if d.Files == nil {
		d.Files = []FileChange{}
	}
	return &CommitDetail{Commit: *c, DiffResult: *d}, nil
}

// emptyTree names the empty tree in the repo's own hash, so a root commit
// diffs against nothing in SHA-1 and SHA-256 repos alike.
func (r *Runner) emptyTree(ctx context.Context, dir string) (string, error) {
	res, err := r.RunInput(ctx, dir, "", "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// Compare counts the commits only base has (behind) and only head has
// (ahead), and finds their merge base ("" for unrelated histories).
func (r *Runner) Compare(ctx context.Context, dir, base, head string) (ahead, behind int, mergeBase string, err error) {
	for _, rev := range []string{base, head} {
		if err := checkRev(rev); err != nil {
			return 0, 0, "", err
		}
	}
	out, err := r.Output(ctx, dir, "rev-list", "--left-right", "--count", base+"..."+head, "--")
	if err != nil {
		return 0, 0, "", err
	}
	if _, err := fmt.Sscanf(out, "%d\t%d", &behind, &ahead); err != nil {
		return 0, 0, "", &core.Error{Category: core.CatInternal, Message: "unexpected git rev-list output", Detail: out}
	}
	res, err := r.Run(ctx, dir, "merge-base", base, head)
	switch {
	case err == nil:
		mergeBase = strings.TrimSpace(string(res.Stdout))
	case res != nil && res.ExitCode == 1: // no common history
	default:
		return 0, 0, "", err
	}
	return ahead, behind, mergeBase, nil
}
