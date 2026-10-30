package provider

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// gitlabAPI serves gitlab.com and self-hosted GitLab (API v4).
type gitlabAPI struct {
	host, base string
	h          *httpc
}

func newGitLab(host string, h *httpc, base string) *gitlabAPI {
	if base == "" {
		base = "https://" + host + "/api/v4"
	}
	return &gitlabAPI{host: host, base: base, h: h}
}

func (g *gitlabAPI) Kind() string { return GitLab }
func (g *gitlabAPI) Host() string { return g.host }

type glProject struct {
	Path              string    `json:"path"`
	PathWithNamespace string    `json:"path_with_namespace"`
	HTTPURL           string    `json:"http_url_to_repo"`
	SSHURL            string    `json:"ssh_url_to_repo"`
	DefaultBranch     string    `json:"default_branch"`
	LastActivityAt    time.Time `json:"last_activity_at"`
	Archived          bool      `json:"archived"`
	Visibility        string    `json:"visibility"`
	ForkedFrom        *struct{} `json:"forked_from_project"`
	Namespace         struct {
		FullPath string `json:"full_path"`
	} `json:"namespace"`
}

func (p glProject) repo(host string) Repo {
	owner := p.Namespace.FullPath
	if i := strings.LastIndex(p.PathWithNamespace, "/"); owner == "" && i >= 0 {
		owner = p.PathWithNamespace[:i]
	}
	return Repo{Host: host, Owner: owner, Name: p.Path, CloneURL: p.HTTPURL, SSHURL: p.SSHURL,
		DefaultBranch: p.DefaultBranch, PushedAt: p.LastActivityAt, Archived: p.Archived,
		Fork: p.ForkedFrom != nil, Private: p.Visibility == "private"}
}

// Lookup asks GET /projects/{path}. GitLab keeps redirects for renamed and
// transferred projects and answers the old path with the project at its
// new path.
func (g *gitlabAPI) Lookup(ctx context.Context, owner, name string) (Lookup, error) {
	r, err := g.h.send(ctx, http.MethodGet, g.base+"/projects/"+url.PathEscape(owner+"/"+name), "Bearer", nil)
	if err != nil {
		return Lookup{}, err
	}
	switch r.status {
	case http.StatusNotFound:
		return Lookup{Verdict: Gone}, nil
	case http.StatusUnavailableForLegalReasons:
		return Lookup{Verdict: Legal}, nil
	case http.StatusOK:
	default:
		return Lookup{}, statusError(r)
	}
	var p glProject
	if err := decode(r, &p); err != nil {
		return Lookup{}, err
	}
	repo := p.repo(g.host)
	if !strings.EqualFold(p.PathWithNamespace, owner+"/"+name) {
		return Lookup{Verdict: Moved, Repo: &repo}, nil
	}
	return Lookup{Verdict: Exists, Repo: &repo}, nil
}

// Heads: GitLab has no cheap batch query for branch heads (GraphQL limits
// project lookups by full path), so GitLab repos are always fetched.
func (g *gitlabAPI) Heads(context.Context, []Ref) (map[Ref]Head, error) {
	return nil, ErrUnsupported
}

// List pages through a group's projects including subgroups, falling back
// to a user's projects, or the logged-in user's stars.
func (g *gitlabAPI) List(ctx context.Context, src Source) ([]Repo, error) {
	if src.Starred {
		if g.h.token == "" {
			return nil, &core.Error{Category: core.CatAuth, Message: "following your starred projects needs a login",
				Hint: "run `chitragit auth login gitlab`"}
		}
		return g.pages(ctx, g.base+"/projects?starred=true&per_page=100")
	}
	repos, err := g.pages(ctx, g.base+"/groups/"+url.PathEscape(src.Owner)+"/projects?include_subgroups=true&per_page=100")
	if core.CategoryOf(err) == core.CatNotFound {
		repos, err = g.pages(ctx, g.base+"/users/"+url.PathEscape(src.Owner)+"/projects?per_page=100")
	}
	return repos, err
}

func (g *gitlabAPI) pages(ctx context.Context, link string) ([]Repo, error) {
	var out []Repo
	for page := 0; link != "" && page < maxPages; page++ {
		var batch []glProject
		r, err := g.h.getJSON(ctx, link, "Bearer", &batch)
		if err != nil {
			return out, err
		}
		for _, p := range batch {
			out = append(out, p.repo(g.host))
		}
		link = next(r.header)
	}
	return out, nil
}

// User returns the login of the token's owner.
func (g *gitlabAPI) User(ctx context.Context) (string, error) {
	var u struct {
		Username string `json:"username"`
	}
	if _, err := g.h.getJSON(ctx, g.base+"/user", "Bearer", &u); err != nil {
		return "", err
	}
	return u.Username, nil
}
