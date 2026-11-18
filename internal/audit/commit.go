package audit

import (
	"context"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// maxPayloadBlobBytes caps the files the payload check reads to look at
// their first bytes.
// shortcut: git cat-file has no "first N bytes" mode, so larger programs are
// judged by name only; read a prefix with a filtered stream if that matters.
const maxPayloadBlobBytes = 32 << 20

// CommitOptions says which commit ScanCommit reads and how.
type CommitOptions struct {
	RepoID, RepoPath, Dir string
	Commit                string     // any commit, such as a fetched upstream that is not checked out yet
	Pack                  *RulesPack // actions and agents rules
	MaxFileSize           int64      // text files larger than this are not read (0 = no limit)
}

// CommitReport is what Shield reads from one commit before it is checked
// out: the tree's shape and the findings of the tree, actions, agents,
// autorun and payload checks.
type CommitReport struct {
	Commit   string    `json:"commit"`
	Stats    TreeStats `json:"stats"`
	Findings []Finding `json:"findings"`
	Skipped  int       `json:"skipped"` // text files over MaxFileSize, not read
}

// ScanCommit runs every check that can judge a commit from its objects
// alone, so a verdict exists before a single file is written. It lists the
// tree once and reads each blob it needs once. The actions and agents
// findings are the same as `audit --checks actions,agents` on that commit.
func ScanCommit(ctx context.Context, git *gitx.Runner, o CommitOptions) (*CommitReport, error) {
	return ScanObjects(ctx, GitObjects(git, o.Dir, o.Commit), o)
}

// ScanObjects is ScanCommit reading through src, such as a provider API
// before the repo is cloned (Shield L0). o.Dir is not used.
func ScanObjects(ctx context.Context, src Objects, o CommitOptions) (*CommitReport, error) {
	entries, err := src.Tree(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := inspectEntries(ctx, src, o.RepoID, o.RepoPath, o.Commit, entries)
	if err != nil {
		return nil, err
	}
	rep := &CommitReport{Commit: o.Commit, Stats: tree.Stats, Findings: tree.Findings}

	pay := &payloadScanner{repoID: o.RepoID, repo: o.RepoPath, commit: o.Commit, ids: map[string]bool{}}
	var text, bins []gitx.TreeEntry
	for _, e := range entries {
		pay.name(e)
		if !e.Regular() {
			continue
		}
		if payloadCandidate(e) {
			bins = append(bins, e)
		}
		if IsWorkflowFile(e.Path) || IsAutorunFile(e.Path) {
			text = append(text, e)
		} else if _, ok := o.Pack.AgentFile(e.Path); ok {
			text = append(text, e)
		}
	}
	files, skipped, err := readTextBlobs(ctx, src, text, o.MaxFileSize)
	if err != nil {
		return nil, err
	}
	rep.Skipped = skipped
	err = src.Blobs(ctx, shasOf(bins), maxPayloadBlobBytes, func(b gitx.Blob) error {
		pay.blob(bins[b.Index], b.Data)
		return nil
	})
	if err != nil {
		return nil, err
	}

	rep.Findings = append(rep.Findings, ScanActions(o.Pack, o.RepoID, o.RepoPath, o.Commit, files)...)
	rep.Findings = append(rep.Findings, ScanAgents(o.Pack, o.RepoID, o.RepoPath, o.Commit, files)...)
	rep.Findings = append(rep.Findings, ScanAutorun(o.RepoID, o.RepoPath, o.Commit, files)...)
	rep.Findings = append(rep.Findings, pay.out...)
	sortFindings(rep.Findings)
	return rep, nil
}
