package store

import (
	"context"
	"slices"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestRepoMeta(t *testing.T) {
	ctx := context.Background()
	s, _ := openLedger(t)
	if _, err := s.ApplyScan(ctx, []core.Repo{{ID: "r1", Path: "a/r1", Kind: core.KindNormal},
		{ID: "r2", Path: "a/r2", Kind: core.KindNormal}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.RepoMeta(ctx, "r1"); err != nil || ok {
		t.Fatalf("before any save: %v %v", ok, err)
	}
	want := core.RepoMeta{Description: "notes", Topics: []string{"go", "cli"}, Stars: 7, Language: "Go", License: "MIT",
		Homepage: "https://example.com", Fork: true}
	err := s.SetRepoMeta(ctx, map[string]core.RepoMeta{"r1": want, "r2": {}, "gone": {Stars: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.RepoMeta(ctx, "r1")
	if err != nil || !ok || got.UpdatedAt == "" {
		t.Fatalf("r1 = %+v %v %v", got, ok, err)
	}
	got.UpdatedAt = ""
	if got.Description != want.Description || !slices.Equal(got.Topics, want.Topics) || got.Stars != 7 ||
		got.Language != "Go" || got.License != "MIT" || got.Homepage != want.Homepage || !got.Fork {
		t.Errorf("r1 = %+v, want %+v", got, want)
	}
	// A later answer replaces the row; unknown repos get none.
	if err := s.SetRepoMeta(ctx, map[string]core.RepoMeta{"r1": {Stars: 8}}); err != nil {
		t.Fatal(err)
	}
	all, err := s.AllRepoMeta(ctx)
	if err != nil || len(all) != 2 || all["r1"].Stars != 8 || all["r1"].Description != "" || len(all["r2"].Topics) != 0 {
		t.Errorf("all = %+v %v", all, err)
	}
}
