package perf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

func generate(t *testing.T, opts Options) *Archive {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	a, err := Generate(context.Background(), t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A synthetic archive must look like a real one to the engine: scan finds
// every repo with the right kind and a unique ID, and a fetch of every
// repo succeeds against its upstream.
func TestGenerateScansAndSyncs(t *testing.T) {
	ctx := context.Background()
	a := generate(t, Options{Repos: 400, Templates: 6})
	if len(a.Paths) != 400 || len(a.Upstreams) != 6 {
		t.Fatalf("paths %d, upstreams %d", len(a.Paths), len(a.Upstreams))
	}
	if a.Mix.Bare == 0 || a.Mix.Mirror == 0 || a.Mix.Nested == 0 || a.Mix.Dirty == 0 {
		t.Errorf("mix lacks a kind: %+v", a.Mix)
	}

	g, err := gitx.New(gitx.Options{HooksDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rep, err := engine.Scan(ctx, a.Root, scan.Options{}, g, st)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found != 400 || rep.Added != 400 || len(rep.Warnings) > 0 {
		t.Fatalf("scan = %+v", rep)
	}
	repos, err := st.Repos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	ids := map[string]bool{}
	for _, r := range repos {
		kinds[r.Kind]++
		ids[r.ID] = true
	}
	if want := a.Mix.Bare + a.Mix.Mirror; kinds[core.KindBare] != want {
		t.Errorf("bare repos = %d, want %d", kinds[core.KindBare], want)
	}
	if kinds[core.KindNested] != a.Mix.Nested {
		t.Errorf("nested repos = %d, want %d", kinds[core.KindNested], a.Mix.Nested)
	}
	if len(ids) != 400 {
		t.Errorf("%d distinct IDs for 400 repos", len(ids))
	}

	res, err := engine.Sync(ctx, engine.SyncRequest{Root: a.Root, Options: syncer.Options{Mode: "fetch"}}, g, st)
	if err != nil {
		t.Fatal(err)
	}
	if bad := res.Counts[syncer.Failed]; bad > 0 {
		t.Errorf("%d repos failed to fetch: %+v", bad, res.Counts)
	}
}

// The same seed gives the same layout, so runs on two commits compare.
func TestGenerateIsDeterministic(t *testing.T) {
	a := generate(t, Options{Repos: 120, Templates: 3, Seed: 7})
	b := generate(t, Options{Repos: 120, Templates: 3, Seed: 7})
	if !slices.Equal(a.Paths, b.Paths) || a.Mix != b.Mix {
		t.Fatalf("seed 7 gave two layouts:\n%v\n%v", a.Mix, b.Mix)
	}
	head := func(root, rel string) string {
		out, err := exec.Command("git", "-C", filepath.Join(root, rel), "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, rel := range a.Paths[:20] {
		if head(a.Root, rel) != head(b.Root, rel) {
			t.Errorf("%s: HEAD differs between two runs with the same seed", rel)
		}
	}
	if c := generate(t, Options{Repos: 120, Templates: 3, Seed: 8}); slices.Equal(a.Paths, c.Paths) {
		t.Error("seeds 7 and 8 gave the same layout")
	}
}

func TestGenerateRefusesAFolderInUse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(context.Background(), dir, Options{Repos: 1}); err == nil {
		t.Fatal("generated into a folder that was not empty")
	}
}
