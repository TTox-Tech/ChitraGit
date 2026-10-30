package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// fake serves canned API answers. Handlers see the path without the base.
func fake(t *testing.T, kind, token string, routes map[string]http.HandlerFunc) Client {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	reg := &Registry{Hosts: map[string]string{"example.test": kind}, BaseURL: srv.URL,
		Token: func(string) string { return token }}
	return reg.For("example.test")
}

func jsonReply(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func TestGitHubLookup(t *testing.T) {
	c := fake(t, GitHub, "", map[string]http.HandlerFunc{
		"GET /repos/o/ok": jsonReply(map[string]any{"full_name": "o/ok", "name": "ok", "owner": map[string]string{"login": "o"},
			"archived": true, "default_branch": "main"}),
		// A rename: GitHub redirects to the repo's new home.
		"GET /repos/o/old":     http.RedirectHandler("/repositories/42", http.StatusMovedPermanently).ServeHTTP,
		"GET /repositories/42": jsonReply(map[string]any{"full_name": "neo/new", "name": "new", "owner": map[string]string{"login": "neo"}, "clone_url": "https://example.test/neo/new.git"}),
		"GET /repos/o/gone":    status(http.StatusNotFound),
		"GET /repos/o/dmca":    status(http.StatusUnavailableForLegalReasons),
		"GET /repos/o/blocked": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"Repository access blocked"}`)
		},
		"GET /repos/o/limited": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(403)
		},
		"GET /repos/o/needs-login":   status(http.StatusUnauthorized),
		"GET /repos/o/server-broken": status(http.StatusBadGateway),
	})
	ctx := context.Background()
	cases := map[string]Verdict{"ok": Exists, "old": Moved, "gone": Gone, "dmca": Legal, "blocked": Legal}
	for name, want := range cases {
		got, err := c.Lookup(ctx, "o", name)
		if err != nil || got.Verdict != want {
			t.Errorf("Lookup(%s) = %+v, %v; want %s", name, got, err, want)
		}
	}
	if got, _ := c.Lookup(ctx, "o", "old"); got.Repo == nil || got.Repo.FullName() != "neo/new" || got.Repo.CloneURL == "" {
		t.Errorf("moved repo = %+v", got.Repo)
	}
	if got, _ := c.Lookup(ctx, "o", "ok"); !got.Repo.Archived {
		t.Errorf("archived flag lost: %+v", got.Repo)
	}
	errs := map[string]core.Category{"limited": core.CatRateLimited, "needs-login": core.CatAuth, "server-broken": core.CatNetwork}
	for name, want := range errs {
		if _, err := c.Lookup(ctx, "o", name); core.CategoryOf(err) != want {
			t.Errorf("Lookup(%s) error = %v, want category %s", name, err, want)
		}
	}
}

func TestGitHubHeadsBatchesAndNeedsToken(t *testing.T) {
	if _, err := fake(t, GitHub, "", nil).Heads(context.Background(), []Ref{{"o", "a"}}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no token: %v", err)
	}
	queries := 0
	c := fake(t, GitHub, "secret", map[string]http.HandlerFunc{
		"POST /graphql": func(w http.ResponseWriter, r *http.Request) {
			queries++
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("auth header = %q", r.Header.Get("Authorization"))
			}
			var body struct {
				Query     string            `json:"query"`
				Variables map[string]string `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			data := map[string]any{}
			for i := 0; ; i++ {
				name, ok := body.Variables[fmt.Sprintf("n%d", i)]
				if !ok {
					break
				}
				if name == "gone" {
					data[fmt.Sprintf("r%d", i)] = nil
					continue
				}
				data[fmt.Sprintf("r%d", i)] = map[string]any{"pushedAt": "2026-10-01T10:00:00Z", "isArchived": false,
					"defaultBranchRef": map[string]any{"name": "main", "target": map[string]string{"oid": "sha-" + name}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data,
				"errors": []map[string]string{{"type": "NOT_FOUND", "message": "Could not resolve"}}})
		},
	})
	var refs []Ref
	for i := range 150 {
		refs = append(refs, Ref{"o", fmt.Sprintf("r%d", i)})
	}
	refs = append(refs, Ref{"o", "gone"})
	heads, err := c.Heads(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 2 || len(heads) != 150 || heads[Ref{"o", "r7"}].SHA != "sha-r7" {
		t.Fatalf("queries=%d heads=%d r7=%+v", queries, len(heads), heads[Ref{"o", "r7"}])
	}
	if _, ok := heads[Ref{"o", "gone"}]; ok {
		t.Fatal("a missing repo must be left out")
	}
}

func TestGitHubListPagesAndFallsBackToUser(t *testing.T) {
	var srvURL string
	c := fake(t, GitHub, "", map[string]http.HandlerFunc{
		"GET /orgs/torvalds/repos": status(http.StatusNotFound),
		"GET /users/torvalds/repos": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<`+srvURL+`/users/torvalds/repos?page=2>; rel="next", <x>; rel="last"`)
				_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "linux", "owner": map[string]string{"login": "torvalds"}}})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "subsurface", "owner": map[string]string{"login": "torvalds"}, "fork": true}})
		},
	})
	srvURL = c.(*githubAPI).rest
	repos, err := c.List(context.Background(), Source{Kind: GitHub, Owner: "torvalds"})
	if err != nil || len(repos) != 2 || repos[1].Name != "subsurface" || !repos[1].Fork {
		t.Fatalf("List = %+v, %v", repos, err)
	}
	if _, err := c.List(context.Background(), Source{Kind: GitHub, Starred: true}); core.CategoryOf(err) != core.CatAuth {
		t.Fatalf("starred without token: %v", err)
	}
}

func TestGitLabLookupAndGroups(t *testing.T) {
	c := fake(t, GitLab, "", map[string]http.HandlerFunc{
		"GET /projects/{id}": func(w http.ResponseWriter, r *http.Request) {
			switch r.PathValue("id") {
			case "gnome/sub/gtk":
				_ = json.NewEncoder(w).Encode(map[string]any{"path": "gtk", "path_with_namespace": "gnome/sub/gtk",
					"namespace": map[string]string{"full_path": "gnome/sub"}})
			case "old/name":
				_ = json.NewEncoder(w).Encode(map[string]any{"path": "name", "path_with_namespace": "new/name"})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		},
		"GET /groups/gnome/projects": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("include_subgroups") != "true" {
				t.Error("subgroups not included")
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"path": "gtk", "path_with_namespace": "gnome/sub/gtk",
				"forked_from_project": map[string]any{"id": 1}}})
		},
	})
	ctx := context.Background()
	if l, err := c.Lookup(ctx, "gnome/sub", "gtk"); err != nil || l.Verdict != Exists || l.Repo.Owner != "gnome/sub" {
		t.Fatalf("exists: %+v %v", l, err)
	}
	if l, _ := c.Lookup(ctx, "old", "name"); l.Verdict != Moved || l.Repo.Owner != "new" {
		t.Fatalf("moved: %+v", l)
	}
	if l, _ := c.Lookup(ctx, "x", "y"); l.Verdict != Gone {
		t.Fatalf("gone: %+v", l)
	}
	repos, err := c.List(ctx, Source{Kind: GitLab, Owner: "gnome"})
	if err != nil || len(repos) != 1 || repos[0].Owner != "gnome/sub" || !repos[0].Fork {
		t.Fatalf("List = %+v %v", repos, err)
	}
}

func TestGiteaListPagesWithoutLinkHeader(t *testing.T) {
	c := fake(t, Gitea, "tok", map[string]http.HandlerFunc{
		"GET /orgs/forgejo/repos": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "token tok" {
				t.Errorf("auth header = %q", r.Header.Get("Authorization"))
			}
			n := giteaPage
			if r.URL.Query().Get("page") == "2" {
				n = 3
			}
			batch := make([]map[string]any, n)
			for i := range batch {
				batch[i] = map[string]any{"name": fmt.Sprintf("r%d", i), "owner": map[string]string{"login": "forgejo"}}
			}
			_ = json.NewEncoder(w).Encode(batch)
		},
	})
	repos, err := c.List(context.Background(), Source{Kind: Gitea, Owner: "forgejo"})
	if err != nil || len(repos) != giteaPage+3 {
		t.Fatalf("List = %d repos, %v", len(repos), err)
	}
}

func TestTokenIsRedactedFromNetworkErrors(t *testing.T) {
	reg := &Registry{HTTP: failingDoer{}, Token: func(string) string { return "s3cr3t" }}
	_, err := reg.For("github.com").Lookup(context.Background(), "o", "n")
	if core.CategoryOf(err) != core.CatNetwork || strings.Contains(fmt.Sprintf("%v %+v", err, err), "s3cr3t") {
		t.Fatalf("err = %v", err)
	}
}

type failingDoer struct{}

func (failingDoer) Do(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial failed for %s", r.Header.Get("Authorization"))
}

func TestParseSource(t *testing.T) {
	good := map[string]Source{
		"github:torvalds":            {Kind: GitHub, Host: "github.com", Owner: "torvalds"},
		"github:@starred":            {Kind: GitHub, Host: "github.com", Starred: true},
		"gitlab:gnome/":              {Kind: GitLab, Host: "gitlab.com", Owner: "gnome"},
		"gitlab:gnome/sub":           {Kind: GitLab, Host: "gitlab.com", Owner: "gnome/sub"},
		"codeberg:forgejo":           {Kind: Gitea, Host: "codeberg.org", Owner: "forgejo"},
		"gitea@git.example.com:team": {Kind: Gitea, Host: "git.example.com", Owner: "team"},
		"github@ghe.corp:infra":      {Kind: GitHub, Host: "ghe.corp", Owner: "infra"},
	}
	for spec, want := range good {
		got, err := ParseSource(spec)
		if err != nil || got != want {
			t.Errorf("ParseSource(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
		if again, _ := ParseSource(got.String()); again != got {
			t.Errorf("round trip of %q via %q = %+v", spec, got.String(), again)
		}
	}
	for _, bad := range []string{"torvalds", "github:", "svn:x", "github:a/b", "github:../x", "codeberg@x:y"} {
		if _, err := ParseSource(bad); err == nil {
			t.Errorf("ParseSource(%q) should fail", bad)
		}
	}
}

func TestRegistryKinds(t *testing.T) {
	reg := &Registry{Hosts: map[string]string{"git.corp": GitLab}}
	for host, want := range map[string]string{"github.com": GitHub, "GitLab.com": GitLab, "codeberg.org": Gitea,
		"git.corp": GitLab, "acme.ghe.com": GitHub, "example.org": Generic} {
		if got := reg.KindOf(host); got != want {
			t.Errorf("KindOf(%s) = %s, want %s", host, got, want)
		}
	}
	if reg.For("example.org") != nil {
		t.Error("a generic host has no API client")
	}
}
