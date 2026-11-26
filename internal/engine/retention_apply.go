package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// KeepOnlyCopy is why retention keeps a snapshot the policy would drop: it
// holds an object that nothing else keeps, so dropping it could lose data.
const KeepOnlyCopy = "only copy"

// RetentionItem is retention's decision on one snapshot.
type RetentionItem struct {
	Snapshot int64  `json:"snapshot"`
	RepoID   string `json:"repo_id"`
	Path     string `json:"path,omitempty"`
	At       string `json:"at"`
	Keep     bool   `json:"keep"`
	Reason   string `json:"reason,omitempty"` // why it is kept
}

// RetentionReport is what retention kept, removed and unpinned. With
// Applied false it is a plan and nothing was changed.
type RetentionReport struct {
	Applied  bool            `json:"applied"`
	Kept     int             `json:"kept"`
	Removed  int             `json:"removed"`
	Unpinned int             `json:"unpinned"` // snap refs removed (or to remove)
	Items    []RetentionItem `json:"items"`
	Errors   []string        `json:"errors,omitempty"` // repos left as they were

	// TempFiles are bundle files a killed bundle run left half written,
	// relative to the archive root. With Applied they were removed.
	TempFiles []string `json:"temp_files,omitempty"`
	TempBytes int64    `json:"temp_bytes,omitempty"`
}

// Retention applies p to every repo's snapshots. A snapshot the policy
// drops is still kept when one of its objects is reachable from nothing
// else: not from the repo's refs, and not from a snapshot that stays. So
// retention never makes a commit unreachable; it only removes snapshots
// and pins that other refs already cover. With apply false it only plans.
// The caller must hold the archive lock, so no sync pins objects meanwhile.
func Retention(ctx context.Context, root string, git *gitx.Runner, st *store.Store, p RetentionPolicy, apply bool) (*RetentionReport, error) {
	all, err := st.AllSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]core.Repo{}
	for _, r := range repos {
		byID[r.ID] = r
	}
	keep := planRetention(all, p)
	byRepo := map[string][]store.Snapshot{}
	for _, sn := range all {
		byRepo[sn.RepoID] = append(byRepo[sn.RepoID], sn)
	}

	rep := &RetentionReport{Applied: apply, Items: []RetentionItem{}}
	var drop []int64
	unpin := map[string][]string{} // repo id -> SHAs to unpin
	ids := make([]string, 0, len(byRepo))
	for id := range byRepo {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snaps := byRepo[id]
		r, ok := byID[id]
		var shas []string
		if ok && !r.Missing {
			shas, err = guardRepo(ctx, git, RepoDir(root, r), snaps, keep)
		}
		if !ok || r.Missing || err != nil {
			// Cannot look inside the repo, so keep everything it has.
			if err != nil {
				rep.Errors = append(rep.Errors, r.Path+": "+core.Message(err))
			}
			for _, sn := range snaps {
				if _, k := keep[sn.ID]; !k {
					keep[sn.ID] = KeepOnlyCopy
				}
			}
			continue
		}
		if len(shas) > 0 {
			unpin[id] = shas
		}
	}

	for _, sn := range all {
		why, k := keep[sn.ID]
		rep.Items = append(rep.Items, RetentionItem{Snapshot: sn.ID, RepoID: sn.RepoID, Path: sn.Path, At: sn.At, Keep: k, Reason: why})
		if k {
			rep.Kept++
		} else {
			rep.Removed++
			drop = append(drop, sn.ID)
		}
	}
	for _, s := range unpin {
		rep.Unpinned += len(s)
	}
	rep.TempFiles, rep.TempBytes = bundleTemps(root)
	if !apply {
		return rep, nil
	}
	// Rows first, pins after: a crash in between leaves extra pins, which
	// the next run removes, and never a snapshot without its objects.
	if err := st.DeleteSnapshots(ctx, drop); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if len(unpin[id]) == 0 {
			continue
		}
		var b strings.Builder
		for _, sha := range unpin[id] {
			b.WriteString("delete " + snapRefPrefix + sha + " " + sha + "\n")
		}
		if _, err := git.RunInput(ctx, RepoDir(root, byID[id]), b.String(), "update-ref", "--stdin"); err != nil {
			rep.Errors = append(rep.Errors, byID[id].Path+": "+core.Message(err))
			rep.Unpinned -= len(unpin[id])
		}
	}
	for _, rel := range rep.TempFiles {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			rep.Errors = append(rep.Errors, rel+": "+err.Error())
		}
	}
	return rep, nil
}

// bundleTemps finds the half-written files guardian.Bundle leaves when it
// is killed: "<name>.bundle.tmp-<pid>" under the bundles folder. Every
// bundle run holds the archive lock, and so does retention --apply, so
// any such file seen under the lock belongs to no running process. The
// finished bundle next to it is never touched.
func bundleTemps(root string) (files []string, size int64) {
	dir := filepath.Join(root, archive.MetaDir, "bundles")
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: leave it; verify reports the folder
		}
		if d.Type().IsRegular() && strings.Contains(d.Name(), ".bundle.tmp-") {
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, size
}

// guardRepo marks, in keep, every snapshot of one repo that holds an
// object nothing else covers, and returns the pinned SHAs that are safe
// to unpin once the dropped snapshots are gone: pinned, in no kept
// snapshot, and covered by the repo's refs or a kept snapshot.
func guardRepo(ctx context.Context, git *gitx.Runner, dir string, snaps []store.Snapshot, keep map[int64]string) ([]string, error) {
	out, err := git.Output(ctx, dir, "for-each-ref", "--format=%(objectname) %(refname)",
		"refs/heads", "refs/remotes", "refs/tags", snapRefPrefix)
	if err != nil {
		return nil, err
	}
	tips, pinned := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		switch {
		case !ok:
		case strings.HasPrefix(ref, snapRefPrefix):
			pinned[sha] = true
		default:
			tips[sha] = true
		}
	}

	// A kept snapshot can make another one needed, which adds tips, so
	// repeat until nothing changes. Each round keeps at least one more
	// snapshot or ends, so this stops.
	for {
		for _, sn := range snaps {
			if _, k := keep[sn.ID]; k {
				for _, sha := range sn.Refs {
					tips[sha] = true
				}
			}
		}
		cands := map[string]bool{}
		for _, sn := range snaps {
			if _, k := keep[sn.ID]; !k {
				for _, sha := range sn.Refs {
					if !tips[sha] {
						cands[sha] = true
					}
				}
			}
		}
		lost, err := uncovered(ctx, git, dir, cands, tips)
		if err != nil {
			return nil, err
		}
		more := false
		for _, sn := range snaps {
			if _, k := keep[sn.ID]; k {
				continue
			}
			for _, sha := range sn.Refs {
				if lost[sha] {
					keep[sn.ID] = KeepOnlyCopy
					more = true
					break
				}
			}
		}
		if !more {
			break
		}
	}

	// Pins not needed by any kept snapshot; unpin only the covered ones,
	// so a pin left by an interrupted sync never drops a lost commit.
	cands := pinned
	for _, sn := range snaps {
		if _, k := keep[sn.ID]; k {
			for _, sha := range sn.Refs {
				delete(cands, sha)
			}
		}
	}
	lost, err := uncovered(ctx, git, dir, cands, tips)
	if err != nil {
		return nil, err
	}
	var safe []string
	for sha := range cands {
		if !lost[sha] {
			safe = append(safe, sha)
		}
	}
	sort.Strings(safe)
	return safe, nil
}

// uncovered returns the cands that would be lost without their own ref:
// commits not reachable from tips, and any other object (a tag object, a
// missing object) that is not itself a tip.
func uncovered(ctx context.Context, git *gitx.Runner, dir string, cands, tips map[string]bool) (map[string]bool, error) {
	lost := map[string]bool{}
	if len(cands) == 0 {
		return lost, nil
	}
	list := make([]string, 0, len(cands))
	for sha := range cands {
		list = append(list, sha)
	}
	sort.Strings(list)
	res, err := git.RunInput(ctx, dir, strings.Join(list, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[1] == "commit":
			commits = append(commits, f[0])
		case len(f) > 0 && !tips[f[0]]:
			lost[f[0]] = true // a tag object, a tree, or "<sha> missing"
		}
	}
	if len(commits) == 0 {
		return lost, nil
	}
	var in strings.Builder
	for _, c := range commits {
		in.WriteString(c + "\n")
	}
	in.WriteString("--not\n")
	for t := range tips {
		in.WriteString(t + "\n")
	}
	out, err := git.RunInput(ctx, dir, in.String(), "rev-list", "--stdin")
	if err != nil {
		return nil, err
	}
	for _, sha := range strings.Fields(string(out.Stdout)) {
		if cands[sha] {
			lost[sha] = true
		}
	}
	return lost, nil
}
