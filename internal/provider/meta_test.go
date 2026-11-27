package provider

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// The repo details each forge already sends in its list answers end up in
// Repo.Meta, and GitHub's GraphQL heads carry the same, so keeping them
// offline costs no extra call (CG-32).
func TestListAnswersCarryMeta(t *testing.T) {
	want := core.RepoMeta{Description: "a kernel", Topics: []string{"os", "c"}, Stars: 42, Language: "C",
		License: "GPL-2.0", Homepage: "https://kernel.org"}
	cases := []struct {
		kind, route string
		body        map[string]any
		want        core.RepoMeta
	}{
		{GitHub, "GET /orgs/o/repos", map[string]any{"name": "r", "owner": map[string]string{"login": "o"},
			"description": "a kernel", "topics": []string{"os", "c"}, "stargazers_count": 42, "language": "C",
			"license": map[string]string{"spdx_id": "GPL-2.0"}, "homepage": "https://kernel.org"}, want},
		{GitHub, "GET /orgs/o/repos", map[string]any{"name": "r", "owner": map[string]string{"login": "o"},
			"license": map[string]string{"spdx_id": "NOASSERTION"}}, core.RepoMeta{}},
		{Gitea, "GET /orgs/o/repos", map[string]any{"name": "r", "owner": map[string]string{"login": "o"},
			"description": "a kernel", "topics": []string{"os", "c"}, "stars_count": 42, "language": "C",
			"licenses": []string{"GPL-2.0"}, "website": "https://kernel.org"}, want},
		{GitLab, "GET /groups/o/projects", map[string]any{"path": "r", "path_with_namespace": "o/r",
			"description": "a kernel", "topics": []string{"os", "c"}, "star_count": 42},
			core.RepoMeta{Description: "a kernel", Topics: []string{"os", "c"}, Stars: 42}},
	}
	for _, c := range cases {
		client := fake(t, c.kind, "", map[string]http.HandlerFunc{c.route: jsonReply([]map[string]any{c.body})})
		repos, err := client.List(context.Background(), Source{Kind: c.kind, Owner: "o"})
		if err != nil || len(repos) != 1 {
			t.Fatalf("%s: %+v %v", c.kind, repos, err)
		}
		if got := repos[0].Meta(); !sameMeta(got, c.want) {
			t.Errorf("%s: meta = %+v, want %+v", c.kind, got, c.want)
		}
	}
}

func TestGitHubHeadsCarryMeta(t *testing.T) {
	c := fake(t, GitHub, "tok", map[string]http.HandlerFunc{
		"POST /graphql": jsonReply(map[string]any{"data": map[string]any{"r0": map[string]any{
			"pushedAt": "2026-10-01T10:00:00Z", "defaultBranchRef": map[string]any{"name": "main", "target": map[string]string{"oid": "abc"}},
			"description": "a kernel", "stargazerCount": 42, "homepageUrl": "https://kernel.org", "isFork": true,
			"primaryLanguage": map[string]string{"name": "C"}, "licenseInfo": map[string]string{"spdxId": "GPL-2.0"},
			"repositoryTopics": map[string]any{"nodes": []map[string]any{{"topic": map[string]string{"name": "os"}}}},
		}}}),
	})
	heads, err := c.Heads(context.Background(), []Ref{{"o", "r"}})
	if err != nil {
		t.Fatal(err)
	}
	want := core.RepoMeta{Description: "a kernel", Topics: []string{"os"}, Stars: 42, Language: "C", License: "GPL-2.0",
		Homepage: "https://kernel.org", Fork: true}
	if got := heads[Ref{"o", "r"}].Meta; !sameMeta(got, want) {
		t.Errorf("meta = %+v, want %+v", got, want)
	}
}

func sameMeta(a, b core.RepoMeta) bool {
	return a.Description == b.Description && slices.Equal(a.Topics, b.Topics) && a.Stars == b.Stars &&
		a.Language == b.Language && a.License == b.License && a.Homepage == b.Homepage && a.Fork == b.Fork
}
