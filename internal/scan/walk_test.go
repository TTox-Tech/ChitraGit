package scan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// tree creates folders and files under root. A name ending in "/" is a folder.
func tree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if name[len(name)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWalkFindsEveryKind(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{
		"a/one/.git/":                     "",
		"a/one/src/inner/.git/":           "",                               // nested independent repo
		"a/one/lib/sub/.git":              "gitdir: ../../.git/modules/sub", // submodule: skipped
		"a/one/node_modules/x/.git/":      "",                               // excluded by default config
		"b/mirror.git/HEAD":               "ref: refs/heads/main",
		"b/mirror.git/objects/":           "",
		"b/mirror.git/refs/":              "",
		"b/mirror.git/inside/.git/":       "", // never searched inside a bare repo
		"c/wt/.git":                       "gitdir: /x/main/.git/worktrees/wt",
		"d/sep/.git":                      "gitdir: /elsewhere/sep.git",
		"e/skipme/.git/":                  "",
		"f/deep/er/still/.git/":           "",
		".chitragit/x/.git/":              "", // archive metadata is never scanned
		IgnoreFile:                        "# comment\ne/\n!keep\n",
		"plain/folder/without/repos/file": "",
	})

	res, err := Walk(context.Background(), root, Options{Exclude: []string{"node_modules"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Found{
		{"a/one", core.KindNormal},
		{"a/one/src/inner", core.KindNested},
		{"b/mirror.git", core.KindBare},
		{"c/wt", core.KindWorktree},
		{"d/sep", core.KindNormal},
		{"f/deep/er/still", core.KindNormal},
	}
	if !reflect.DeepEqual(res.Repos, want) {
		t.Errorf("repos:\n got %v\nwant %v", res.Repos, want)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("want one warning for the unsupported !pattern, got %v", res.Warnings)
	}
}

func TestWalkMaxDepthAndAnchoredPattern(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{
		"x/.git/":       "",
		"y/z/.git/":     "",
		"skip/me/.git/": "",
		"keep/me/.git/": "",
	})
	res, err := Walk(context.Background(), root, Options{MaxDepth: 1, Exclude: []string{"/skip/me"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Found{{"x", core.KindNormal}}; !reflect.DeepEqual(res.Repos, want) {
		t.Errorf("depth 1: got %v", res.Repos)
	}
	res, _ = Walk(context.Background(), root, Options{Exclude: []string{"/skip/me"}})
	if len(res.Repos) != 3 {
		t.Errorf("anchored exclude: got %v", res.Repos)
	}
}

func TestWalkSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need admin rights on Windows")
	}
	root := t.TempDir()
	tree(t, root, map[string]string{"real/r/.git/": ""})
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "real", "loop")); err != nil {
		t.Fatal(err)
	}

	res, _ := Walk(context.Background(), root, Options{})
	if len(res.Repos) != 1 {
		t.Errorf("symlinks are not followed by default: %v", res.Repos)
	}
	res, err := Walk(context.Background(), root, Options{FollowSymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 1 {
		t.Errorf("following symlinks must not loop or double count: %v", res.Repos)
	}
}

func TestWalkRejectsBadPattern(t *testing.T) {
	if _, err := Walk(context.Background(), t.TempDir(), Options{Exclude: []string{"["}}); core.CategoryOf(err) != core.CatConfig {
		t.Errorf("bad pattern: %v", err)
	}
}
