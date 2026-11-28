package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// age sets every folder under root back to one fixed time, long enough ago for the cache to trust them.
func age(t *testing.T, root string) {
	t.Helper()
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			err = os.Chtimes(p, old, old)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func paths(res *Result) []string {
	var out []string
	for _, f := range res.Repos {
		out = append(out, f.Path)
	}
	return out
}

func TestWalkCache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(t.TempDir(), "scan.cache")
	tree(t, root, map[string]string{
		"a/one/.git/":           "",
		"a/one/src/x.go":        "",
		"b/mirror.git/HEAD":     "ref: refs/heads/main",
		"b/mirror.git/objects/": "",
		"b/mirror.git/refs/":    "",
		"c/wt/.git":             "gitdir: /x/main/.git/worktrees/wt",
	})
	age(t, root)
	opts := Options{CacheFile: cache}
	walk := func(o Options) []string {
		t.Helper()
		res, err := Walk(context.Background(), root, o)
		if err != nil {
			t.Fatal(err)
		}
		return paths(res)
	}
	want := []string{"a/one", "b/mirror.git", "c/wt"}
	if got := walk(opts); !reflect.DeepEqual(got, want) {
		t.Fatalf("first walk = %v", got)
	}
	if got := walk(opts); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached walk = %v", got)
	}

	// A new repo changes its parent's time, so the parent is read again.
	tree(t, root, map[string]string{"a/two/.git/": ""})
	if got := walk(opts); !reflect.DeepEqual(got, []string{"a/one", "a/two", "b/mirror.git", "c/wt"}) {
		t.Errorf("after adding a/two = %v", got)
	}

	// A folder whose time is put back is trusted, so what changed in it is
	// only seen with Full, which also rewrites the cache.
	tree(t, root, map[string]string{"b/three/.git/": ""})
	age(t, filepath.Join(root, "b"))
	if got := walk(opts); len(got) != 4 {
		t.Errorf("cache was not used: %v", got)
	}
	full := opts
	full.Full = true
	if got := walk(full); len(got) != 5 {
		t.Errorf("full walk = %v", got)
	}
	if got := walk(opts); len(got) != 5 {
		t.Errorf("walk after full = %v", got)
	}

	// A folder changed just now is never trusted, however its time looks.
	tree(t, root, map[string]string{"d/four/.git/": ""})
	walk(opts)
	tree(t, root, map[string]string{"d/five/.git/": ""})
	now := time.Now()
	if err := os.Chtimes(filepath.Join(root, "d"), now, now); err != nil {
		t.Fatal(err)
	}
	if got := walk(opts); len(got) != 7 {
		t.Errorf("racy folder was trusted: %v", got)
	}

	// A damaged cache is ignored.
	if err := os.WriteFile(cache, []byte("not a cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := walk(opts); len(got) != 7 {
		t.Errorf("walk with a damaged cache = %v", got)
	}
}

func TestWalkCacheKinds(t *testing.T) {
	// The same tree walked with and without the cache finds the same repos.
	root := t.TempDir()
	tree(t, root, map[string]string{
		"a/one/.git/":           "",
		"a/one/src/inner/.git/": "",
		"a/one/lib/sub/.git":    "gitdir: ../../.git/modules/sub",
		"d/sep/.git":            "gitdir: /elsewhere/sep.git",
	})
	age(t, root)
	cache := filepath.Join(t.TempDir(), "scan.cache")
	plain, err := Walk(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		res, err := Walk(context.Background(), root, Options{CacheFile: cache})
		if err != nil || !reflect.DeepEqual(res.Repos, plain.Repos) {
			t.Fatalf("cached = %v %v, want %v", res, err, plain.Repos)
		}
	}
	if plain.Repos[1].Kind != core.KindNested {
		t.Errorf("kinds = %v", plain.Repos)
	}
}
