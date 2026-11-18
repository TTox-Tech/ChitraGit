package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func size(n int64) *int64 { return &n }

func TestGitHubTreeAndBlob(t *testing.T) {
	c := fake(t, GitHub, "", map[string]http.HandlerFunc{
		"GET /repos/o/r/commits/feature/x": jsonReply(map[string]any{"sha": "c0ffee"}),
		"GET /repos/o/r/git/trees/c0ffee": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("recursive") != "1" {
				t.Errorf("tree not asked recursively: %s", r.URL)
			}
			jsonReply(map[string]any{"truncated": true, "tree": []apiEntry{
				{Path: "a.sh", Mode: "100755", Type: "blob", SHA: "b1", Size: size(12)},
				{Path: "dir", Mode: "040000", Type: "tree", SHA: "t1"},
				{Path: "sub", Mode: "160000", Type: "commit", SHA: "s1"},
			}})(w, r)
		},
		// GitHub wraps base64 at 60 columns.
		"GET /repos/o/r/git/blobs/b1":  jsonReply(map[string]any{"encoding": "base64", "size": 12, "content": b64("#!/bin/sh\n")[:8] + "\n" + b64("#!/bin/sh\n")[8:]}),
		"GET /repos/o/r/git/blobs/big": jsonReply(map[string]any{"encoding": "base64", "size": 5000, "content": ""}),
		"GET /repos/o/r/git/blobs/odd": jsonReply(map[string]any{"encoding": "utf-8", "size": 1, "content": "x"}),
	})
	ctx := context.Background()
	tr, err := c.Tree(ctx, "o", "r", "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	want := Tree{Commit: "c0ffee", Truncated: true, Entries: []TreeEntry{
		{Path: "a.sh", Mode: "100755", Type: "blob", SHA: "b1", Size: 12},
		{Path: "dir", Mode: "040000", Type: "tree", SHA: "t1", Size: -1},
		{Path: "sub", Mode: "160000", Type: "commit", SHA: "s1", Size: -1},
	}}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("tree = %+v\nwant %+v", tr, want)
	}
	data, err := c.Blob(ctx, "o", "r", "b1", 100)
	if err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("blob = %q, %v", data, err)
	}
	if _, err := c.Blob(ctx, "o", "r", "big", 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big blob: %v, want ErrTooLarge", err)
	}
	if _, err := c.Blob(ctx, "o", "r", "odd", 100); err == nil {
		t.Fatal("a blob in an unknown encoding was accepted")
	}
}

func TestGiteaTreePages(t *testing.T) {
	c := fake(t, Gitea, "", map[string]http.HandlerFunc{
		"GET /repos/o/r/git/commits/main": jsonReply(map[string]any{"sha": "abc"}),
		"GET /repos/o/r/git/trees/abc": func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
			jsonReply(map[string]any{"truncated": page == "1", "tree": []apiEntry{
				{Path: "f" + page, Mode: "100644", Type: "blob", SHA: "s" + page, Size: size(3)},
			}})(w, r)
		},
		"GET /repos/o/r/git/blobs/s1": jsonReply(map[string]any{"encoding": "base64", "size": 3, "content": b64("hey")}),
	})
	ctx := context.Background()
	tr, err := c.Tree(ctx, "o", "r", "main")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Commit != "abc" || tr.Truncated || len(tr.Entries) != 2 || tr.Entries[1].Path != "f2" {
		t.Fatalf("tree = %+v", tr)
	}
	if data, err := c.Blob(ctx, "o", "r", "s1", 0); err != nil || string(data) != "hey" {
		t.Fatalf("blob = %q, %v", data, err)
	}
}

func TestGitLabTreeKeyset(t *testing.T) {
	var srvURL string
	c := fake(t, GitLab, "", map[string]http.HandlerFunc{
		"GET /projects/{id}/repository/commits/{ref}": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != "g/sub/r" || r.PathValue("ref") != "main" {
				t.Errorf("commit asked as %s", r.URL)
			}
			jsonReply(map[string]any{"id": "d00d"})(w, r)
		},
		"GET /projects/{id}/repository/tree": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("ref") != "d00d" {
				t.Errorf("tree of %q", r.URL.Query().Get("ref"))
			}
			if r.URL.Query().Get("page_token") == "" {
				srvURL = "http://" + r.Host
				w.Header().Set("Link", fmt.Sprintf(`<%s/projects/g%%2Fsub%%2Fr/repository/tree?ref=d00d&page_token=2>; rel="next"`, srvURL))
				jsonReply([]map[string]string{{"id": "x1", "type": "blob", "path": ".gitmodules", "mode": "100644"}})(w, r)
				return
			}
			jsonReply([]map[string]string{{"id": "x2", "type": "blob", "path": "link", "mode": "120000"}})(w, r)
		},
		"GET /projects/{id}/repository/blobs/{sha}": jsonReply(map[string]any{"encoding": "base64", "size": 2, "content": b64("hi")}),
	})
	ctx := context.Background()
	tr, err := c.Tree(ctx, "g/sub", "r", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []TreeEntry{
		{Path: ".gitmodules", Mode: "100644", Type: "blob", SHA: "x1", Size: -1},
		{Path: "link", Mode: "120000", Type: "blob", SHA: "x2", Size: -1},
	}
	if tr.Commit != "d00d" || tr.Truncated || !reflect.DeepEqual(tr.Entries, want) {
		t.Fatalf("tree = %+v", tr)
	}
	if data, err := c.Blob(ctx, "g/sub", "r", "x1", 0); err != nil || string(data) != "hi" {
		t.Fatalf("blob = %q, %v", data, err)
	}
}
