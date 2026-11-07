package store

import (
	"context"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// The timeline orders every kind of entry, and keeps a run's pre snapshot
// before its ref changes and its post snapshot after them.
func TestTimelineOrder(t *testing.T) {
	ctx := context.Background()
	s, _ := openLedger(t)
	if _, err := s.ApplyScan(ctx, []core.Repo{{ID: "r1", Path: "a/r1", Kind: core.KindNormal}}); err != nil {
		t.Fatal(err)
	}
	sync := func(start, end string, before, after map[string]string, c core.RefChange) int64 {
		t.Helper()
		run, err := s.StartRun(ctx, "sync", "safe", []string{"r1"}, 1, "h")
		if err != nil {
			t.Fatal(err)
		}
		it := RunItem{RepoID: "r1", Outcome: "updated", Status: "ok", StartedAt: start, FinishedAt: end,
			Changes: []core.RefChange{c}, Before: before, After: after}
		if err := s.RecordItem(ctx, run, it); err != nil {
			t.Fatal(err)
		}
		return run
	}
	main := "refs/heads/main"
	sync("2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", map[string]string{}, map[string]string{main: "a"},
		core.RefChange{Ref: main, NewSHA: "a", Kind: "create"})
	sync("2026-01-02T00:00:00Z", "2026-01-02T00:00:01Z", map[string]string{main: "a"}, map[string]string{main: "b"},
		core.RefChange{Ref: main, OldSHA: "a", NewSHA: "b", Commits: 1, Kind: "update"})
	r3 := sync("2026-01-03T00:00:00Z", "2026-01-03T00:00:01Z", map[string]string{main: "b"}, map[string]string{main: "c"},
		core.RefChange{Ref: main, OldSHA: "b", NewSHA: "c", Kind: "rewrite"})
	r4 := sync("2026-01-04T00:00:00Z", "2026-01-04T00:00:01Z", map[string]string{main: "c", "refs/heads/x": "d"},
		map[string]string{main: "c"}, core.RefChange{Ref: "refs/heads/x", OldSHA: "d", Kind: "delete"})
	if _, _, err := s.NoteFailure(ctx, "r1", r4+1, "gone", "404", 1); err != nil {
		t.Fatal(err)
	}

	got, err := s.Timeline(ctx, "r1", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Newest first. Each run: post snapshot, change, pre snapshot (a pre
	// equal to the last post is not stored again).
	want := []string{"rescued",
		"snapshot:post", "delete", "snapshot:pre",
		"snapshot:post", "rewrite",
		"snapshot:post", "update",
		"snapshot:post", "create", "snapshot:pre"}
	if len(got) != len(want) {
		t.Fatalf("timeline has %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		k := e.Kind
		if k == TimelineSnapshot {
			k += ":" + e.SnapshotKind
		}
		if k != want[i] {
			t.Fatalf("entry %d = %s, want %s (%+v)", i, k, want[i], got)
		}
	}
	if got[4].RunID != r3 || got[5].OldSHA != "b" || got[5].NewSHA != "c" || got[0].Message == "" {
		t.Fatalf("entry fields lost: %+v", got)
	}

	if got, err := s.Timeline(ctx, "r1", 3); err != nil || len(got) != 3 || got[2].Kind != "delete" {
		t.Fatalf("limited timeline = %+v, %v", got, err)
	}
}
