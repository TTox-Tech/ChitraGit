package syncer

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// flakyServer serves the fixture's upstream over smart HTTP and cuts the
// connection of the next cuts requests: half of them before any reply,
// the other half in the middle of one.
type flakyServer struct {
	*httptest.Server
	cuts atomic.Int64
	seen atomic.Int64
}

func newFlakyServer(t *testing.T, f *fixture) *flakyServer {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(f.upstream), "GIT_HTTP_EXPORT_ALL=1"}}
	s := &flakyServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.seen.Add(1)
		if s.cuts.Add(-1) < 0 {
			s.cuts.Store(0)
			backend.ServeHTTP(w, r)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if n%2 == 0 { // cut mid-reply
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/x-git-upload-pack-result\r\nContent-Length: 4096\r\n\r\n0008NAK\n")
			_ = buf.Flush()
		}
		_ = conn.Close()
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *flakyServer) url(f *fixture) string { return s.URL + "/" + filepath.Base(f.upstream) }

// A connection that drops a few times is retried until the sync gets
// through; one that keeps dropping fails the repo as a network error and
// changes nothing, and the next good sync brings everything in.
func TestNetworkDrops(t *testing.T) {
	f := newFixture(t)
	srv := newFlakyServer(t, f)
	local := f.clone(t)
	git(t, local, "remote", "set-url", "origin", srv.url(f))
	f.commit("b.txt", "two")
	f.push("main")
	want := git(t, f.dev, "rev-parse", "HEAD")

	srv.cuts.Store(2)
	r := syncDir(t, local, Options{Mode: "ff", Retries: 3})
	if r.Outcome != Updated || git(t, local, "rev-parse", "HEAD") != want {
		t.Fatalf("sync through 2 dropped connections = %s %s: %s", r.Outcome, r.Category, detail(r.Err))
	}

	f.commit("c.txt", "three")
	f.push("main")
	before := git(t, local, "rev-parse", "refs/remotes/origin/main")
	srv.cuts.Store(1 << 20)
	r = syncDir(t, local, Options{Mode: "ff", Retries: 2})
	if r.Outcome != Failed || r.Category != string(core.CatNetwork) {
		t.Fatalf("sync over a dead connection = %s %s: %s", r.Outcome, r.Category, detail(r.Err))
	}
	if got := git(t, local, "rev-parse", "refs/remotes/origin/main"); got != before {
		t.Fatalf("a failed fetch moved origin/main to %s", got)
	}
	git(t, local, "fsck", "--no-progress")

	srv.cuts.Store(0)
	if r = syncDir(t, local, Options{Mode: "ff"}); r.Outcome != Updated ||
		git(t, local, "rev-parse", "HEAD") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Fatalf("sync after the network came back = %s: %s", r.Outcome, detail(r.Err))
	}
	if !strings.Contains(git(t, local, "log", "--format=%s"), "c.txt three") {
		t.Fatal("commit missing after the network came back")
	}
}

// detail is git's stderr behind a sync error, for test failures.
func detail(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message + ": " + ce.Detail
	}
	return fmt.Sprint(err)
}
