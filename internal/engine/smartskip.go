package engine

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// smartSkip asks provider APIs which repos changed since their last sync
// and returns the indexes of jobs that need no fetch (PRD F2). It is
// conservative: a repo is skipped only when
//
//   - its last sync ended "ok" (branches level with upstream, nothing
//     waiting to be applied),
//   - it has exactly one remote, and that remote is the API's repo,
//   - nothing was pushed to it (any branch or tag) since that sync, and
//   - the remote's default-branch head equals our copy of it.
//
// Anything unsure is fetched as usual. It also returns the "archived
// upstream" flag for every repo the API answered for.
func smartSkip(ctx context.Context, git *gitx.Runner, reg *provider.Registry, st *store.Store, jobs []syncer.Job) (map[int]bool, map[string]bool, []string) {
	remotes, err := st.Remotes(ctx)
	if err != nil {
		return nil, nil, []string{"smart skip is off for this run: " + err.Error()}
	}
	type cand struct {
		idx    int
		remote string
		ref    provider.Ref
		synced time.Time
	}
	byHost := map[string][]cand{}
	for i, j := range jobs {
		r := j.Repo
		if r.Status != syncer.StatusOK || r.Kind == core.KindWorktree || r.Owner == "" || r.Name == "" ||
			reg.KindOf(r.Host) != provider.GitHub {
			continue
		}
		synced, err := time.Parse(time.RFC3339Nano, r.LastSyncedAt)
		if err != nil || len(remotes[r.ID]) != 1 {
			continue
		}
		for name, url := range remotes[r.ID] {
			p := provider.Parse(url)
			if strings.EqualFold(p.Host, r.Host) && strings.EqualFold(p.Owner, r.Owner) && strings.EqualFold(p.Name, r.Name) {
				byHost[r.Host] = append(byHost[r.Host], cand{i, name, provider.Ref{Owner: r.Owner, Name: r.Name}, synced})
			}
		}
	}

	skip := map[int]bool{}
	archived := map[string]bool{}
	var warnings []string
	var mu sync.Mutex
	for host, cands := range byHost {
		client := reg.For(host)
		if client == nil {
			continue
		}
		refs := make([]provider.Ref, len(cands))
		for i, c := range cands {
			refs[i] = c.ref
		}
		heads, err := client.Heads(ctx, refs)
		if errors.Is(err, provider.ErrUnsupported) {
			continue // no token: GraphQL needs one; fetch as usual
		}
		if err != nil {
			warnings = append(warnings, "smart skip: "+host+": "+plainError(err)+"; those repos are fetched instead")
		}

		sem := make(chan struct{}, 16)
		var wg sync.WaitGroup
		for _, c := range cands {
			h, ok := heads[c.ref]
			if !ok {
				continue
			}
			archived[jobs[c.idx].Repo.ID] = h.Archived
			if h.SHA == "" || h.PushedAt.After(c.synced.Add(-pushSlack)) {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(c cand, h provider.Head) {
				defer func() { <-sem; wg.Done() }()
				j := jobs[c.idx]
				// Our copy of the default branch: the remote-tracking ref in
				// a normal clone, the branch itself in a bare mirror.
				tracking := path.Join("refs/remotes", c.remote, h.Branch)
				if j.Repo.Kind == core.KindBare {
					tracking = "refs/heads/" + h.Branch
				}
				sha, err := git.Output(ctx, j.Dir, "rev-parse", "--verify", "--quiet", tracking+"^{commit}")
				if err == nil && sha == h.SHA {
					mu.Lock()
					skip[c.idx] = true
					mu.Unlock()
				}
			}(c, h)
		}
		wg.Wait()
	}
	return skip, archived, warnings
}

// pushSlack widens "pushed since the last sync" to cover a push that
// landed while that sync was running, and clock skew with the provider.
const pushSlack = 15 * time.Minute

func plainError(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}
