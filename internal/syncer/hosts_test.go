package syncer

import (
	"context"
	"testing"
	"time"
)

func TestHostGateBacksOffAndRecovers(t *testing.T) {
	g := newHostGate(8)
	ctx := context.Background()
	for range 8 {
		if err := g.enter(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// A burst of rate limits halves the limit once, not once per repo.
	for range 5 {
		g.limited(50 * time.Millisecond)
	}
	if got := g.current(); got != 4 {
		t.Fatalf("limit after a burst = %d, want 4", got)
	}
	for range 8 {
		g.leave(true)
	}

	// During the pause nothing starts, even with free room.
	start := time.Now()
	if err := g.enter(ctx); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < 40*time.Millisecond {
		t.Errorf("entered %v into the pause", waited)
	}
	g.leave(false)

	// Each limit-many clean repos raise the limit by one, up to the cap.
	for range 200 {
		if err := g.enter(ctx); err != nil {
			t.Fatal(err)
		}
		g.leave(false)
	}
	if got := g.current(); got != 8 {
		t.Errorf("limit after recovering = %d, want 8", got)
	}
}

func TestHostGateLimitsAndCancels(t *testing.T) {
	g := newHostGate(1)
	if err := g.enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.enter(ctx); err == nil {
		t.Fatal("a second repo entered a gate of one")
	}
	done := make(chan error)
	go func() { done <- g.enter(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	g.leave(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("a waiting repo did not start when the first one left")
	}
	// Repeated limits never drop below one.
	for range 3 {
		g.limited(0)
	}
	if got := g.current(); got != 1 {
		t.Errorf("limit = %d, want 1", got)
	}
}
