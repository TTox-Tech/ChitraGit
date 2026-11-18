package syncer

import (
	"context"
	"fmt"
	"sort"
)

// Inspection is what a Shield hook says about one commit.
type Inspection struct {
	Block    bool
	Findings int    // findings at any severity
	Worst    string // title of the worst finding, for the reason
}

// ShieldFunc judges a fetched commit from its objects before any local
// branch moves to it (KodeKavach Shield, CG-42.7). It must only read.
type ShieldFunc func(ctx context.Context, dir, commit string) (Inspection, error)

// shieldBlock says Shield held a repo's branches back.
type shieldBlock struct {
	sha string
	in  Inspection
}

func (e *shieldBlock) Error() string {
	worst := ""
	if e.in.Worst != "" {
		worst = ": " + e.in.Worst
	}
	return fmt.Sprintf("KodeKavach blocked upstream %.12s (%d findings%s); downloaded but not applied, branches left where they were",
		e.sha, e.in.Findings, worst)
}

// shield inspects every upstream tip a branch could move to. One blocked
// tip holds the whole repo: no branch moves and no file is written. The
// new commits stay in the remote-tracking refs and are pinned with the
// rest of the sync's snapshot, so nothing is lost.
func (s *repoSync) shield(ctx context.Context, branches map[string]branch) error {
	if s.opts.Shield == nil {
		return nil
	}
	seen := map[string]bool{}
	var tips []string
	for _, b := range branches {
		if b.upstreamSHA != "" && b.upstreamSHA != b.sha && !seen[b.upstreamSHA] {
			seen[b.upstreamSHA] = true
			tips = append(tips, b.upstreamSHA)
		}
	}
	sort.Strings(tips)
	for _, sha := range tips {
		step("sync.inspect")
		in, err := s.opts.Shield(ctx, s.dir, sha)
		if err != nil {
			return err
		}
		if in.Block {
			return &shieldBlock{sha: sha, in: in}
		}
	}
	return nil
}
