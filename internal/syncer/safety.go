package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Every ref ChitraGit creates lives under refs/chitragit/, out of the way of
// branches and tags, and is never pushed (push only sends refs/heads and
// refs/tags unless told otherwise).
const (
	backupPrefix    = "refs/chitragit/backup/"    // HEAD before a stash flow, kept until it ends
	archivedPrefix  = "refs/chitragit/archived/"  // remote branches deleted upstream
	rewrittenPrefix = "refs/chitragit/rewritten/" // history replaced by an upstream force-push
)

// markerFile in the git dir says an update with local changes is in
// progress. If ChitraGit dies mid-way, the next run finds it and restores
// the repo (PRD §8.2 "crash mid stash flow").
const markerFile = "chitragit-update.json"

type marker struct {
	Backup  string `json:"backup"`  // backup ref holding the original HEAD
	Stash   string `json:"stash"`   // stash message, used to find the stash
	Started string `json:"started"` // RFC 3339
}

// stamp is a ref-safe UTC timestamp, unique per second.
func stamp() string { return time.Now().UTC().Format("20060102T150405Z") }

// stashUpdate is the safe stash flow (PRD F3): back up HEAD, put local
// changes aside, fast-forward, put them back. If they do not go back
// cleanly, the repo is rolled back to exactly where it was.
func (s *repoSync) stashUpdate(ctx context.Context, gitDir, target string, behind int) error {
	r := s.res
	ts := stamp()
	m := marker{Backup: backupPrefix + ts, Stash: "chitragit-auto " + ts, Started: time.Now().UTC().Format(time.RFC3339)}

	head, err := s.out(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if _, err := s.git.Run(ctx, s.dir, "update-ref", m.Backup, head); err != nil {
		return err
	}
	if err := writeMarker(gitDir, m); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write the update marker", Err: err}
	}
	if _, err := s.git.Run(ctx, s.dir, "stash", "push", "--include-untracked", "--quiet", "-m", m.Stash); err != nil {
		s.finishFlow(ctx, gitDir, m)
		return err
	}
	stash, err := s.findStash(ctx, m.Stash)
	if err != nil || stash == "" {
		// Nothing was stashed after all (the changes vanished meanwhile).
		s.finishFlow(ctx, gitDir, m)
		if err != nil {
			return err
		}
		_, err = s.git.Run(ctx, s.dir, "merge", "--ff-only", "--quiet", "--no-stat", target)
		return err
	}

	if _, err := s.git.Run(ctx, s.dir, "merge", "--ff-only", "--quiet", "--no-stat", target); err != nil {
		if rerr := s.restore(ctx, gitDir, m, stash); rerr != nil {
			return rerr
		}
		return err
	}
	if _, err := s.git.Run(ctx, s.dir, "stash", "apply", "--index", "--quiet", stash); err != nil {
		files, _ := s.out(ctx, "diff", "--name-only", "--diff-filter=U")
		if rerr := s.restore(ctx, gitDir, m, stash); rerr != nil {
			return rerr
		}
		r.Status = StatusConflict
		r.Reason = "your local changes conflict with " + plural(behind, "new commit") +
			conflictFiles(files) + "; the repo was put back exactly as it was and the updates are downloaded"
		return nil
	}
	if err := s.dropStash(ctx, stash); err != nil {
		return err
	}
	s.finishFlow(ctx, gitDir, m)
	r.Reason = "applied " + plural(behind, "new commit") + " and kept your local changes"
	return nil
}

// restore rolls the working tree, index and HEAD back to the backup, then
// re-applies the stash on the commit it was made from, which always works.
func (s *repoSync) restore(ctx context.Context, gitDir string, m marker, stash string) error {
	steps := [][]string{
		{"reset", "--hard", "--quiet", m.Backup},
		// Untracked files left now came from the failed apply; the stash
		// holds them all. Ignored files are never touched (no -x).
		{"clean", "-d", "--force", "--quiet"},
		{"stash", "apply", "--index", "--quiet", stash},
	}
	for _, args := range steps {
		if _, err := s.git.Run(ctx, s.dir, args...); err != nil {
			return &core.Error{Category: core.CatConflict,
				Message: "could not put your changes back automatically; they are safe in the stash \"" + m.Stash + "\"",
				Hint:    "run `git stash list` in this repo and `git stash apply` the entry named " + m.Stash, Err: err}
		}
	}
	if err := s.dropStash(ctx, stash); err != nil {
		return err
	}
	s.finishFlow(ctx, gitDir, m)
	return nil
}

// recover finishes the job of an update that was interrupted by a crash or
// a forced exit. It reports true when it restored the repo.
func (s *repoSync) recover(ctx context.Context, gitDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(gitDir, markerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	var m marker
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	if err != nil || !strings.HasPrefix(m.Backup, backupPrefix) {
		return false, &core.Error{Category: core.CatCorrupt, Message: "found an unreadable update marker",
			Hint: "check the repo by hand, then delete .git/" + markerFile, Err: err}
	}
	stash, err := s.findStash(ctx, m.Stash)
	if err != nil {
		return false, err
	}
	if stash == "" {
		// The stash was never made or was already re-applied and dropped:
		// the working tree holds the user's changes either way.
		s.finishFlow(ctx, gitDir, m)
		return false, nil
	}
	return true, s.restore(ctx, gitDir, m, stash)
}

func (s *repoSync) finishFlow(ctx context.Context, gitDir string, m marker) {
	_ = os.Remove(filepath.Join(gitDir, markerFile))
	_, _ = s.git.Run(ctx, s.dir, "update-ref", "-d", m.Backup)
}

// findStash returns the commit of the stash entry with this message.
func (s *repoSync) findStash(ctx context.Context, msg string) (string, error) {
	out, err := s.out(ctx, "stash", "list", "--format=%H %s")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, subject, ok := strings.Cut(line, " "); ok && strings.HasSuffix(subject, ": "+msg) {
			return sha, nil
		}
	}
	return "", nil
}

// dropStash drops the stash entry whose commit is sha. `git stash drop`
// only takes stash@{n}, so the index is looked up first.
func (s *repoSync) dropStash(ctx context.Context, sha string) error {
	out, err := s.out(ctx, "stash", "list", "--format=%H")
	if err != nil {
		return err
	}
	for i, line := range strings.Split(out, "\n") {
		if line == sha {
			_, err := s.git.Run(ctx, s.dir, "stash", "drop", "--quiet", "stash@{"+strconv.Itoa(i)+"}")
			return err
		}
	}
	return nil
}

// protect keeps history that a fetch would otherwise make unreachable:
// remote branches deleted upstream go to refs/chitragit/archived/, and the
// old tip of every ref an upstream force-push replaced goes to
// refs/chitragit/rewritten/. Both run right after the fetch, which ran with
// automatic gc off, so the old objects are still there.
func (s *repoSync) protect(ctx context.Context, before map[string]string, archive bool) error {
	after, err := s.refs(ctx)
	if err != nil {
		return err
	}
	ts := stamp()
	var batch strings.Builder
	for ref, old := range before {
		short := strings.TrimPrefix(ref, "refs/")
		now, still := after[ref]
		switch {
		case !still && archive && strings.HasPrefix(ref, "refs/remotes/"):
			batch.WriteString("create " + archivedPrefix + short + "/" + ts + " " + old + "\n")
			s.res.Notes = append(s.res.Notes, "kept "+shortRef(ref)+", deleted upstream")
		case still && now != old && !s.isAncestor(ctx, old, now):
			batch.WriteString("create " + rewrittenPrefix + short + "/" + ts + " " + old + "\n")
			s.res.Notes = append(s.res.Notes, "saved the old history of "+shortRef(ref)+" before an upstream force-push")
		}
	}
	if batch.Len() == 0 {
		return nil
	}
	_, err = s.git.RunInput(ctx, s.dir, batch.String(), "update-ref", "--stdin")
	return err
}

func conflictFiles(list string) string {
	files := strings.Fields(list)
	switch {
	case len(files) == 0:
		return ""
	case len(files) > 3:
		return " in " + strings.Join(files[:3], ", ") + " and " + plural(len(files)-3, "more file")
	}
	return " in " + strings.Join(files, ", ")
}

func writeMarker(gitDir string, m marker) error {
	data, _ := json.Marshal(m)
	return os.WriteFile(filepath.Join(gitDir, markerFile), data, 0o600)
}
