package provider

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// giteaAPI serves Gitea, Forgejo and Codeberg (API v1).
type giteaAPI struct {
	host, base string
	h          *httpc
}

func newGitea(host string, h *httpc, base string) *giteaAPI {
	if base == "" {
		base = "https://" + host + "/api/v1"
	}
	return &giteaAPI{host: host, base: base, h: h}
}

func (g *giteaAPI) Kind() string { return Gitea }
func (g *giteaAPI) Host() string { return g.host }

type giteaRepo struct {
	FullName      string                 `json:"full_name"`
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	CloneURL      string                 `json:"clone_url"`
	SSHURL        string                 `json:"ssh_url"`
	DefaultBranch string                 `json:"default_branch"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Archived      bool                   `json:"archived"`
	Fork          bool                   `json:"fork"`
	Private       bool                   `json:"private"`
	Size          int64                  `json:"size"`
	Language      string                 `json:"language"`
}

func (r giteaRepo) repo(host string) Repo {
	return Repo{Host: host, Owner: r.Owner.Login, Name: r.Name, CloneURL: r.CloneURL, SSHURL: r.SSHURL,
		DefaultBranch: r.DefaultBranch, PushedAt: r.UpdatedAt, Archived: r.Archived, Fork: r.Fork,
		Private: r.Private, SizeKB: r.Size, Language: r.Language}
}

// Lookup asks GET /repos/{owner}/{name}. Gitea redirects renamed and
// transferred repos to their new place.
func (g *giteaAPI) Lookup(ctx context.Context, owner, name string) (Lookup, error) {
	r, err := g.h.send(ctx, http.MethodGet, g.base+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), "token", nil)
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
	var gr giteaRepo
	if err := decode(r, &gr); err != nil {
		return Lookup{}, err
	}
	repo := gr.repo(g.host)
	if !strings.EqualFold(gr.FullName, owner+"/"+name) {
		return Lookup{Verdict: Moved, Repo: &repo}, nil
	}
	return Lookup{Verdict: Exists, Repo: &repo}, nil
}

// Heads: Gitea has no batch query, so Gitea repos are always fetched.
func (g *giteaAPI) Heads(context.Context, []Ref) (map[Ref]Head, error) {
	return nil, ErrUnsupported
}

// giteaPage is the page size; Gitea's default maximum is 50.
const giteaPage = 50

// List pages through an org's repos, falling back to a user's repos, or
// the logged-in user's stars.
func (g *giteaAPI) List(ctx context.Context, src Source) ([]Repo, error) {
	if src.Starred {
		if g.h.token == "" {
			return nil, &core.Error{Category: core.CatAuth, Message: "following your starred repos needs a login",
				Hint: "run `chitragit auth login " + g.host + "`"}
		}
		return g.pages(ctx, g.base+"/user/starred")
	}
	repos, err := g.pages(ctx, g.base+"/orgs/"+url.PathEscape(src.Owner)+"/repos")
	if core.CategoryOf(err) == core.CatNotFound {
		repos, err = g.pages(ctx, g.base+"/users/"+url.PathEscape(src.Owner)+"/repos")
	}
	return repos, err
}

// pages walks ?page=N. Older Gitea versions send no Link header, so a full
// page also means "there may be more".
func (g *giteaAPI) pages(ctx context.Context, first string) ([]Repo, error) {
	var out []Repo
	for page := 1; page <= maxPages; page++ {
		var batch []giteaRepo
		if _, err := g.h.getJSON(ctx, first+"?limit="+strconv.Itoa(giteaPage)+"&page="+strconv.Itoa(page), "token", &batch); err != nil {
			return out, err
		}
		for _, gr := range batch {
			out = append(out, gr.repo(g.host))
		}
		if len(batch) < giteaPage {
			break
		}
	}
	return out, nil
}

// User returns the login of the token's owner.
func (g *giteaAPI) User(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if _, err := g.h.getJSON(ctx, g.base+"/user", "token", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}
