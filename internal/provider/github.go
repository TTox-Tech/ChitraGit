package provider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// githubAPI serves github.com and GitHub Enterprise Server/Cloud.
type githubAPI struct {
	host, rest, graphql string
	h                   *httpc
}

func newGitHub(host string, h *httpc, base string) *githubAPI {
	g := &githubAPI{host: host, h: h}
	switch {
	case base != "":
		g.rest, g.graphql = base, base+"/graphql"
	case host == "github.com":
		g.rest, g.graphql = "https://api.github.com", "https://api.github.com/graphql"
	case strings.HasSuffix(host, ".ghe.com"):
		g.rest, g.graphql = "https://api."+host, "https://api."+host+"/graphql"
	default: // GitHub Enterprise Server
		g.rest, g.graphql = "https://"+host+"/api/v3", "https://"+host+"/api/graphql"
	}
	return g
}

func (g *githubAPI) Kind() string { return GitHub }
func (g *githubAPI) Host() string { return g.host }

type ghRepo struct {
	FullName      string                 `json:"full_name"`
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	CloneURL      string                 `json:"clone_url"`
	SSHURL        string                 `json:"ssh_url"`
	DefaultBranch string                 `json:"default_branch"`
	PushedAt      time.Time              `json:"pushed_at"`
	Archived      bool                   `json:"archived"`
	Fork          bool                   `json:"fork"`
	Private       bool                   `json:"private"`
	Size          int64                  `json:"size"`
	Language      string                 `json:"language"`
}

func (r ghRepo) repo(host string) Repo {
	return Repo{Host: host, Owner: r.Owner.Login, Name: r.Name, CloneURL: r.CloneURL, SSHURL: r.SSHURL,
		DefaultBranch: r.DefaultBranch, PushedAt: r.PushedAt, Archived: r.Archived, Fork: r.Fork,
		Private: r.Private, SizeKB: r.Size, Language: r.Language}
}

// Lookup asks GET /repos/{owner}/{name}. GitHub answers a renamed or
// transferred repo with a 301 to its new place, which the HTTP client
// follows; the full name in the answer then differs from the one asked.
func (g *githubAPI) Lookup(ctx context.Context, owner, name string) (Lookup, error) {
	r, err := g.h.send(ctx, http.MethodGet, g.rest+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), "Bearer", nil)
	if err != nil {
		return Lookup{}, err
	}
	switch r.status {
	case http.StatusNotFound:
		return Lookup{Verdict: Gone}, nil
	case http.StatusUnavailableForLegalReasons:
		return Lookup{Verdict: Legal}, nil
	case http.StatusForbidden:
		// "Repository access blocked": taken down for breaking the terms.
		if bytes.Contains(bytes.ToLower(r.body), []byte("access blocked")) {
			return Lookup{Verdict: Legal}, nil
		}
		return Lookup{}, statusError(r)
	case http.StatusOK:
	default:
		return Lookup{}, statusError(r)
	}
	var gr ghRepo
	if err := decode(r, &gr); err != nil {
		return Lookup{}, err
	}
	repo := gr.repo(g.host)
	if !strings.EqualFold(gr.FullName, owner+"/"+name) {
		return Lookup{Verdict: Moved, Repo: &repo}, nil
	}
	return Lookup{Verdict: Exists, Repo: &repo}, nil
}

// headsBatch is how many repos one GraphQL query asks about. GitHub allows
// 100 nodes per connection and charges about one point per query.
const headsBatch = 100

// Heads asks GraphQL for default-branch heads, 100 repos per query, so a
// no-change sync of 5,000 repos costs about 50 calls (PRD F2 smart skip).
// GraphQL always needs a token.
func (g *githubAPI) Heads(ctx context.Context, refs []Ref) (map[Ref]Head, error) {
	if g.h.token == "" {
		return nil, ErrUnsupported
	}
	out := map[Ref]Head{}
	for start := 0; start < len(refs); start += headsBatch {
		batch := refs[start:min(start+headsBatch, len(refs))]
		if err := g.headsQuery(ctx, batch, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (g *githubAPI) headsQuery(ctx context.Context, batch []Ref, out map[Ref]Head) error {
	var q strings.Builder
	vars := map[string]any{}
	q.WriteString("query(")
	for i := range batch {
		if i > 0 {
			q.WriteString(", ")
		}
		fmt.Fprintf(&q, "$o%d: String!, $n%d: String!", i, i)
		vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = batch[i].Owner, batch[i].Name
	}
	q.WriteString(") {")
	for i := range batch {
		fmt.Fprintf(&q, " r%d: repository(owner: $o%d, name: $n%d) { pushedAt isArchived defaultBranchRef { name target { oid } } }", i, i, i)
	}
	q.WriteString(" }")

	r, err := g.h.send(ctx, http.MethodPost, g.graphql, "Bearer", map[string]any{"query": q.String(), "variables": vars})
	if err != nil {
		return err
	}
	if r.status != http.StatusOK {
		return statusError(r)
	}
	var resp struct {
		Data map[string]*struct {
			PushedAt         time.Time `json:"pushedAt"`
			IsArchived       bool      `json:"isArchived"`
			DefaultBranchRef *struct {
				Name   string `json:"name"`
				Target struct {
					OID string `json:"oid"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := decode(r, &resp); err != nil {
		return err
	}
	// Missing repos come back as null with a NOT_FOUND error; that is a
	// normal answer (they will be fetched, and rescue detection runs).
	// Anything else with no data at all is a failed query.
	if resp.Data == nil && len(resp.Errors) > 0 {
		e := resp.Errors[0]
		cat := core.CatInternal
		if e.Type == "RATE_LIMITED" {
			cat = core.CatRateLimited
		}
		return &core.Error{Category: cat, Message: "the GitHub GraphQL query failed", Detail: e.Message}
	}
	for i, ref := range batch {
		d := resp.Data[fmt.Sprintf("r%d", i)]
		if d == nil || d.DefaultBranchRef == nil {
			continue
		}
		out[ref] = Head{Branch: d.DefaultBranchRef.Name, SHA: d.DefaultBranchRef.Target.OID, PushedAt: d.PushedAt, Archived: d.IsArchived}
	}
	return nil
}

// List pages through an org's repos (all it lets us see), falling back to
// a user's own repos, or the logged-in user's stars.
func (g *githubAPI) List(ctx context.Context, src Source) ([]Repo, error) {
	var first string
	switch {
	case src.Starred:
		if g.h.token == "" {
			return nil, &core.Error{Category: core.CatAuth, Message: "following your starred repos needs a login",
				Hint: "run `chitragit auth login github`"}
		}
		first = g.rest + "/user/starred?per_page=100"
	default:
		first = g.rest + "/orgs/" + url.PathEscape(src.Owner) + "/repos?per_page=100&type=all"
	}
	repos, err := g.pages(ctx, first)
	if core.CategoryOf(err) == core.CatNotFound && !src.Starred {
		repos, err = g.pages(ctx, g.rest+"/users/"+url.PathEscape(src.Owner)+"/repos?per_page=100&type=owner")
	}
	return repos, err
}

func (g *githubAPI) pages(ctx context.Context, link string) ([]Repo, error) {
	var out []Repo
	for page := 0; link != "" && page < maxPages; page++ {
		var batch []ghRepo
		r, err := g.h.getJSON(ctx, link, "Bearer", &batch)
		if err != nil {
			return out, err
		}
		for _, gr := range batch {
			out = append(out, gr.repo(g.host))
		}
		link = next(r.header)
	}
	return out, nil
}

// User returns the login of the token's owner.
func (g *githubAPI) User(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if _, err := g.h.getJSON(ctx, g.rest+"/user", "Bearer", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}
