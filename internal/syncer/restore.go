package syncer

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// RefMove is one ref a restore changes. From is empty for a ref the
// restore creates, To is empty for one it deletes.
type RefMove struct {
	Ref  string `json:"ref"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// PlanRestore compares the repo's refs now with target (a snapshot's refs)
// and returns the moves that make them equal, sorted by ref, plus the refs
// now. It changes nothing.
func PlanRestore(ctx context.Context, git *gitx.Runner, dir string, target map[string]string) (moves []RefMove, now map[string]string, err error) {
	now, _, err = readRefs(ctx, git, dir)
	if err != nil {
		return nil, nil, err
	}
	refs := append(slices.Collect(maps.Keys(now)), slices.Collect(maps.Keys(target))...)
	slices.Sort(refs)
	for _, ref := range slices.Compact(refs) {
		if from, to := now[ref], target[ref]; from != to {
			moves = append(moves, RefMove{Ref: ref, From: from, To: to})
		}
	}
	return moves, now, nil
}

// ApplyRestore carries out moves from PlanRestore. Every other ref changes
// in one `git update-ref --stdin` transaction that checks each ref's old
// value, so they all move or none does, and a ref that moved since the plan
// fails the whole restore. The checked-out branch moves first, with git's
// own checkout, so its files follow; it needs a clean working tree. A
// restore that fails part way leaves a consistent repo: run it again.
func ApplyRestore(ctx context.Context, git *gitx.Runner, dir string, moves []RefMove) error {
	if len(moves) == 0 {
		return nil
	}
	f, err := newFlow(ctx, git, dir)
	if err != nil {
		return err
	}
	s := f.s
	if why := Busy(f.GitDir); why != "" {
		return &core.Error{Category: core.CatConflict, Message: "not restored: " + why}
	}
	if m, err := readMarker(f.GitDir); err != nil {
		return err
	} else if m != nil {
		return &core.Error{Category: core.CatConflict, Message: "not restored: an interrupted update is still open in this repo",
			Hint: "run `chitragit sync` on it first; it puts the repo back as it was"}
	}
	if err := s.haveObjects(ctx, moves); err != nil {
		return err
	}
	current, err := s.worktreeBranch(ctx)
	if err != nil {
		return err
	}
	used, err := s.checkedOut(ctx)
	if err != nil {
		return err
	}

	var head *RefMove
	var batch strings.Builder
	for i, m := range moves {
		switch {
		case m.Ref == current && m.To == "":
			return &core.Error{Category: core.CatConflict, Message: "not restored: the snapshot has no " + shortRef(m.Ref) + ", which is checked out",
				Hint: "check out another branch, then restore again"}
		case m.Ref == current:
			head = &moves[i]
			continue
		case used[m.Ref]:
			return &core.Error{Category: core.CatConflict, Message: "not restored: " + shortRef(m.Ref) + " is checked out in another worktree",
				Hint: "check out another branch there, then restore again"}
		}
		switch {
		case m.From == "":
			batch.WriteString("create " + m.Ref + " " + m.To + "\n")
		case m.To == "":
			batch.WriteString("delete " + m.Ref + " " + m.From + "\n")
		default:
			batch.WriteString("update " + m.Ref + " " + m.To + " " + m.From + "\n")
		}
	}

	branch := strings.TrimPrefix(current, "refs/heads/")
	if head != nil {
		tracked, untracked, err := s.changes(ctx)
		if err != nil {
			return err
		}
		if sha, err := s.out(ctx, "rev-parse", current); err != nil || sha != head.From {
			return &core.Error{Category: core.CatConflict, Message: "not restored: " + branch + " moved since the restore was planned",
				Hint: "run the restore again", Err: err}
		}
		if tracked || untracked {
			return &core.Error{Category: core.CatConflict, Message: "not restored: " + branch + " has uncommitted changes",
				Hint: "commit them, or pass --stash to put them aside and back"}
		}
		// checkout moves the branch, the index and the files together and
		// refuses to overwrite anything it would lose.
		if _, err := git.Run(ctx, dir, "checkout", "--quiet", "-B", branch, head.To); err != nil {
			return err
		}
	}
	step("restore.head")
	if batch.Len() > 0 {
		if _, err := git.RunInput(ctx, dir, batch.String(), "update-ref", "-m", "chitragit: restore", "--stdin"); err != nil {
			if head != nil {
				_, _ = git.Run(ctx, dir, "checkout", "--quiet", "-B", branch, head.From)
			}
			return err
		}
	}
	step("restore.refs")
	return nil
}

// haveObjects fails when the repo lacks an object a restore would point a
// ref at, which can only happen to a snapshot taken before pins existed or
// a repo cloned again since.
func (s *repoSync) haveObjects(ctx context.Context, moves []RefMove) error {
	var in strings.Builder
	for _, m := range moves {
		if m.To != "" {
			in.WriteString(m.To + "\n")
		}
	}
	if in.Len() == 0 {
		return nil
	}
	res, err := s.git.RunInput(ctx, s.dir, in.String(), "cat-file", "--batch-check")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if sha, ok := strings.CutSuffix(line, " missing"); ok {
			return &core.Error{Category: core.CatCorrupt, Message: "not restored: this repo no longer has commit " + sha[:min(12, len(sha))] + " from the snapshot",
				Hint: "pick a newer snapshot with `chitragit history`"}
		}
	}
	return nil
}

// worktreeBranch returns the branch checked out in the repo's working tree,
// or "" for a bare repo or a detached HEAD.
func (s *repoSync) worktreeBranch(ctx context.Context) (string, error) {
	bare, err := s.out(ctx, "rev-parse", "--is-bare-repository")
	if err != nil || bare == "true" {
		return "", err
	}
	ref, err := s.out(ctx, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil || ref == "HEAD" { // HEAD: detached
		return "", err
	}
	return ref, nil
}
