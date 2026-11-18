package provider

import (
	"context"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Reading a repo's files through the API, so KodeKavach Shield can judge
// it before a byte of it is cloned (CG-42.16, "L0"). Everything here comes
// from a server Shield does not trust: it only ever adds findings, and the
// clone is still judged from its own objects afterwards (L1).

// maxTreePages bounds a paged tree listing (GitLab: 100 per page, Gitea:
// up to 1,000). A longer tree is reported as Truncated.
const maxTreePages = 200

// apiEntry is a tree entry as GitHub and Gitea send it.
type apiEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size"`
}

func (e apiEntry) entry() TreeEntry {
	te := TreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.SHA, Size: -1}
	if e.Size != nil && e.Type == "blob" {
		te.Size = *e.Size
	}
	return te
}

// apiBlob is a blob as all three APIs send it: base64 content and a size.
type apiBlob struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Size     int64  `json:"size"`
}

func (b apiBlob) data(max int64) ([]byte, error) {
	if max > 0 && b.Size > max {
		return nil, ErrTooLarge
	}
	if b.Encoding != "base64" {
		return nil, &core.Error{Category: core.CatInternal, Message: "the provider API sent a blob in an unknown encoding", Detail: b.Encoding}
	}
	// GitHub wraps base64 at 60 columns.
	data, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(b.Content))
	if err != nil {
		return nil, &core.Error{Category: core.CatInternal, Message: "the provider API sent a blob that is not base64", Err: err}
	}
	if max > 0 && int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	return data, nil
}

// escapeRef escapes each part of a ref such as feature/x, keeping the
// slashes, for APIs that take a ref as part of the path.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// repoPath is "/repos/owner/name" for GitHub and Gitea.
func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// --- GitHub ---

// Tree resolves ref to a commit, then asks for its tree recursively in one
// call. GitHub sends at most 100,000 entries and says when it stopped.
func (g *githubAPI) Tree(ctx context.Context, owner, name, ref string) (Tree, error) {
	var c struct {
		SHA string `json:"sha"`
	}
	if _, err := g.h.getJSON(ctx, g.rest+repoPath(owner, name)+"/commits/"+escapeRef(ref), "Bearer", &c); err != nil {
		return Tree{}, err
	}
	var t struct {
		Tree      []apiEntry `json:"tree"`
		Truncated bool       `json:"truncated"`
	}
	if _, err := g.h.getJSON(ctx, g.rest+repoPath(owner, name)+"/git/trees/"+url.PathEscape(c.SHA)+"?recursive=1", "Bearer", &t); err != nil {
		return Tree{}, err
	}
	out := Tree{Commit: c.SHA, Truncated: t.Truncated}
	for _, e := range t.Tree {
		out.Entries = append(out.Entries, e.entry())
	}
	return out, nil
}

// Blob reads GET /repos/{owner}/{name}/git/blobs/{sha}.
func (g *githubAPI) Blob(ctx context.Context, owner, name, sha string, max int64) ([]byte, error) {
	var b apiBlob
	if _, err := g.h.getJSON(ctx, g.rest+repoPath(owner, name)+"/git/blobs/"+url.PathEscape(sha), "Bearer", &b); err != nil {
		return nil, err
	}
	return b.data(max)
}

// --- Gitea, Forgejo, Codeberg ---

// Tree resolves ref with /git/commits/{ref}, then pages through
// /git/trees/{sha}?recursive=true, where truncated means "more pages".
func (g *giteaAPI) Tree(ctx context.Context, owner, name, ref string) (Tree, error) {
	var c struct {
		SHA string `json:"sha"`
	}
	if _, err := g.h.getJSON(ctx, g.base+repoPath(owner, name)+"/git/commits/"+escapeRef(ref)+"?stat=false&files=false", "token", &c); err != nil {
		return Tree{}, err
	}
	out := Tree{Commit: c.SHA, Truncated: true}
	for page := 1; page <= maxTreePages; page++ {
		var t struct {
			Tree      []apiEntry `json:"tree"`
			Truncated bool       `json:"truncated"`
		}
		link := g.base + repoPath(owner, name) + "/git/trees/" + url.PathEscape(c.SHA) + "?recursive=true&per_page=1000&page=" + strconv.Itoa(page)
		if _, err := g.h.getJSON(ctx, link, "token", &t); err != nil {
			return Tree{}, err
		}
		for _, e := range t.Tree {
			out.Entries = append(out.Entries, e.entry())
		}
		if !t.Truncated || len(t.Tree) == 0 {
			out.Truncated = t.Truncated
			break
		}
	}
	return out, nil
}

// Blob reads GET /repos/{owner}/{name}/git/blobs/{sha}.
func (g *giteaAPI) Blob(ctx context.Context, owner, name, sha string, max int64) ([]byte, error) {
	var b apiBlob
	if _, err := g.h.getJSON(ctx, g.base+repoPath(owner, name)+"/git/blobs/"+url.PathEscape(sha), "token", &b); err != nil {
		return nil, err
	}
	return b.data(max)
}

// --- GitLab ---

func (g *gitlabAPI) project(owner, name string) string {
	return g.base + "/projects/" + url.PathEscape(owner+"/"+name)
}

// Tree resolves ref with /repository/commits/{ref}, then pages through
// /repository/tree with keyset pagination. GitLab sends no sizes.
func (g *gitlabAPI) Tree(ctx context.Context, owner, name, ref string) (Tree, error) {
	var c struct {
		ID string `json:"id"`
	}
	if _, err := g.h.getJSON(ctx, g.project(owner, name)+"/repository/commits/"+url.PathEscape(ref), "Bearer", &c); err != nil {
		return Tree{}, err
	}
	out := Tree{Commit: c.ID}
	link := g.project(owner, name) + "/repository/tree?recursive=true&per_page=100&pagination=keyset&ref=" + url.QueryEscape(c.ID)
	for page := 0; link != ""; page++ {
		if page == maxTreePages {
			out.Truncated = true
			break
		}
		var batch []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Path string `json:"path"`
			Mode string `json:"mode"`
		}
		r, err := g.h.getJSON(ctx, link, "Bearer", &batch)
		if err != nil {
			return Tree{}, err
		}
		for _, e := range batch {
			out.Entries = append(out.Entries, TreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.ID, Size: -1})
		}
		link = next(r.header)
	}
	return out, nil
}

// Blob reads GET /projects/{id}/repository/blobs/{sha}.
func (g *gitlabAPI) Blob(ctx context.Context, owner, name, sha string, max int64) ([]byte, error) {
	var b apiBlob
	if _, err := g.h.getJSON(ctx, g.project(owner, name)+"/repository/blobs/"+url.PathEscape(sha), "Bearer", &b); err != nil {
		return nil, err
	}
	return b.data(max)
}
