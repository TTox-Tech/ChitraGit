// Package provider knows about Git hosting services: it parses remote URLs
// and talks to the GitHub, GitLab and Gitea/Forgejo APIs (PRD F6). Plain Git
// URLs are "generic": they sync, but have no API.
package provider

import (
	"net/url"
	"strings"
)

// Remote is what a remote URL tells us about where a repo lives.
type Remote struct {
	Provider string // github | gitlab | gitea | generic
	Host     string // lower-case host name, without port; empty for local paths
	Owner    string // user, org or (GitLab) group path
	Name     string
}

var knownHosts = map[string]string{
	"github.com":   "github",
	"gitlab.com":   "gitlab",
	"codeberg.org": "gitea",
	"gitea.com":    "gitea",
}

// Parse understands the three URL shapes Git accepts:
//
//	https://host/owner/name.git     (also http://, git://, ssh://user@host:port/...)
//	git@host:owner/name.git         (scp-like SSH syntax)
//	/some/local/path                (no host)
func Parse(raw string) Remote {
	raw = strings.TrimSpace(raw)
	var host, p string
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		host, p = u.Hostname(), u.Path
	} else if at := strings.Index(raw, ":"); at > 0 && !strings.ContainsAny(raw[:at], `/\`) && len(raw[:at]) > 1 {
		// scp-like: [user@]host:path. A one-letter prefix is a Windows drive ("C:\...").
		host, p = raw[:at], raw[at+1:]
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
	} else {
		return Remote{Provider: "generic"}
	}

	host = strings.ToLower(host)
	r := Remote{Provider: "generic", Host: host}
	if prov, ok := knownHosts[host]; ok {
		r.Provider = prov
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		r.Owner, r.Name = p[:i], p[i+1:]
	} else {
		r.Name = p
	}
	return r
}
