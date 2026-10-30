package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// A conflict-resolution session (internal/conflict) reuses the stash flow's
// backup ref, stash and marker, so it gets the same crash safety: until the
// session is laid out, an interrupted session is rolled back by the next
// sync like any interrupted update.
const (
	FlowResolve    = "resolve"   // Marker.Kind of a resolve session
	PhaseStarting  = "starting"  // changes stashed, conflicts not laid out yet: roll back on crash
	PhaseResolving = "resolving" // the user is resolving: never roll back automatically
)

// errResolving tells the pipeline to leave a repo alone because a resolve
// session is open in it.
var errResolving = errors.New("a conflict-resolution session is open")

// Flow is an update with local changes put aside: a backup ref holding the
// original HEAD, a stash holding the changes, and the marker file that
// records both.
type Flow struct {
	Marker
	GitDir   string
	StashSHA string // the stash commit; empty if it is gone
	s        *repoSync
}

// Busy returns why the repo whose git dir is gitDir must not be touched (a
// merge, rebase or other operation in progress), or "".
func Busy(gitDir string) string {
	for file, why := range inProgress {
		if _, err := os.Stat(filepath.Join(gitDir, file)); err == nil {
			return why
		}
	}
	return ""
}

func newFlow(ctx context.Context, git *gitx.Runner, dir string) (*Flow, error) {
	gitDir, err := git.Output(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	return &Flow{GitDir: gitDir, s: &repoSync{git: git, dir: dir, gitDir: gitDir, res: &Result{}}}, nil
}

// OpenFlow returns the update in progress in the repo at dir, or nil.
func OpenFlow(ctx context.Context, git *gitx.Runner, dir string) (*Flow, error) {
	f, err := newFlow(ctx, git, dir)
	if err != nil {
		return nil, err
	}
	m, err := readMarker(f.GitDir)
	if err != nil || m == nil {
		return nil, err
	}
	f.Marker = *m
	if f.StashSHA, err = f.s.findStash(ctx, m.Stash); err != nil {
		return nil, err
	}
	return f, nil
}

// BeginFlow backs up HEAD, writes the marker m (Backup, Stash and Started
// are filled in) and stashes every local change, untracked files included.
// If there was nothing to stash, the flow is finished at once and the
// returned Flow has no StashSHA.
func BeginFlow(ctx context.Context, git *gitx.Runner, dir string, m Marker) (*Flow, error) {
	f, err := newFlow(ctx, git, dir)
	if err != nil {
		return nil, err
	}
	ts := stamp()
	m.Backup, m.Stash, m.Started = backupPrefix+ts, "chitragit-resolve "+ts, time.Now().UTC().Format(time.RFC3339)
	f.Marker = m
	s := f.s
	head, err := s.out(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if _, err := git.Run(ctx, dir, "update-ref", m.Backup, head); err != nil {
		return nil, err
	}
	if err := f.Save(); err != nil {
		s.finishFlow(ctx, f.GitDir, m)
		return nil, err
	}
	step("flow.marked")
	if _, err := git.Run(ctx, dir, "stash", "push", "--include-untracked", "--quiet", "-m", m.Stash); err != nil {
		s.finishFlow(ctx, f.GitDir, m)
		return nil, err
	}
	step("flow.stashed")
	if f.StashSHA, err = s.findStash(ctx, m.Stash); err != nil || f.StashSHA == "" {
		s.finishFlow(ctx, f.GitDir, m)
		return f, err
	}
	return f, nil
}

// Save rewrites the marker file.
func (f *Flow) Save() error {
	if err := writeMarker(f.GitDir, f.Marker); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write the update marker", Err: err}
	}
	return nil
}

// Rollback puts the repo back exactly as it was before the flow began and
// ends the flow.
func (f *Flow) Rollback(ctx context.Context) error {
	if f.StashSHA == "" {
		// Nothing was stashed, or it was already put back.
		f.s.finishFlow(ctx, f.GitDir, f.Marker)
		return nil
	}
	return f.s.restore(ctx, f.GitDir, f.Marker, f.StashSHA)
}

// Finish ends the flow keeping the working tree as it is. The stash commit
// is first saved under keepRef, so the original changes stay reachable.
// The marker goes before the stash entry: a crash in between leaves a
// harmless extra stash entry, never a flow that a sync would roll back.
func (f *Flow) Finish(ctx context.Context, keepRef string) error {
	if f.StashSHA != "" && keepRef != "" {
		if _, err := f.s.git.Run(ctx, f.s.dir, "update-ref", keepRef, f.StashSHA); err != nil {
			return err
		}
	}
	step("flow.kept")
	if err := os.Remove(filepath.Join(f.GitDir, markerFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &core.Error{Category: core.CatDisk, Message: "cannot remove the update marker", Err: err}
	}
	step("flow.unmarked")
	if f.StashSHA != "" {
		if err := f.s.dropStash(ctx, f.StashSHA); err != nil {
			return err
		}
	}
	f.s.finishFlow(ctx, f.GitDir, f.Marker)
	return nil
}

// Stamp returns a ref-safe UTC timestamp, for refs under refs/chitragit/.
func Stamp() string { return stamp() }

// step is a test hook called between the steps that change a repo. Tests
// make it panic to simulate a crash at that point.
var step = func(string) {}

// Step calls the test hook; packages that build on Flow call it between
// their own steps.
func Step(name string) { step(name) }

// SetStepHook installs a hook called between steps; for crash tests only.
// It returns a function that restores the previous hook.
func SetStepHook(h func(name string)) (restore func()) {
	old := step
	step = h
	return func() { step = old }
}

func readMarker(gitDir string) (*Marker, error) {
	data, err := os.ReadFile(filepath.Join(gitDir, markerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var m Marker
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	if err != nil || !strings.HasPrefix(m.Backup, backupPrefix) {
		return nil, &core.Error{Category: core.CatCorrupt, Message: "found an unreadable update marker",
			Hint: "check the repo by hand, then delete .git/" + markerFile, Err: err}
	}
	return &m, nil
}
