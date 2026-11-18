package audit

import "fmt"

// CheckLimits covers repos too big to take in safely (KodeKavach Shield,
// CG-42.8): pack bombs, million-file trees, huge blobs, absurd nesting and
// floods of refs.
const CheckLimits = "limits"

// Rules of the limits check.
const (
	RuleLimitRepoSize = "limit-repo-size"
	RuleLimitFiles    = "limit-files"
	RuleLimitBlob     = "limit-blob-size"
	RuleLimitDepth    = "limit-tree-depth"
	RuleLimitRefs     = "limit-refs"
)

// Limits caps what Shield lets into the archive. Zero means no limit.
type Limits struct {
	MaxRepoBytes int64 `json:"max_repo_bytes"` // objects on disk after the fetch
	MaxFiles     int   `json:"max_files"`
	MaxBlobBytes int64 `json:"max_blob_bytes"`
	MaxTreeDepth int   `json:"max_tree_depth"`
	MaxRefs      int   `json:"max_refs"`
}

// DefaultLimits are the [shield] defaults of the Shield plan.
func DefaultLimits() Limits {
	return Limits{MaxRepoBytes: 2048 << 20, MaxFiles: 200_000, MaxBlobBytes: 100 << 20, MaxTreeDepth: 64, MaxRefs: 100_000}
}

// Size is what Check measures beyond the tree: the repo's objects on disk
// and its refs. A negative value was not measured and is not checked.
type Size struct {
	Bytes int64
	Refs  int
}

// Check returns a critical finding for every limit the commit's tree or
// the repo's size goes past.
func (l Limits) Check(repoID, repoPath, commit string, st TreeStats, sz Size) []Finding {
	var out []Finding
	add := func(rule, title, hint string) {
		out = append(out, Finding{
			ID: FindingID(repoID, CheckLimits, rule, ""), RepoID: repoID, RepoPath: repoPath, Check: CheckLimits,
			Rule: rule, Title: title, Severity: SevCritical, Confidence: ConfidenceHigh, Commit: commit,
			Occurrences: 1, Hint: hint, Status: StatusOpen,
		})
	}
	if l.MaxRepoBytes > 0 && sz.Bytes > l.MaxRepoBytes {
		add(RuleLimitRepoSize, fmt.Sprintf("The repo is %s, over the %s limit", mib(sz.Bytes), mib(l.MaxRepoBytes)),
			"A pack this large can fill the disk. Raise shield.max_repo_mb if you expect it.")
	}
	if l.MaxFiles > 0 && st.Files > l.MaxFiles {
		add(RuleLimitFiles, fmt.Sprintf("The tree has %d files, over the %d limit", st.Files, l.MaxFiles),
			"Checking out this many files can exhaust the disk or the file system. Raise shield.max_files if you expect it.")
	}
	if l.MaxBlobBytes > 0 && st.LargestBlob > l.MaxBlobBytes {
		add(RuleLimitBlob, fmt.Sprintf("A file is %s, over the %s limit (%s)", mib(st.LargestBlob), mib(l.MaxBlobBytes), st.LargestPath),
			"A file this large is rarely source code. Raise shield.max_blob_mb if you expect it.")
	}
	if l.MaxTreeDepth > 0 && st.MaxDepth > l.MaxTreeDepth {
		add(RuleLimitDepth, fmt.Sprintf("Folders nest %d deep, over the %d limit", st.MaxDepth, l.MaxTreeDepth),
			"Paths this deep break tools and can exceed the path length limits of Windows.")
	}
	if l.MaxRefs > 0 && sz.Refs > l.MaxRefs {
		add(RuleLimitRefs, fmt.Sprintf("The repo has %d refs, over the %d limit", sz.Refs, l.MaxRefs),
			"A flood of branches or tags slows every sync. Raise shield.max_refs if you expect it.")
	}
	return out
}

func mib(b int64) string {
	if b < 1<<20 {
		return fmt.Sprintf("%d KB", (b+1023)>>10)
	}
	return fmt.Sprintf("%d MB", (b+(1<<20)-1)>>20)
}
