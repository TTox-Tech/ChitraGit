package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

func TestConditionalRequests(t *testing.T) {
	var full, notModified atomic.Int32
	body := `{"full_name":"o/ok","name":"ok","owner":{"login":"o"},"default_branch":"main"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	dir := filepath.Join(t.TempDir(), "etags")
	client := func(token string) Client {
		reg := &Registry{Hosts: map[string]string{"example.test": GitHub}, BaseURL: srv.URL,
			Token: func(string) string { return token }, ETags: DirETags(dir)}
		return reg.For("example.test")
	}

	ctx := context.Background()
	for range 3 {
		got, err := client("t1").Lookup(ctx, "o", "ok")
		if err != nil || got.Verdict != Exists || got.Repo.DefaultBranch != "main" {
			t.Fatalf("Lookup = %+v, %v", got, err)
		}
	}
	if full.Load() != 1 || notModified.Load() != 2 {
		t.Errorf("full answers %d, not modified %d; want 1 and 2", full.Load(), notModified.Load())
	}
	// Another token does not see the first token's answers.
	if _, err := client("t2").Lookup(ctx, "o", "ok"); err != nil || full.Load() != 2 {
		t.Errorf("other token: %v, full answers %d", err, full.Load())
	}

	// The saved answers are readable only by the user, and a damaged one
	// is fetched again.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("saved answers = %v %v", entries, err)
	}
	for _, e := range entries {
		info, _ := e.Info()
		if perm := info.Mode().Perm(); perm&0o077 != 0 && runtime.GOOS != "windows" {
			t.Errorf("%s has mode %v", e.Name(), perm)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client("t1").Lookup(ctx, "o", "ok"); err != nil || full.Load() != 3 {
		t.Errorf("after damage: %v, full answers %d", err, full.Load())
	}
}
