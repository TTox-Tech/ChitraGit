package provider

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"
)

// Kinds of hosting service. Generic covers any plain Git URL: it syncs, but
// has no API, so no smart skip, rescue diagnosis or follow.
const (
	GitHub  = "github"
	GitLab  = "gitlab"
	Gitea   = "gitea" // also Forgejo and Codeberg
	Generic = "generic"
)

// Repo is what a provider API says about one repository.
type Repo struct {
	Host          string    `json:"host"`
	Owner         string    `json:"owner"` // user, org or GitLab group path
	Name          string    `json:"name"`
	CloneURL      string    `json:"clone_url"` // https
	SSHURL        string    `json:"ssh_url,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	PushedAt      time.Time `json:"pushed_at"`
	Archived      bool      `json:"archived,omitempty"`
	Fork          bool      `json:"fork,omitempty"`
	Private       bool      `json:"private,omitempty"`
	SizeKB        int64     `json:"size_kb,omitempty"`
	Language      string    `json:"language,omitempty"`
}

// FullName is "owner/name".
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

// Verdict is the API's answer to "why can't we fetch this repo?" (PRD F5).
type Verdict string

const (
	Exists Verdict = "exists" // the repo is there; the failure was something else
	Gone   Verdict = "gone"   // 404: deleted, or made private for us
	Legal  Verdict = "legal"  // 451: DMCA or other legal takedown
	Moved  Verdict = "moved"  // renamed or transferred; Lookup.Repo has the new place
)

// Lookup is the result of asking the API about one repo.
type Lookup struct {
	Verdict Verdict
	Repo    *Repo // set for Exists and Moved
}

// Ref names a repo on a host.
type Ref struct{ Owner, Name string }

// Head is the state of a repo's default branch, for smart skip.
type Head struct {
	Branch   string
	SHA      string
	PushedAt time.Time
	Archived bool
}

// Source is a followed account or group (PRD F6 "follow").
type Source struct {
	Kind    string // github | gitlab | gitea
	Host    string
	Owner   string // user, org or group; empty with Starred
	Starred bool   // the logged-in user's starred repos
}

// Client talks to one host's API.
type Client interface {
	Kind() string
	Host() string
	// Lookup asks about one repo. It returns an error only when the API
	// could not answer (offline, rate limited, bad token).
	Lookup(ctx context.Context, owner, name string) (Lookup, error)
	// Heads returns default-branch heads for as many refs as the API can
	// answer cheaply. Refs it cannot answer are left out; ErrUnsupported
	// means this provider has no batch query at all.
	Heads(ctx context.Context, refs []Ref) (map[Ref]Head, error)
	// List returns every repo of a followed source.
	List(ctx context.Context, src Source) ([]Repo, error)
	// User returns the login the token belongs to; it checks a token.
	User(ctx context.Context) (string, error)
}

// ErrUnsupported means the provider or host has no API for the request.
var ErrUnsupported = errors.New("not supported by this provider")

// Registry builds API clients for hosts. Tokens are looked up per host
// and never stored by the registry.
type Registry struct {
	// Hosts maps self-hosted servers to their kind, e.g.
	// "git.example.com" = "gitea". Well-known hosts need no entry.
	Hosts map[string]string
	// Token returns the API token for a host, or "".
	Token func(host string) string
	// HTTP is the client to use; nil = a default with timeouts.
	HTTP HTTPDoer
	// BaseURL overrides the API root for every host (tests only).
	BaseURL string
}

// KindOf returns the provider kind for a host.
func (g *Registry) KindOf(host string) string {
	host = strings.ToLower(host)
	if k, ok := g.Hosts[host]; ok {
		return k
	}
	if k, ok := knownHosts[host]; ok {
		return k
	}
	// GitHub Enterprise Cloud with data residency uses *.ghe.com.
	if strings.HasSuffix(host, ".ghe.com") {
		return GitHub
	}
	return Generic
}

// For returns a client for host, or nil for a generic host.
func (g *Registry) For(host string) Client {
	host = strings.ToLower(host)
	var token string
	if g.Token != nil {
		token = g.Token(host)
	}
	h := newHTTP(g.HTTP, token)
	switch g.KindOf(host) {
	case GitHub:
		return newGitHub(host, h, g.BaseURL)
	case GitLab:
		return newGitLab(host, h, g.BaseURL)
	case Gitea:
		return newGitea(host, h, g.BaseURL)
	}
	return nil
}

// ParseSource understands follow specs (PRD F6):
//
//	github:torvalds            all repos of a user or org
//	github:@starred            my starred repos
//	gitlab:gnome/              a GitLab group, with subgroups
//	codeberg:forgejo           Codeberg is a Gitea/Forgejo host
//	gitea@git.example.com:team a self-hosted server
func ParseSource(spec string) (Source, error) {
	spec = strings.TrimSpace(spec)
	prefix, owner, ok := strings.Cut(spec, ":")
	if !ok || owner == "" {
		return Source{}, errors.New("expected provider:owner, e.g. github:torvalds or github:@starred")
	}
	kind, host, hasHost := strings.Cut(strings.ToLower(prefix), "@")
	switch kind {
	case GitHub, GitLab, Gitea:
	case "codeberg":
		kind, host = Gitea, "codeberg.org"
		if hasHost {
			return Source{}, errors.New("codeberg already names its host; use gitea@host for other servers")
		}
	case "forgejo":
		kind = Gitea
	default:
		return Source{}, errors.New("unknown provider " + prefix + "; use github, gitlab, gitea or codeberg")
	}
	if host == "" {
		host = map[string]string{GitHub: "github.com", GitLab: "gitlab.com", Gitea: "gitea.com"}[kind]
	}
	src := Source{Kind: kind, Host: host}
	owner = strings.Trim(owner, "/")
	if owner == "@starred" {
		src.Starred = true
		return src, nil
	}
	if owner == "" || strings.ContainsAny(owner, " \t?#") || strings.Contains(owner, "..") || path.Clean(owner) != owner {
		return Source{}, errors.New("invalid owner " + owner)
	}
	if kind != GitLab && strings.Contains(owner, "/") {
		return Source{}, errors.New("only GitLab groups can be nested; use github:owner")
	}
	src.Owner = owner
	return src, nil
}

// String is the canonical spec of a source.
func (s Source) String() string {
	prefix := s.Kind
	def := map[string]string{GitHub: "github.com", GitLab: "gitlab.com", Gitea: "gitea.com"}[s.Kind]
	switch {
	case s.Kind == Gitea && s.Host == "codeberg.org":
		prefix = "codeberg"
	case s.Host != def:
		prefix += "@" + s.Host
	}
	if s.Starred {
		return prefix + ":@starred"
	}
	return prefix + ":" + s.Owner
}
