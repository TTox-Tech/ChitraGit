package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// BenchmarkRecordItem measures what snapshots add to recording one
// unchanged repo's result: the case for almost every repo in a 5k-repo
// sync. Run: go test ./internal/store -bench RecordItem -benchtime 2000x
func BenchmarkRecordItem(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(b.TempDir(), "c.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const repos = 500
	var present []core.Repo
	for i := range repos {
		present = append(present, core.Repo{ID: fmt.Sprint("r", i), Path: fmt.Sprint("o/r", i), Kind: core.KindNormal})
	}
	if _, err := s.ApplyScan(ctx, present); err != nil {
		b.Fatal(err)
	}
	refs := map[string]string{}
	for i := range 20 {
		refs[fmt.Sprintf("refs/remotes/origin/branch-%d", i)] = fmt.Sprintf("%040d", i)
	}
	run, err := s.StartRun(ctx, "sync", "ff", nil, 1, "h")
	if err != nil {
		b.Fatal(err)
	}
	for _, snap := range []bool{false, true} {
		b.Run(fmt.Sprintf("snapshots=%v", snap), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				it := RunItem{RepoID: fmt.Sprint("r", i%repos), Outcome: "unchanged", Status: "ok", StartedAt: Now(), FinishedAt: Now()}
				if snap {
					it.Before, it.After = refs, refs
				}
				if err := s.RecordItem(ctx, run, it); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
