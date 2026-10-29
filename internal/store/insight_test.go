package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seed(t *testing.T, s *Store, repos ...core.Repo) {
	t.Helper()
	if _, err := s.ApplyScan(context.Background(), repos); err != nil {
		t.Fatal(err)
	}
}

func TestFindRepo(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	seed(t, s,
		core.Repo{ID: "11111111-aaaa", Path: "github/torvalds/linux", Kind: "normal", Owner: "torvalds", Name: "linux", Remotes: map[string]string{}},
		core.Repo{ID: "22222222-bbbb", Path: "mirror/linux", Kind: "bare"},
		core.Repo{ID: "33333333-cccc", Path: "tools/glow", Kind: "normal"},
	)
	for q, want := range map[string]string{
		"tools/glow":            "33333333-cccc",
		"tools\\glow\\":         "33333333-cccc",
		"Torvalds/Linux":        "11111111-aaaa",
		"22222222":              "22222222-bbbb",
		"glow":                  "33333333-cccc",
		"torvalds/linux":        "11111111-aaaa",
		"mirror/linux":          "22222222-bbbb",
		"github/torvalds/linux": "11111111-aaaa",
	} {
		r, err := s.FindRepo(ctx, q)
		if err != nil || r.ID != want {
			t.Errorf("FindRepo(%q) = %s, %v; want %s", q, r.ID, err, want)
		}
	}
	if _, err := s.FindRepo(ctx, "linux"); err == nil {
		t.Error("ambiguous name resolved")
	}
	if _, err := s.FindRepo(ctx, "nope"); core.CategoryOf(err) != core.CatNotFound {
		t.Errorf("missing repo error = %v", err)
	}
}

func TestTagsFavouritesAndInfo(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	seed(t, s,
		core.Repo{ID: "a", Path: "x/a", Kind: "normal", Remotes: map[string]string{"origin": "https://github.com/x/a"}},
		core.Repo{ID: "b", Path: "x/b", Kind: "normal"},
	)
	if err := s.AddTags(ctx, []string{"a", "b"}, []string{" Go ", "", "cli"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTags(ctx, []string{"a"}, []string{"go"}); err != nil { // duplicate is fine
		t.Fatal(err)
	}
	if err := s.RemoveTags(ctx, []string{"b"}, []string{"CLI"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavourite(ctx, []string{"b"}, true); err != nil {
		t.Fatal(err)
	}
	all, _ := s.AllTags(ctx)
	if !slices.Equal(all, []TagCount{{"go", 2}, {"cli", 1}}) {
		t.Errorf("all tags = %v", all)
	}
	byRepo, _ := s.RepoTags(ctx)
	if !slices.Equal(byRepo["a"], []string{"cli", "go"}) || !slices.Equal(byRepo["b"], []string{"go"}) {
		t.Errorf("repo tags = %v", byRepo)
	}

	info, err := s.RepoInfo(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if info.Remotes["origin"] == "" || !slices.Equal(info.Tags, []string{"cli", "go"}) || info.Favourite || info.Rescue != nil {
		t.Errorf("info = %+v", info)
	}
	s.DB.Exec(`INSERT INTO conflicts(repo_id, backup_ref, stash_msg, files, created_at) VALUES ('b', 'refs/chitragit/backup/1', 'm', '["f.txt"]', ?)`, Now())
	s.DB.Exec(`INSERT INTO rescues(repo_id, reason, confirmations, first_failed_at, rescued_at) VALUES ('b', 'deleted_or_private', 2, ?, ?)`, Now(), Now())
	info, err = s.RepoInfo(ctx, "b")
	if err != nil || !info.Favourite || info.Conflict == nil || info.Conflict.Files[0] != "f.txt" || info.Rescue == nil {
		t.Errorf("info b = %+v, %v", info, err)
	}
	if _, err := s.RepoInfo(ctx, "zzz"); err != ErrNoRepo {
		t.Errorf("unknown repo: %v", err)
	}

	h, err := s.Health(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if h.Cards[CardFavourites] != 1 || h.Cards[CardConflicts] != 1 || h.Cards[CardRescued] != 1 {
		t.Errorf("cards = %v", h.Cards)
	}
	ids, err := s.RepoIDs(ctx, CardConflicts, time.Hour)
	if err != nil || !slices.Equal(ids, []string{"b"}) {
		t.Errorf("conflict ids = %v, %v", ids, err)
	}
	if _, err := s.RepoIDs(ctx, "bogus", time.Hour); err == nil {
		t.Error("unknown card accepted")
	}
}

func TestActivityGroupsAndFilters(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	seed(t, s,
		core.Repo{ID: "a", Path: "alice/a", Kind: "normal", Owner: "alice", Name: "a", Remotes: map[string]string{}},
		core.Repo{ID: "b", Path: "bob/b", Kind: "normal", Owner: "bob", Name: "b", Remotes: map[string]string{}},
	)
	run, _ := s.StartRun(ctx, "sync", "ff", []string{"a", "b"}, 1, "h")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	rec := func(id, at string, ch ...core.RefChange) {
		if err := s.RecordItem(ctx, run, RunItem{RepoID: id, Outcome: "updated", Status: "ok", FinishedAt: at, Changes: ch}); err != nil {
			t.Fatal(err)
		}
	}
	rec("a", old, core.RefChange{Ref: "refs/remotes/origin/main", OldSHA: "1", NewSHA: "2", Commits: 3, Kind: "update"},
		core.RefChange{Ref: "refs/tags/v1", NewSHA: "2", Kind: "create"})
	rec("b", Now(), core.RefChange{Ref: "refs/remotes/origin/main", OldSHA: "1", NewSHA: "2", Commits: 1, Kind: "update"})
	s.AddTags(ctx, []string{"b"}, []string{"fav"})
	if err := s.AddEvent(ctx, "a", "rescued", map[string]string{"message": "upstream returned 404"}); err != nil {
		t.Fatal(err)
	}

	feed, err := s.Activity(ctx, ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 3 || feed[0].Type != "rescued" || feed[0].Message != "upstream returned 404" ||
		feed[1].Path != "bob/b" || feed[2].Path != "alice/a" || len(feed[2].Changes) != 2 {
		t.Fatalf("feed = %+v", feed)
	}
	check := func(f ActivityFilter, want ...string) {
		t.Helper()
		got, err := s.Activity(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, a := range got {
			paths = append(paths, a.Type+":"+a.Path)
		}
		if !slices.Equal(paths, want) {
			t.Errorf("%+v → %v, want %v", f, paths, want)
		}
	}
	check(ActivityFilter{Owner: "ALICE"}, "rescued:alice/a", "refs:alice/a")
	check(ActivityFilter{Tag: "fav"}, "refs:bob/b")
	check(ActivityFilter{Types: []string{"refs"}}, "refs:bob/b", "refs:alice/a")
	check(ActivityFilter{Types: []string{"rescued"}}, "rescued:alice/a")
	check(ActivityFilter{Since: time.Now().Add(-time.Hour), Types: []string{"refs"}}, "refs:bob/b")
	check(ActivityFilter{RepoID: "a", Limit: 1}, "rescued:alice/a")

	hist, err := s.RepoHistory(ctx, "a", 0)
	if err != nil || len(hist) != 1 || hist[0].Outcome != "updated" || hist[0].Mode != "ff" {
		t.Errorf("history = %+v, %v", hist, err)
	}
}
