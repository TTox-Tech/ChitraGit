package syncer

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// Options controls a sync run. Zero values get sensible defaults.
type Options struct {
	Mode              string         // fetch | ff | dry-run
	Workers           int            // 0 = min(16, 2×CPU)
	PerHostLimit      map[string]int // max parallel repos per host; hosts not listed share only Workers
	Timeout           time.Duration  // per repo
	Retries           int            // extra attempts for network, timeout and rate-limit errors
	RetryDelay        time.Duration  // first backoff step; doubles per attempt, plus jitter
	Prune             bool           // plain prune; off keeps deleted branches under refs/chitragit/archived
	StashLocalChanges bool           // put local changes aside, update, put them back
	FetchAllRemotes   bool
	Submodules        bool
}

// Job is one repo to sync. Dir is its absolute folder.
type Job struct {
	Repo core.Repo
	Dir  string
}

// Result is what happened to one repo.
type Result struct {
	Repo     core.Repo
	Outcome  string // Updated | Unchanged | Skipped | Failed
	Status   string // Status* constant stored on the repo
	Reason   string // plain-language explanation, empty when all is well
	Category string // error category for failures
	Err      error  // the full error, with git's output, for failures
	Changes  []core.RefChange
	Notes    []string // history kept on the side (archived or rewritten refs)
	Remotes  map[string]string
	Attempts int
	Started  time.Time
	Finished time.Time
}

// EventKind tells a listener what happened.
type EventKind int

const (
	RepoStarted EventKind = iota
	RepoFinished
)

// Event is emitted for every repo. Listeners are called one at a time.
type Event struct {
	Kind   EventKind
	Repo   core.Repo
	Result *Result // set for RepoFinished
}

// breakAfter is how many repos on one host may fail with network errors,
// with no success in between, before the rest of that host fails fast.
// It stops an offline run from waiting through every repo's retries.
const breakAfter = 5

// Run syncs jobs in priority order (the order given) and returns one result
// per job, in the same order. Cancelling ctx stops new repos from starting;
// repos already running finish their current step safely.
func Run(ctx context.Context, git *gitx.Runner, jobs []Job, opts Options, listen func(Event)) []Result {
	if opts.Workers <= 0 {
		opts.Workers = min(16, 2*runtime.NumCPU())
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 2 * time.Second
	}
	if opts.Mode == "" {
		opts.Mode = "ff"
	}

	p := &pool{git: git, opts: opts, listen: listen, hosts: map[string]*hostState{}}
	results := make([]Result, len(jobs))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(opts.Workers, max(len(jobs), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = p.syncOne(ctx, jobs[i])
			}
		}()
	}
feed:
	for i := range jobs {
		if ctx.Err() != nil {
			break
		}
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	return results
}

type pool struct {
	git    *gitx.Runner
	opts   Options
	listen func(Event)

	mu    sync.Mutex // guards hosts and serialises listen
	hosts map[string]*hostState
}

type hostState struct {
	slots    chan struct{} // nil = no per-host limit
	failures int           // consecutive network failures
}

func (p *pool) emit(e Event) {
	if p.listen == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listen(e)
}

func (p *pool) host(name string) *hostState {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.hosts[name]
	if !ok {
		h = &hostState{}
		if n := p.opts.PerHostLimit[name]; n > 0 {
			h.slots = make(chan struct{}, n)
		}
		p.hosts[name] = h
	}
	return h
}

func (p *pool) syncOne(ctx context.Context, job Job) (res Result) {
	res = Result{Repo: job.Repo, Started: time.Now()}
	p.emit(Event{Kind: RepoStarted, Repo: job.Repo})
	defer func() {
		// A bug in one repo's pipeline must not stop the run (PRD §9).
		if v := recover(); v != nil {
			err := &core.Error{Category: core.CatInternal, Message: "internal error (please report it)",
				Detail: fmt.Sprintf("%v\n%s", v, debug.Stack())}
			res.Outcome, res.Status, res.Reason, res.Category, res.Err = Failed, StatusFailed, err.Message, string(err.Category), err
		}
		res.Finished = time.Now()
		p.emit(Event{Kind: RepoFinished, Repo: job.Repo, Result: &res})
	}()

	h := p.host(job.Repo.Host)
	if h.slots != nil {
		select {
		case h.slots <- struct{}{}:
			defer func() { <-h.slots }()
		case <-ctx.Done():
			return res // never started: no outcome, nothing recorded
		}
	}
	if p.hostDown(h) {
		res.Outcome, res.Status, res.Category = Failed, StatusFailed, string(core.CatNetwork)
		res.Reason = "skipped because " + job.Repo.Host + " could not be reached for several repos in a row"
		return res
	}

	// A started repo runs to the end of its pipeline even after Ctrl+C:
	// killing git mid-update could leave lock files or a half-moved branch.
	// Its own timeout still bounds it, and a second Ctrl+C exits at once.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.opts.Timeout)
	defer cancel()
	s := &repoSync{git: p.git, dir: job.Dir, opts: &p.opts, res: &res, stop: ctx}
	s.fetch = func(ctx context.Context, args ...string) error {
		return s.withRetry(ctx, func(ctx context.Context) error {
			_, err := p.git.Run(ctx, job.Dir, args...)
			return err
		})
	}
	s.run(rctx)
	p.noteHost(h, res)
	return res
}

func (p *pool) hostDown(h *hostState) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return h.failures >= breakAfter
}

func (p *pool) noteHost(h *hostState, res Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case res.Category == string(core.CatNetwork):
		h.failures++
	case res.Outcome == Updated || res.Outcome == Unchanged:
		h.failures = 0
	}
}

// withRetry runs op, retrying network, timeout and rate-limit errors with
// exponential backoff and jitter (PRD §9.4). Other errors fail at once.
func (s *repoSync) withRetry(ctx context.Context, op func(context.Context) error) error {
	delay := s.opts.RetryDelay
	for attempt := 0; ; attempt++ {
		s.res.Attempts++
		err := op(ctx)
		if err == nil || attempt >= s.opts.Retries || !retryable(core.CategoryOf(err)) || s.stop.Err() != nil {
			return err
		}
		wait := delay + rand.N(delay)
		delay *= 2
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return err
		case <-s.stop.Done():
			return err
		}
	}
}

func retryable(c core.Category) bool {
	return c == core.CatNetwork || c == core.CatTimeout || c == core.CatRateLimited
}
