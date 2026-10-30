package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// upstream makes a bare repo with one commit at dir and returns its HEAD.
func upstream(t *testing.T, dir string) string {
	t.Helper()
	git(t, ".", "init", "-q", "--bare", dir)
	dev := filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", dir, dev)
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dev, "push", "-q", "origin", "HEAD")
	return git(t, dev, "rev-parse", "HEAD")
}

// fakeHost serves a provider API and maps https://example.test/ to a local
// folder of bare repos, so git and the API agree on what exists.
func fakeHost(t *testing.T, routes map[string]http.HandlerFunc) (*provider.Registry, string) {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		mux.HandleFunc(p, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &provider.Registry{Hosts: map[string]string{"example.test": provider.GitHub}, BaseURL: srv.URL,
		Token: func(string) string { return "tok" }}, t.TempDir()
}

// cloneVia clones a repo with a remote URL on example.test that the repo's
// own config rewrites to the local folder.
func cloneVia(t *testing.T, root, name, upDir, remote string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	git(t, ".", "clone", "-q", filepath.Join(upDir, strings.TrimPrefix(remote, "https://example.test/")), dir)
	git(t, dir, "remote", "set-url", "origin", remote)
	git(t, dir, "config", "url."+upDir+"/.insteadOf", "https://example.test/")
	return dir
}

func syncReq(root string, reg *provider.Registry) SyncRequest {
	return SyncRequest{Root: root, Providers: reg, SmartSkip: true, Options: syncer.Options{Mode: "ff", RetryDelay: time.Millisecond},
		Guardian: GuardianOptions{Confirmations: 2, AutoBundle: true, BundlesDir: filepath.Join(root, ".chitragit", "bundles")}}
}

func TestRescueAfterTwoRunsWithBundle(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	reg, upDir := fakeHost(t, map[string]http.HandlerFunc{
		"GET /repos/o/gone": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnavailableForLegalReasons) },
	})
	upstream(t, filepath.Join(upDir, "o", "gone.git"))
	dir := cloneVia(t, root, "gone", upDir, "https://example.test/o/gone.git")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	// Upstream disappears.
	if err := os.RemoveAll(filepath.Join(upDir, "o")); err != nil {
		t.Fatal(err)
	}

	rep, err := Sync(ctx, syncReq(root, reg), g, st)
	if err != nil || rep.Counts[syncer.Failed] != 1 || len(rep.Rescued) != 0 {
		t.Fatalf("first run must only count: %+v %v", rep, err)
	}
	rep, err = Sync(ctx, syncReq(root, reg), g, st)
	if err != nil || len(rep.Rescued) != 1 || rep.Rescued[0].Reason != "legal" || rep.Rescued[0].Bundle == "" {
		t.Fatalf("second run must rescue: %+v %v", rep.Rescued, err)
	}
	repos, _ := st.Repos(ctx)
	if !repos[0].Rescued || !repos[0].Locked || repos[0].Status != "rescued" {
		t.Fatalf("repo = %+v", repos[0])
	}
	// The bundle is a full, usable copy of the history.
	restored := filepath.Join(t.TempDir(), "restored")
	git(t, ".", "clone", "-q", rep.Rescued[0].Bundle, restored)
	if git(t, restored, "rev-parse", "HEAD") != git(t, dir, "rev-parse", "HEAD") {
		t.Fatal("bundle does not hold the repo's history")
	}
	// And the repo itself was not touched.
	if git(t, dir, "status", "--porcelain") != "" {
		t.Fatal("rescue changed the working tree")
	}
}

func TestRenamedUpstreamIsFollowed(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	reg, upDir := fakeHost(t, map[string]http.HandlerFunc{
		"GET /repos/o/old": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "neo/new", "name": "new",
				"owner": map[string]string{"login": "neo"}, "clone_url": "https://example.test/neo/new.git"})
		},
	})
	upstream(t, filepath.Join(upDir, "o", "old.git"))
	dir := cloneVia(t, root, "r", upDir, "https://example.test/o/old.git")
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	// Upstream renames the repo and gets a new commit.
	if err := os.MkdirAll(filepath.Join(upDir, "neo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(upDir, "o", "old.git"), filepath.Join(upDir, "neo", "new.git")); err != nil {
		t.Fatal(err)
	}

	rep, err := Sync(ctx, syncReq(root, reg), g, st)
	if err != nil || len(rep.Moved) != 1 || rep.Counts[syncer.Failed] != 0 {
		t.Fatalf("rename: %+v %+v %v", rep.Moved, rep.Counts, err)
	}
	if got := git(t, dir, "config", "remote.origin.url"); got != "https://example.test/neo/new.git" {
		t.Fatalf("origin = %s", got)
	}
	repos, _ := st.Repos(ctx)
	if repos[0].Owner != "neo" || repos[0].Name != "new" || repos[0].Rescued {
		t.Fatalf("repo after rename = %+v", repos[0])
	}
}

func TestSmartSkipAvoidsFetch(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	var head string
	pushed := time.Now().Add(-time.Hour)
	reg, upDir := fakeHost(t, map[string]http.HandlerFunc{
		"POST /graphql": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"data":{"r0":{"pushedAt":%q,"isArchived":true,"defaultBranchRef":{"name":"master","target":{"oid":%q}}}}}`,
				pushed.UTC().Format(time.RFC3339), head)
		},
	})
	head = upstream(t, filepath.Join(upDir, "o", "a.git"))
	dir := cloneVia(t, root, "a", upDir, "https://example.test/o/a.git")
	if b := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); b != "master" {
		git(t, dir, "branch", "-m", b, "master")
		git(t, dir, "update-ref", "refs/remotes/origin/master", head)
	}
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	// The first sync fetches (never synced before).
	if rep, err := Sync(ctx, syncReq(root, reg), g, st); err != nil || rep.SmartSkips != 0 || rep.Counts[syncer.Failed] != 0 {
		t.Fatalf("first sync: %+v %v", rep, err)
	}
	// Make any real fetch fail, then sync again: the API vouches for it.
	git(t, dir, "config", "url."+filepath.Join(t.TempDir(), "nowhere")+"/.insteadOf", "https://example.test/")
	git(t, dir, "config", "--unset", "url."+upDir+"/.insteadOf")
	rep, err := Sync(ctx, syncReq(root, reg), g, st)
	if err != nil || rep.SmartSkips != 1 || rep.Counts[syncer.Unchanged] != 1 {
		t.Fatalf("second sync should skip the fetch: %+v %v", rep, err)
	}
	repos, _ := st.Repos(ctx)
	if !repos[0].ArchivedUpstream {
		t.Error("archived flag from the API not stored")
	}

	// A push after the last sync means fetch, even if the head is the same.
	pushed = time.Now().Add(time.Hour)
	if rep, _ := Sync(ctx, syncReq(root, reg), g, st); rep.SmartSkips != 0 {
		t.Fatalf("a recent push must not be skipped: %+v", rep)
	}
}

func TestFollowClonesNewReposOnly(t *testing.T) {
	allowLocalClones = true
	t.Cleanup(func() { allowLocalClones = false })
	ctx := context.Background()
	root, g, st := setup(t)
	upDir := t.TempDir()
	for _, n := range []string{"keep", "Fork", "keep2"} {
		upstream(t, filepath.Join(upDir, n+".git"))
	}
	reg, _ := fakeHost(t, map[string]http.HandlerFunc{
		"GET /orgs/acme/repos": func(w http.ResponseWriter, _ *http.Request) {
			list := []map[string]any{
				{"name": "keep", "owner": map[string]string{"login": "acme"}, "clone_url": filepath.Join(upDir, "keep.git")},
				{"name": "Fork", "owner": map[string]string{"login": "acme"}, "clone_url": filepath.Join(upDir, "Fork.git"), "fork": true},
				{"name": "evil", "owner": map[string]string{"login": "acme"}, "clone_url": "ext::sh -c touch% /tmp/pwned"},
				{"name": "..", "owner": map[string]string{"login": "acme"}, "clone_url": filepath.Join(upDir, "keep2.git")},
			}
			_ = json.NewEncoder(w).Encode(list)
		},
	})
	if _, err := st.AddSource(ctx, "github@example.test:acme", ""); err != nil {
		t.Fatal(err)
	}
	opts := CloneOptions{Root: root, TmpDir: filepath.Join(root, ".chitragit", "tmp"), Layout: "{owner}/{repo}"}
	rep, err := Follow(ctx, opts, reg, g, st)
	if err != nil || rep.Cloned != 1 || rep.Sources[0].Cloned[0] != "acme/keep" || len(rep.Sources[0].Failed) != 2 {
		t.Fatalf("follow: %+v %v", rep.Sources, err)
	}
	if _, err := os.Stat(filepath.Join(root, "acme", "keep", ".git")); err != nil {
		t.Fatal("clone not in place")
	}
	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	// Running again clones nothing: the repo is known now.
	if rep, err = Follow(ctx, opts, reg, g, st); err != nil || rep.Cloned != 0 {
		t.Fatalf("second follow: %+v %v", rep, err)
	}
}

func TestFiltersAndPaths(t *testing.T) {
	no := false
	f := Filters{Archived: &no, MaxSizeMB: 1, Name: "go-*", Language: "go"}
	cases := []struct {
		r    provider.Repo
		want bool
	}{
		{provider.Repo{Name: "go-x", Language: "Go"}, true},
		{provider.Repo{Name: "go-x", Language: "Go", Fork: true}, false},
		{provider.Repo{Name: "go-x", Language: "Go", Archived: true}, false},
		{provider.Repo{Name: "go-x", Language: "Go", SizeKB: 4096}, false},
		{provider.Repo{Name: "rs-x", Language: "Go"}, false},
		{provider.Repo{Name: "go-x", Language: "Rust"}, false},
	}
	for _, c := range cases {
		if got, why := f.Match(c.r); got != c.want {
			t.Errorf("Match(%+v) = %v (%s)", c.r, got, why)
		}
	}
	if back, _ := ParseFilters(f.JSON()); back.Name != "go-*" || *back.Archived {
		t.Errorf("filters round trip: %+v", back)
	}

	c := &cloner{opts: CloneOptions{Layout: "{host}/{owner}/{repo}"}, paths: map[string]bool{"h/o/repo": true}}
	if p, _ := c.pathFor(provider.Repo{Host: "h", Owner: "o", Name: "Repo"}); p != "h/o/Repo-2" {
		t.Errorf("case collision path = %s", p)
	}
	for _, bad := range []string{"..", "a:b", ".git", "x."} {
		if _, err := c.pathFor(provider.Repo{Host: "h", Owner: "o", Name: bad}); err == nil {
			t.Errorf("unsafe name %q accepted", bad)
		}
	}
	for url, ok := range map[string]bool{"https://h/o/r.git": true, "git@h:o/r.git": true, "ssh://git@h/o/r": true,
		"file:///etc": false, "ext::sh -c x": false, "-uhttps://x": false, "/local/path": false} {
		if err := checkCloneURL(url); (err == nil) != ok {
			t.Errorf("checkCloneURL(%q) = %v", url, err)
		}
	}
}
