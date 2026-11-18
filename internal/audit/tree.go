package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// CheckTree is KodeKavach Shield's tree inspection: what a checkout of a
// commit would write (names, modes, sizes, symlink targets, filters,
// submodules), read from the object store before anything is checked out.
const CheckTree = "tree"

// Rules of the tree check.
const (
	RuleGitAlias        = "tree-git-alias"        // a name a filesystem treats as .git
	RuleMetaSymlink     = "tree-meta-symlink"     // .gitmodules, .gitattributes, ... as a symlink
	RuleControlChar     = "tree-control-char"     // control characters in a name
	RuleInvisibleChar   = "tree-invisible-char"   // bidi or zero-width characters in a name
	RuleReservedName    = "tree-reserved-name"    // CON, NUL, COM1, ... on Windows
	RuleWindowsName     = "tree-windows-name"     // characters or endings Windows cannot store
	RuleCaseClash       = "tree-case-clash"       // two paths that differ only in case
	RuleSymlinkOutside  = "tree-symlink-outside"  // a symlink that points outside the repo
	RuleSymlinkGit      = "tree-symlink-git"      // a symlink into .git
	RuleFilterDriver    = "tree-filter-driver"    // .gitattributes names a filter driver
	RuleSubmoduleOption = "tree-submodule-option" // a submodule URL or path git could read as an option
	RuleSubmoduleURL    = "tree-submodule-url"    // ext::, fd:: and other transport helpers
	RuleSubmoduleLocal  = "tree-submodule-local"  // a submodule cloned from this computer
	RuleSubmodulePath   = "tree-submodule-path"   // a submodule name or path that leaves its folder
)

// maxTreeMetaBlobBytes caps the symlink targets and .gitattributes files
// read into memory; larger ones are skipped.
const maxTreeMetaBlobBytes = 1 << 20

// TreeStats counts what a checkout of the commit would write.
type TreeStats struct {
	Files       int    `json:"files"` // regular files and symlinks
	Dirs        int    `json:"dirs"`
	Symlinks    int    `json:"symlinks"`
	Submodules  int    `json:"submodules"` // gitlinks
	Executables int    `json:"executables"`
	MaxDepth    int    `json:"max_depth"` // path parts of the deepest entry
	TotalBytes  int64  `json:"total_bytes"`
	LargestBlob int64  `json:"largest_blob"`
	LargestPath string `json:"largest_path,omitempty"`
}

// TreeReport is what InspectTree found in one commit.
type TreeReport struct {
	Commit   string    `json:"commit"`
	Stats    TreeStats `json:"stats"`
	Findings []Finding `json:"findings"`
}

// InspectTree reads the tree of commit from the object store, so nothing
// is written to a work tree and it works the same on bare repos and on a
// quarantined clone. git's own fsck (on for every fetch) already refuses
// "..", "." and empty path parts, and gitx.Tree drops them too.
func InspectTree(ctx context.Context, git *gitx.Runner, repoID, repoPath, dir, commit string) (*TreeReport, error) {
	entries, err := git.Tree(ctx, dir, commit, gitx.TreeOptions{})
	if err != nil {
		return nil, err
	}
	return inspectEntries(ctx, GitObjects(git, dir, commit), repoID, repoPath, commit, entries)
}

// inspectEntries is InspectTree on a tree already listed.
func inspectEntries(ctx context.Context, src Objects, repoID, repoPath, commit string, entries []gitx.TreeEntry) (*TreeReport, error) {
	t := &treeInspector{
		repoID: repoID, repo: repoPath, commit: commit,
		seen: map[string]bool{}, folded: map[string]foldedPath{}, ids: map[string]bool{},
	}
	var meta []gitx.TreeEntry // blobs whose content matters: symlink targets and .gitattributes
	gitmodules := ""
	for _, e := range entries {
		t.entry(e)
		switch {
		case e.Mode == "120000", e.Regular() && path.Base(e.Path) == ".gitattributes":
			meta = append(meta, e)
		case e.Regular() && e.Path == ".gitmodules":
			gitmodules = e.SHA
		}
	}
	err := src.Blobs(ctx, shasOf(meta), maxTreeMetaBlobBytes, func(b gitx.Blob) error {
		if e := meta[b.Index]; e.Mode == "120000" {
			t.symlink(e.Path, string(b.Data))
		} else {
			t.gitattributes(e.Path, b.Data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if gitmodules != "" {
		items, err := src.Submodules(ctx, gitmodules)
		if errors.Is(err, ErrIncomplete) {
			return nil, err
		}
		t.gitmodules(items, err)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	sortFindings(t.out)
	return &TreeReport{Commit: commit, Stats: t.stats, Findings: t.out}, nil
}

type foldedPath struct {
	path    string
	symlink bool
}

type treeInspector struct {
	repoID, repo, commit string
	stats                TreeStats
	seen                 map[string]bool       // every file and folder path already checked
	folded               map[string]foldedPath // lower-case path -> first path seen with it
	ids                  map[string]bool
	out                  []Finding
}

func (t *treeInspector) add(rule string, sev Severity, file, key, title, preview, hint string) {
	id := FindingID(t.repoID, CheckTree, rule, file+"\x00"+key)
	if t.ids[id] {
		return
	}
	t.ids[id] = true
	t.out = append(t.out, Finding{
		ID: id, RepoID: t.repoID, RepoPath: t.repo, Check: CheckTree, Rule: rule, Title: title,
		Severity: sev, Confidence: ConfidenceHigh, File: displayPath(file), Commit: t.commit,
		Preview: snippet(displayPath(preview), 80), Occurrences: 1, Hint: hint, Status: StatusOpen,
	})
}

// entry counts one tree entry and checks every path part not seen yet, so
// a folder shared by many files is checked once.
func (t *treeInspector) entry(e gitx.TreeEntry) {
	parts := strings.Split(e.Path, "/")
	t.stats.MaxDepth = max(t.stats.MaxDepth, len(parts))
	switch {
	case e.Type == "commit":
		t.stats.Submodules++
	case e.Mode == "120000":
		t.stats.Files++
		t.stats.Symlinks++
	case e.Type == "blob":
		t.stats.Files++
		if e.Mode == "100755" {
			t.stats.Executables++
		}
		t.stats.TotalBytes += max(e.Size, 0) // -1: a provider API that does not send sizes
		if e.Size > t.stats.LargestBlob {
			t.stats.LargestBlob, t.stats.LargestPath = e.Size, e.Path
		}
	}
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		if t.seen[p] {
			continue
		}
		t.seen[p] = true
		leaf := i == len(parts)-1
		if !leaf {
			t.stats.Dirs++
		}
		symlink := leaf && e.Mode == "120000"
		t.name(p, parts[i], symlink)
		t.caseClash(p, symlink)
	}
}

// metaFiles are files git reads from the work tree; as a symlink they can
// point git at a file outside the repo (CVE-2018-11235 and later).
var metaFiles = []string{".gitmodules", ".gitattributes", ".gitignore", ".mailmap"}

// name checks one path part: p is the path up to and including it.
func (t *treeInspector) name(p, name string, symlink bool) {
	if isGitAlias(name, ".git", "git~1") {
		t.add(RuleGitAlias, SevCritical, p, "", "A file name that a filesystem reads as .git",
			name, "Checking it out could replace the repo's own git folder and run code. Do not check out this commit.")
	}
	if symlink {
		for _, m := range metaFiles {
			if isGitAlias(name, m, m[1:min(len(m), 7)]+"~1") {
				sev := SevHigh
				if m == ".gitmodules" {
					sev = SevCritical
				}
				t.add(RuleMetaSymlink, sev, p, m, "A git settings file ("+m+") that is a symlink",
					name, "git would read this file from wherever the link points. Do not check out this commit.")
			}
		}
	}
	var control, invisibleRune, windowsChar bool
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0):
			control = true
		case invisible(r) || bidiMark(r):
			invisibleRune = true
		case strings.ContainsRune(`<>:"|?*\`, r):
			windowsChar = true
		}
	}
	if !utf8.ValidString(name) {
		control = true
	}
	if control {
		t.add(RuleControlChar, SevHigh, p, "", "A control character in a file name",
			name, "Control characters can rewrite what a terminal shows. Rename the file before trusting any listing of this repo.")
	}
	if invisibleRune {
		t.add(RuleInvisibleChar, SevHigh, p, "", "An invisible or right-to-left character in a file name",
			name, "The name can look like a different file in a listing or review. Check what the file really is.")
	}
	if windowsChar || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		t.add(RuleWindowsName, SevMedium, p, "", "A file name Windows cannot store",
			name, "Checking out this commit fails or writes a different name on Windows.")
	}
	if isReservedName(name) {
		t.add(RuleReservedName, SevMedium, p, "", "A file name Windows reserves for a device",
			name, "Windows treats this name as a device, so checking it out fails or writes to the device.")
	}
}

// caseClash finds two paths that are the same on a case-insensitive disk
// (Windows, macOS): one overwrites the other on checkout. With a symlink
// on one side, the link can redirect the other's files (CVE-2021-21300).
func (t *treeInspector) caseClash(p string, symlink bool) {
	key := strings.ToLower(p)
	other, ok := t.folded[key]
	if !ok {
		t.folded[key] = foldedPath{path: p, symlink: symlink}
		return
	}
	sev := SevMedium
	if symlink || other.symlink {
		sev = SevHigh
	}
	t.add(RuleCaseClash, sev, p, "", "Two paths that differ only in upper and lower case",
		other.path, "On Windows and macOS one overwrites the other on checkout; with a symlink, files can land outside their folder.")
}

// symlink checks where a link points, the way a checkout would resolve it.
func (t *treeInspector) symlink(p, target string) {
	if target == "" {
		return
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) || (len(target) > 1 && target[1] == ':') {
		t.add(RuleSymlinkOutside, SevMedium, p, target, "A symlink to an absolute path outside the repo",
			target, "Programs that follow the link read or write outside the repo.")
		return
	}
	resolved := path.Join(path.Dir(p), strings.ReplaceAll(target, `\`, "/")) // nosemgrep: use-filepath-join -- tree paths and link targets always use "/"
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		t.add(RuleSymlinkOutside, SevMedium, p, target, "A symlink that points outside the repo",
			target, "Programs that follow the link read or write outside the repo.")
		return
	}
	for part := range strings.SplitSeq(resolved, "/") {
		if isGitAlias(part, ".git", "git~1") {
			t.add(RuleSymlinkGit, SevHigh, p, target, "A symlink into the repo's .git folder",
				target, "Writing through the link changes git's own files, such as hooks or config.")
			return
		}
	}
}

// gitattributes reports filter drivers. ChitraGit never runs them (CG-42.2),
// but a user's own git would, if a driver of that name is configured. LFS
// is left out: it is the common case and has its own setting (sync.lfs).
func (t *treeInspector) gitattributes(p string, data []byte) {
	for line := range bytes.Lines(data) {
		fields := strings.Fields(string(line))
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		for _, attr := range fields[1:] {
			driver, ok := strings.CutPrefix(attr, "filter=")
			if !ok || driver == "lfs" {
				continue
			}
			t.add(RuleFilterDriver, SevLow, p, driver, "A .gitattributes filter driver ("+driver+")",
				strings.TrimSpace(string(line)), "A git with a filter of this name configured runs it on checkout. ChitraGit never does.")
		}
	}
}

// gitmodules checks each submodule's name, path and URL in .gitmodules,
// read by Objects.Submodules (git's own config parser on a local repo).
func (t *treeInspector) gitmodules(items [][2]string, err error) {
	if err != nil {
		t.add(RuleSubmodulePath, SevMedium, ".gitmodules", "unreadable", "A .gitmodules file git cannot read",
			"", "Submodules of this commit cannot be checked; do not initialise them.")
		return
	}
	for _, item := range items {
		key, value := item[0], item[1]
		rest, ok := strings.CutPrefix(key, "submodule.")
		if !ok {
			continue
		}
		i := strings.LastIndexByte(rest, '.')
		if i < 0 {
			continue
		}
		name, field := rest[:i], rest[i+1:]
		t.submoduleName(name)
		switch field {
		case "path":
			t.submodulePath(name, value)
		case "url":
			t.submoduleURL(name, value)
		}
	}
}

func (t *treeInspector) submoduleName(name string) {
	if leavesFolder(name) {
		t.add(RuleSubmodulePath, SevCritical, ".gitmodules", "name\x00"+name, "A submodule name that leaves .git/modules",
			name, "git stores the submodule under this name, so it can overwrite git's own files (CVE-2018-11235).")
	}
}

func (t *treeInspector) submodulePath(name, p string) {
	switch {
	case strings.HasPrefix(p, "-"):
		t.add(RuleSubmoduleOption, SevCritical, ".gitmodules", "path\x00"+name, "A submodule path that starts with a dash",
			p, "An older git reads it as a command-line option (CVE-2018-17456).")
	case leavesFolder(p):
		t.add(RuleSubmodulePath, SevCritical, ".gitmodules", "path\x00"+name, "A submodule path outside the repo or inside .git",
			p, "Initialising the submodule writes outside its folder.")
	}
}

func (t *treeInspector) submoduleURL(name, url string) {
	lower := strings.ToLower(url)
	switch {
	case strings.HasPrefix(url, "-"):
		t.add(RuleSubmoduleOption, SevCritical, ".gitmodules", "url\x00"+name, "A submodule URL that starts with a dash",
			url, "An older git reads it as a command-line option and runs it (CVE-2018-17456).")
	case transportHelper(lower):
		t.add(RuleSubmoduleURL, SevCritical, ".gitmodules", "url\x00"+name, "A submodule URL that runs a program",
			url, "ext:: and other transport helpers run a command when the submodule is cloned.")
	case strings.HasPrefix(lower, "file:") || strings.HasPrefix(url, "/") || strings.HasPrefix(url, "./") ||
		strings.HasPrefix(url, "../") || (len(url) > 1 && url[1] == ':'):
		// Relative URLs (./, ../) resolve against the remote, not the disk,
		// but a clone from a local folder turns them into local paths.
		if strings.HasPrefix(url, ".") {
			return
		}
		t.add(RuleSubmoduleLocal, SevMedium, ".gitmodules", "url\x00"+name, "A submodule cloned from a folder on this computer",
			url, "Initialising it copies a local folder into the repo, which can expose files from outside the archive.")
	}
}

// transportHelper reports a URL of the form <helper>::<address>, which
// makes git run git-remote-<helper>; ext:: runs any command.
func transportHelper(url string) bool {
	helper, _, ok := strings.Cut(url, "::")
	if !ok || helper == "" {
		return false
	}
	for _, r := range helper {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '+' && r != '.' {
			return false
		}
	}
	return true
}

// leavesFolder reports a relative path that is absolute, climbs out with
// "..", or passes through a .git folder.
func leavesFolder(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" || strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
		return true
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == ".." || isGitAlias(part, ".git", "git~1") {
			return true
		}
	}
	return false
}

// isGitAlias reports whether a checkout would write name as want (for
// example ".git") on some filesystem: case-insensitive, with HFS+ ignoring
// zero-width characters, NTFS dropping trailing dots and spaces and
// anything after a colon (alternate streams), and NTFS's 8.3 short name
// (short, such as "git~1"). It follows git's is_hfs_dotgit/is_ntfs_dotgit.
func isGitAlias(name, want, short string) bool {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if !hfsIgnorable(r) {
			b.WriteRune(r)
		}
	}
	n := b.String()
	if i := strings.IndexByte(n, ':'); i >= 0 {
		n = n[:i]
	}
	n = strings.TrimRight(n, ". ")
	return n == want || n == short
}

// hfsIgnorable lists the code points HFS+ drops when comparing names.
func hfsIgnorable(r rune) bool {
	return r >= 0x200C && r <= 0x200F || r >= 0x202A && r <= 0x202E || r >= 0x206A && r <= 0x206F || r == 0xFEFF
}

// bidiMark covers the direction marks invisible() leaves out.
func bidiMark(r rune) bool { return r == 0x200E || r == 0x200F || r == 0x061C }

// isReservedName reports a Windows device name, with or without an
// extension: CON, PRN, AUX, NUL, COM0-9 and LPT0-9 (and their superscript
// digit forms).
func isReservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) < 4 || (stem[:3] != "COM" && stem[:3] != "LPT") {
		return false
	}
	d := stem[3:]
	return len(d) == 1 && d[0] >= '0' && d[0] <= '9' || d == "¹" || d == "²" || d == "³"
}

// displayPath shows a path with control and invisible characters as
// <U+XXXX>, so printing a finding can never drive the terminal.
func displayPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0) || invisible(r) || bidiMark(r) || r == utf8.RuneError {
			fmt.Fprintf(&b, "<U+%04X>", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
