package syncer

import (
	"context"
	"sync"
	"time"
)

// hostGate decides how many repos of one host run at once. It starts at
// the host's cap and adapts to the server the way TCP does: when the host
// rate limits a request, the limit halves and no new repo of that host
// starts for a short pause; every limit-many repos that finish without a
// rate limit raise it by one again, back up to the cap.
type hostGate struct {
	mu       sync.Mutex
	max      int       // the cap: per_host_limit, else the worker count
	limit    int       // repos of this host allowed to run now, 1..max
	active   int       // repos of this host running
	until    time.Time // no new repo starts before this
	cut      time.Time // when the limit was last halved
	good     int       // repos finished since the limit last changed
	failures int       // consecutive network failures, see breakAfter
	changed  chan struct{}
}

func newHostGate(max int) *hostGate {
	return &hostGate{max: max, limit: max, changed: make(chan struct{})}
}

// wake tells waiting repos that the gate changed. The caller holds mu.
func (g *hostGate) wake() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// enter waits until another repo of this host may start.
func (g *hostGate) enter(ctx context.Context) error {
	for {
		g.mu.Lock()
		wait := time.Until(g.until)
		if g.active < g.limit && wait <= 0 {
			g.active++
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		g.mu.Unlock()

		var timer *time.Timer
		var fire <-chan time.Time
		if wait > 0 {
			timer = time.NewTimer(wait)
			fire = timer.C
		}
		select {
		case <-changed:
		case <-fire:
		case <-ctx.Done():
		}
		if timer != nil {
			timer.Stop()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// leave is called when a repo of this host finishes. limited is whether
// it was rate limited at any point.
func (g *hostGate) leave(limited bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if !limited {
		if g.good++; g.good >= g.limit && g.limit < g.max {
			g.limit++
			g.good = 0
		}
	}
	g.wake()
}

// limited is called each time a request to this host is rate limited.
// Many repos hit the limit at once, so the limit halves at most once per
// pause; the pause itself is extended by each new hit.
func (g *hostGate) limited(pause time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if now.Sub(g.cut) >= pause {
		g.limit = max(1, g.limit/2)
		g.cut = now
		g.good = 0
	}
	g.until = now.Add(pause)
	g.wake()
}

// current is how many repos of the host may run at once now.
func (g *hostGate) current() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}
