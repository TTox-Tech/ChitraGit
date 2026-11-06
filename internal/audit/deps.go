package audit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// CheckDeps finds dependencies with known vulnerabilities.
const CheckDeps = "deps"

// DepsOptions controls one repo's dependency check.
type DepsOptions struct {
	RepoID      string
	RepoPath    string // relative to the archive, for reports
	Dir         string // absolute path git runs in (works for bare repos too)
	MaxFileSize int64  // skip lockfiles larger than this; 0 = no limit
}

// DepsResult is what one repo's dependency check found.
type DepsResult struct {
	Findings  []Finding
	Head      string   // the commit whose lockfiles were read; "" for an empty repo
	Lockfiles int      // lockfiles read
	Packages  int      // distinct package versions checked
	Skipped   int      // lockfiles skipped because they were too large
	Problems  []string // lockfiles that could not be parsed
}

// ScanDeps reads the lockfiles at HEAD, straight from the object store
// (so bare repos work and no work tree is touched), and matches every
// pinned version against the local vulnerability database. Nothing is
// sent anywhere.
func ScanDeps(ctx context.Context, git *gitx.Runner, db *VulnDB, o DepsOptions) (*DepsResult, error) {
	if db == nil {
		return nil, ErrNoVulnDB
	}
	res := &DepsResult{}
	head, files, skipped, err := headFiles(ctx, git, o.Dir, IsLockfile, o.MaxFileSize)
	if err != nil {
		return nil, err
	}
	res.Head, res.Skipped = head, skipped
	type hit struct {
		pkg  Package
		vuln Vuln
	}
	var hits []hit
	checked := map[string]bool{}
	for _, f := range files {
		pkgs, err := ParseLockfile(f.path, f.data)
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %v", f.path, err))
			continue
		}
		res.Lockfiles++
		for _, p := range pkgs {
			ok, err := db.Has(ctx, p.Ecosystem)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("%w for %s (needed for %s); run `chitragit audit db update`", ErrNoVulnDB, p.Ecosystem, p.File)
			}
			checked[p.Ecosystem+"\x00"+p.Name+"\x00"+p.Version] = true
			vs, err := db.Vulns(ctx, p)
			if err != nil {
				return nil, err
			}
			for _, v := range mergeAliases(vs) {
				hits = append(hits, hit{p, v})
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	res.Packages = len(checked)
	for _, h := range hits {
		res.Findings = append(res.Findings, depFinding(o, h.pkg, h.vuln))
	}
	slices.SortFunc(res.Findings, func(a, b Finding) int {
		if c := b.Severity.Rank() - a.Severity.Rank(); c != 0 {
			return c
		}
		return strings.Compare(a.File+a.Title, b.File+b.Title)
	})
	return res, nil
}

// mergeAliases folds advisories that describe the same vulnerability
// (GitHub's GHSA, the ecosystem's own database and the CVE often all have
// a record) into one, so each problem is reported once. The kept record
// is the most severe; its Rule is the group's CVE, or its smallest ID, so
// the finding keeps its ID when a database adds another alias.
func mergeAliases(vs []Vuln) []Vuln {
	parent := make([]int, len(vs))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	owner := map[string]int{}
	for i, v := range vs {
		for _, id := range append([]string{v.ID}, v.Aliases...) {
			if j, ok := owner[id]; ok {
				parent[find(i)] = find(j)
			} else {
				owner[id] = i
			}
		}
	}
	groups := map[int][]int{}
	var roots []int
	for i := range vs {
		r := find(i)
		if _, ok := groups[r]; !ok {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], i)
	}
	out := make([]Vuln, 0, len(roots))
	for _, r := range roots {
		members := groups[r]
		best := vs[members[0]]
		var ids, fixes []string
		for _, i := range members {
			v := vs[i]
			ids = append(ids, v.ID)
			ids = append(ids, v.Aliases...)
			fixes = append(fixes, v.Fixed...)
			if v.Severity.Rank() > best.Severity.Rank() || (v.Severity == best.Severity && v.Score > best.Score) ||
				(v.Severity == best.Severity && v.Score == best.Score && len(best.Fixed) == 0 && len(v.Fixed) > 0) {
				best = v
			}
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		best.Key = cveAlias(ids)
		if best.Key == "" {
			best.Key = ids[0]
		}
		best.Aliases = slices.DeleteFunc(ids, func(id string) bool { return id == best.ID })
		if len(best.Fixed) == 0 {
			best.Fixed = fixes // a fix one database knows of is a fix
		}
		out = append(out, best)
	}
	return out
}

func depFinding(o DepsOptions, p Package, v Vuln) Finding {
	// The ID leaves out the version, so moving from one affected version
	// to another keeps the finding (and any ignore) instead of opening a
	// new one.
	key := Fingerprint(p.File + "\x00" + p.Ecosystem + "\x00" + normalizeName(p.Ecosystem, p.Name))
	summary := v.Summary
	if summary == "" {
		summary = "known vulnerability"
	}
	preview := p.Name + "@" + p.Version
	if alias := cveAlias(v.Aliases); alias != "" {
		preview += " · " + alias
	}
	if v.Score > 0 {
		preview += " · CVSS " + formatScore(v.Score)
	}
	return Finding{
		ID:          FindingID(o.RepoID, CheckDeps, v.key(), key),
		RepoID:      o.RepoID,
		RepoPath:    o.RepoPath,
		Check:       CheckDeps,
		Rule:        v.key(),
		Title:       p.Name + " " + p.Version + ": " + summary,
		Severity:    v.Severity,
		Confidence:  ConfidenceHigh,
		File:        p.File,
		Preview:     preview,
		Occurrences: 1,
		Hint:        depHint(p, v),
		Status:      StatusOpen,
	}
}

func depHint(p Package, v Vuln) string {
	more := " Details: https://osv.dev/vulnerability/" + v.ID
	if strings.HasPrefix(v.ID, "MAL-") {
		return "This " + p.Ecosystem + " package version is known malware. Remove it, and treat any machine that installed it as compromised." + more
	}
	if len(v.Fixed) > 0 {
		return fmt.Sprintf("Upgrade %s to %s or later.%s", p.Name, v.Fixed[0], more)
	}
	return "No fixed version is published yet; consider replacing " + p.Name + " or limiting how it is used." + more
}

func cveAlias(aliases []string) string {
	for _, a := range aliases {
		if strings.HasPrefix(a, "CVE-") {
			return a
		}
	}
	return ""
}

type headFile struct {
	path string
	data []byte
}

// headFiles reads the files at HEAD whose path passes want, straight from
// the object store. Files over maxSize are counted, not read.
func headFiles(ctx context.Context, git *gitx.Runner, dir string, want func(string) bool, maxSize int64) (string, []headFile, int, error) {
	head, err := git.Output(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || head == "" {
		return "", nil, 0, nil // no commits yet, or an unborn HEAD: nothing to read
	}
	tree, err := git.Run(ctx, dir, "ls-tree", "-r", "-z", "-l", "--full-tree", head)
	if err != nil {
		return head, nil, 0, err
	}
	type blob struct{ sha, path string }
	var blobs []blob
	skipped := 0
	for _, ent := range bytes.Split(tree.Stdout, []byte{0}) {
		meta, p, ok := bytes.Cut(ent, []byte{'\t'})
		if !ok {
			continue
		}
		fields := strings.Fields(string(meta)) // mode type sha size
		if len(fields) != 4 || fields[1] != "blob" || fields[0] == "120000" || !want(string(p)) {
			continue // symlinks are not followed
		}
		if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil && maxSize > 0 && size > maxSize {
			skipped++
			continue
		}
		blobs = append(blobs, blob{fields[2], string(p)})
	}
	if len(blobs) == 0 {
		return head, nil, skipped, nil
	}
	var in strings.Builder
	for _, b := range blobs {
		in.WriteString(b.sha + "\n")
	}
	s, err := git.Stream(ctx, dir, in.String(), "cat-file", "--batch")
	if err != nil {
		return head, nil, skipped, err
	}
	defer s.Close()
	r := bufio.NewReaderSize(s, 64*1024)
	out := make([]headFile, 0, len(blobs))
	for _, b := range blobs {
		header, err := r.ReadString('\n')
		if err != nil {
			return head, nil, skipped, fmt.Errorf("read %s: %w", b.path, err)
		}
		f := strings.Fields(header) // sha type size | sha missing
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		content := make([]byte, size+1) // +1 for the newline after each object
		if _, err := io.ReadFull(r, content); err != nil {
			return head, nil, skipped, fmt.Errorf("read %s: %w", b.path, err)
		}
		out = append(out, headFile{b.path, content[:size]})
		if ctx.Err() != nil {
			return head, nil, skipped, ctx.Err()
		}
	}
	return head, out, skipped, s.Close()
}
