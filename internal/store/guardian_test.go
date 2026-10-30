package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestRescueNeedsConfirmationsFromDifferentRuns(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ApplyScan(ctx, []core.Repo{{ID: "r1", Path: "o/r", Kind: core.KindNormal}}); err != nil {
		t.Fatal(err)
	}
	run := func() int64 {
		id, err := s.StartRun(ctx, "sync", "ff", []string{"r1"}, 1, "h")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	r1 := run()
	for range 3 { // the same run never counts twice
		if r, newly, err := s.NoteFailure(ctx, "r1", r1, "gone", "404", 2); err != nil || newly || r.Confirmations != 1 {
			t.Fatalf("first run: %+v newly=%v err=%v", r, newly, err)
		}
	}
	// A successful sync in between resets an unconfirmed rescue.
	r2 := run()
	if err := s.RecordItem(ctx, r2, RunItem{RepoID: "r1", Outcome: "unchanged", Status: "ok", FinishedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.NoteFailure(ctx, "r1", run(), "gone", "404", 2); r.Confirmations != 1 {
		t.Fatalf("after a success the count must restart: %+v", r)
	}
	r, newly, err := s.NoteFailure(ctx, "r1", run(), "legal", "451", 2)
	if err != nil || !newly || r.RescuedAt == "" || r.Reason != "legal" {
		t.Fatalf("second run should rescue: %+v newly=%v err=%v", r, newly, err)
	}
	repos, _ := s.Repos(ctx)
	if !repos[0].Rescued || !repos[0].Locked || repos[0].Status != "rescued" || repos[0].RescueReason != "legal" {
		t.Fatalf("repo after rescue = %+v", repos[0])
	}

	// A confirmed rescue survives later successful syncs.
	if err := s.RecordItem(ctx, run(), RunItem{RepoID: "r1", Outcome: "unchanged", Status: "ok", FinishedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Rescues(ctx); len(list) != 1 {
		t.Fatalf("rescue lost: %+v", list)
	}
	if err := s.SetLocked(ctx, "r1", false); err != nil {
		t.Fatal(err)
	}
	if repos, _ := s.Repos(ctx); repos[0].Locked || !repos[0].Rescued {
		t.Fatalf("unlock keeps the badge: %+v", repos[0])
	}
}

func TestSources(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if added, err := s.AddSource(ctx, "github:torvalds", ""); err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	if added, _ := s.AddSource(ctx, "github:torvalds", `{"forks":true}`); added {
		t.Fatal("second add updates filters")
	}
	list, _ := s.Sources(ctx)
	if len(list) != 1 || list[0].Filters != `{"forks":true}` {
		t.Fatalf("sources = %+v", list)
	}
	if err := s.SourceChecked(ctx, list[0].ID, "rate limited"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.RemoveSource(ctx, "github:torvalds"); !ok {
		t.Fatal("remove")
	}
}
