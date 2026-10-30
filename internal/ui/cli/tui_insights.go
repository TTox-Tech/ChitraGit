package cli

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/ui/tui"
)

// The localBackend also serves tui.Insights: the detail page, the
// activity feed and the health dashboard. Nothing here takes the archive
// lock or touches a working tree, so it all works during a sync.

var _ tui.Insights = (*localBackend)(nil)

func (b *localBackend) RepoInfo(ctx context.Context, id string) (*store.RepoInfo, error) {
	return b.sess().st.RepoInfo(ctx, id)
}

func (b *localBackend) RepoDir(r core.Repo) string { return engine.RepoDir(b.sess().arc.Root, r) }

func (b *localBackend) RepoGit(ctx context.Context, r core.Repo) (*tui.RepoGit, error) {
	if r.Missing {
		return &tui.RepoGit{}, nil
	}
	git, err := b.runner(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	dir := b.RepoDir(r)
	out := &tui.RepoGit{}
	if out.Commits, err = engine.Commits(ctx, git, dir, "", 300); err != nil {
		return nil, err
	}
	if out.Refs, err = engine.Refs(ctx, git, dir); err != nil {
		return nil, err
	}
	out.Readme, err = engine.ReadReadme(ctx, git, dir)
	return out, err
}

func (b *localBackend) RepoHistory(ctx context.Context, id string) ([]store.RepoRun, error) {
	return b.sess().st.RepoHistory(ctx, id, 50)
}

func (b *localBackend) Activity(ctx context.Context, f store.ActivityFilter) ([]store.Activity, error) {
	return b.sess().st.Activity(ctx, f)
}

func (b *localBackend) staleAfter() time.Duration {
	return time.Duration(b.sess().cfg.Guardian.StaleAfter)
}

func (b *localBackend) Health(ctx context.Context) (*store.Health, error) {
	return b.sess().st.Health(ctx, b.staleAfter())
}

func (b *localBackend) CardRepos(ctx context.Context, card string) ([]string, error) {
	return b.sess().st.RepoIDs(ctx, card, b.staleAfter())
}

func (b *localBackend) MeasureSizes(ctx context.Context) error {
	repos, err := b.sess().st.Repos(ctx)
	if err != nil {
		return err
	}
	return engine.MeasureSizes(ctx, b.sess().arc.Root, repos, b.sess().st)
}

func (b *localBackend) SetFavourite(ctx context.Context, ids []string, fav bool) error {
	return b.sess().st.SetFavourite(ctx, ids, fav)
}

func (b *localBackend) AddTags(ctx context.Context, ids, tags []string) error {
	return b.sess().st.AddTags(ctx, ids, tags)
}

func (b *localBackend) RemoveTags(ctx context.Context, ids, tags []string) error {
	return b.sess().st.RemoveTags(ctx, ids, tags)
}

func (b *localBackend) RepoTags(ctx context.Context) (map[string][]string, error) {
	return b.sess().st.RepoTags(ctx)
}

// Open hands a folder or URL to the desktop. The target is passed as one
// argument, never through a shell.
func (b *localBackend) Open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return &core.Error{Category: core.CatEnvironment, Message: "cannot open " + target, Err: err}
	}
	go func() { _ = cmd.Wait() }() // explorer exits non-zero even on success
	return nil
}
