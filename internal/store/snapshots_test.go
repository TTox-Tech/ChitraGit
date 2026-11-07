package store

import (
	"context"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestSnapshotDedupe(t *testing.T) {
	ctx := context.Background()
	s, _ := openLedger(t)
	if _, err := s.ApplyScan(ctx, []core.Repo{{ID: "r1", Path: "a/r1", Kind: core.KindNormal}}); err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{"refs/heads/main": "aaa", "refs/tags/v1": "bbb"}
	id1, created, err := s.TakeSnapshot(ctx, "r1", refs)
	if err != nil || !created {
		t.Fatalf("first snapshot = %d %v %v", id1, created, err)
	}
	id2, created, err := s.TakeSnapshot(ctx, "r1", map[string]string{"refs/tags/v1": "bbb", "refs/heads/main": "aaa"})
	if err != nil || created || id2 != id1 {
		t.Fatalf("same refs made a new snapshot: %d %v %v", id2, created, err)
	}
	refs["refs/heads/main"] = "ccc"
	id3, created, err := s.TakeSnapshot(ctx, "r1", refs)
	if err != nil || !created || id3 == id1 {
		t.Fatalf("changed refs not stored: %d %v %v", id3, created, err)
	}

	list, err := s.Snapshots(ctx, "r1", 0)
	if err != nil || len(list) != 2 || list[0].ID != id3 || list[0].Count != 2 || list[0].Path != "a/r1" {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	sn, err := s.Snapshot(ctx, id1)
	if err != nil || sn.Refs["refs/heads/main"] != "aaa" || sn.Kind != SnapshotManual {
		t.Fatalf("snapshot %d = %+v, %v", id1, sn, err)
	}
	if _, err := s.Snapshot(ctx, 999); core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("missing snapshot error = %v", err)
	}
	mustVerify(t, s, 3) // the scan and the two snapshots stored; the reused one logs nothing
}

// A sync's pre snapshot is dated when the refs were read, before the ref
// changes; the post snapshot shares their time.
func TestRunSnapshotTimes(t *testing.T) {
	ctx := context.Background()
	s, _ := openLedger(t)
	if _, err := s.ApplyScan(ctx, []core.Repo{{ID: "r1", Path: "a/r1", Kind: core.KindNormal}}); err != nil {
		t.Fatal(err)
	}
	run, err := s.StartRun(ctx, "sync", "safe", []string{"r1"}, 1, "h")
	if err != nil {
		t.Fatal(err)
	}
	const start, end = "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z"
	it := RunItem{RepoID: "r1", Outcome: "updated", Status: "ok", StartedAt: start, FinishedAt: end,
		Before: map[string]string{"refs/heads/main": "aaa"}, After: map[string]string{"refs/heads/main": "bbb"}}
	if err := s.RecordItem(ctx, run, it); err != nil {
		t.Fatal(err)
	}
	list, err := s.Snapshots(ctx, "r1", 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	if list[0].Kind != SnapshotPost || list[0].At != end || list[1].Kind != SnapshotPre || list[1].At != start {
		t.Fatalf("snapshot times = %+v", list)
	}
}
