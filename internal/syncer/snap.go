package syncer

import (
	"context"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// snapPrefix holds one ref per object that any snapshot points at, named
// after the object: refs/chitragit/snap/<sha>. A ref keeps git gc from
// ever removing the object, and naming it by SHA means an object shared by
// many snapshots is pinned once, so the number of refs grows with new
// history, not with the number of snapshots. Retention (CG-09) is the only
// thing that removes them.
const snapPrefix = "refs/chitragit/snap/"

// readRefs returns every branch, remote-tracking ref and tag with its SHA,
// and the set of objects already pinned, in one git call.
func readRefs(ctx context.Context, git *gitx.Runner, dir string) (refs map[string]string, pinned map[string]bool, err error) {
	out, err := git.Output(ctx, dir, "for-each-ref", "--format=%(objectname) %(refname)",
		"refs/heads", "refs/remotes", "refs/tags", snapPrefix)
	if err != nil {
		return nil, nil, err
	}
	refs, pinned = map[string]string{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		switch {
		case !ok:
		case strings.HasPrefix(ref, snapPrefix):
			pinned[sha] = true
		case !strings.HasSuffix(ref, "/HEAD"):
			refs[ref] = sha
		}
	}
	return refs, pinned, nil
}

// pin adds a snap ref for every object in sets that is not pinned yet, in
// one update-ref transaction. Nothing is written when all are pinned.
func pin(ctx context.Context, git *gitx.Runner, dir string, pinned map[string]bool, sets ...map[string]string) error {
	var batch strings.Builder
	for _, set := range sets {
		for _, sha := range set {
			if !pinned[sha] {
				pinned[sha] = true
				batch.WriteString("update " + snapPrefix + sha + " " + sha + "\n")
			}
		}
	}
	if batch.Len() == 0 {
		return nil
	}
	_, err := git.RunInput(ctx, dir, batch.String(), "update-ref", "--stdin")
	return err
}

// Snapshot reads a repo's refs for a manual snapshot and pins their
// objects first, so the snapshot is safe from gc as soon as it is stored.
func Snapshot(ctx context.Context, git *gitx.Runner, dir string) (map[string]string, error) {
	refs, pinned, err := readRefs(ctx, git, dir)
	if err != nil {
		return nil, err
	}
	return refs, pin(ctx, git, dir, pinned, refs)
}
