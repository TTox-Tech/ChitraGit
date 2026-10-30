package conflict

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// git runs plain git (no hardening) for test setup.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t",
		"-c", "init.defaultBranch=main", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output() // stdout only: warnings must not change snapshots
	if err != nil {
		t.Fatalf("git %v: %v\n%s%s", args, err, out, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// repo is an upstream, a "dev" clone that pushes to it, and the archived
// clone under test ("local"), already fetched and behind with local
// changes that clash.
type repo struct {
	t              *testing.T
	up, dev, local string
	git            *gitx.Runner
	ctx            context.Context
	before         state
	upstreamHead   string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := &repo{t: t, up: filepath.Join(t.TempDir(), "up.git"), dev: filepath.Join(t.TempDir(), "dev"),
		local: filepath.Join(t.TempDir(), "local"), ctx: context.Background()}
	git(t, ".", "init", "-q", "--bare", r.up)
	git(t, ".", "clone", "-q", r.up, r.dev)
	write(t, r.dev, "a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	write(t, r.dev, "gone.txt", "will be deleted upstream\n")
	write(t, r.dev, "keep.txt", "unchanged\n")
	git(t, r.dev, "add", ".")
	git(t, r.dev, "commit", "-q", "-m", "init")
	git(t, r.dev, "push", "-q", "origin", "main")
	git(t, ".", "clone", "-q", r.up, r.local)
	// Exact byte comparisons below; Windows runners default to
	// core.autocrlf=true, which rewrites line endings on checkout.
	git(t, r.local, "config", "core.autocrlf", "false")
	var err error
	if r.git, err = gitx.New(gitx.Options{HooksDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return r
}

// upstream commits files in dev, pushes, and fetches into local.
func (r *repo) upstream(files map[string]string) {
	for name, content := range files {
		if content == "" {
			git(r.t, r.dev, "rm", "-q", name)
			continue
		}
		write(r.t, r.dev, name, content)
	}
	git(r.t, r.dev, "add", "-A")
	git(r.t, r.dev, "commit", "-q", "-m", "upstream")
	git(r.t, r.dev, "push", "-q", "origin", "main")
	git(r.t, r.local, "fetch", "-q")
	r.upstreamHead = git(r.t, r.dev, "rev-parse", "HEAD")
}

// typical sets up the usual clash: you and upstream both edit a.txt (two
// hunks), you add notes.txt that upstream adds too, upstream deletes a file
// you edited, and you have an unrelated untracked and an ignored file.
func (r *repo) typical() {
	t := r.t
	write(t, r.local, "a.txt", "1 mine\n2\n3\n4\n5\n6\n7\n8\n9 mine\n")
	write(t, r.local, "gone.txt", "I still need this\n")
	write(t, r.local, "notes.txt", "my notes\n")
	write(t, r.local, "scratch.txt", "untracked, no clash\n")
	write(t, r.local, ".git/info/exclude", "*.log\n")
	write(t, r.local, "debug.log", "ignored, never touched\n")
	r.before = snapshot(t, r.local)
	r.upstream(map[string]string{"a.txt": "1 theirs\n2\n3\n4\n5\n6\n7\n8\n9 theirs\n", "gone.txt": "",
		"notes.txt": "their notes\n"})
}

// state is everything a user could notice about a working copy.
type state struct {
	Head, Status, Diff, Stash string
	Files                     map[string]string
}

func snapshot(t *testing.T, dir string) state {
	t.Helper()
	st := state{
		Head:   git(t, dir, "rev-parse", "HEAD"),
		Status: git(t, dir, "status", "--porcelain", "--ignored"),
		Diff:   git(t, dir, "diff", "HEAD"),
		Stash:  git(t, dir, "stash", "list"),
		Files:  map[string]string{},
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			st.Files[e.Name()] = read(t, dir, e.Name())
		}
	}
	return st
}

func (r *repo) start() *Session {
	r.t.Helper()
	s, err := Start(r.ctx, r.git, r.local)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func kinds(t *testing.T, s *Session) map[string]File {
	t.Helper()
	files, err := s.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]File{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

func TestStartLaysOutEveryKindOfClash(t *testing.T) {
	r := newRepo(t)
	r.typical()
	s := r.start()
	if s.Done {
		t.Fatal("finished although files clash")
	}
	files := kinds(t, s)
	want := map[string]File{
		"a.txt":     {Path: "a.txt", Kind: KindContent, Hunks: 2},
		"gone.txt":  {Path: "gone.txt", Kind: KindDeletedTheirs},
		"notes.txt": {Path: "notes.txt", Kind: KindAddedBoth, Hunks: 1},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %+v", files)
	}
	if git(t, r.local, "rev-parse", "HEAD") != r.upstreamHead {
		t.Error("HEAD is not at the update")
	}
	if read(t, r.local, "scratch.txt") != "untracked, no clash\n" || read(t, r.local, "debug.log") != "ignored, never touched\n" {
		t.Error("an unrelated file was not put back")
	}
	hs, err := s.Hunks(r.ctx, "notes.txt")
	if err != nil || len(hs) != 1 || hs[0].Mine != "my notes\n" || hs[0].Theirs != "their notes\n" {
		t.Errorf("notes.txt hunks = %+v %v", hs, err)
	}

	// A sync leaves an open session alone.
	res := syncer.Run(r.ctx, r.git, []syncer.Job{{Repo: core.Repo{Path: "r", Kind: core.KindNormal}, Dir: r.local}},
		syncer.Options{StashLocalChanges: true, RetryDelay: time.Millisecond}, nil)[0]
	if res.Status != syncer.StatusResolving {
		t.Errorf("sync status = %s: %s", res.Status, res.Reason)
	}
	if again, err := Open(r.ctx, r.git, r.local); err != nil || again == nil {
		t.Errorf("session gone after sync: %v", err)
	}
}

func TestResolveAndFinishKeepsChangesUncommitted(t *testing.T) {
	r := newRepo(t)
	r.typical()
	s := r.start()
	ctx := r.ctx

	if err := s.Finish(ctx); err == nil {
		t.Fatal("finished with files unresolved")
	}
	if err := s.ResolveHunk(ctx, "a.txt", 1, Mine); err != nil {
		t.Fatal(err)
	}
	if f := kinds(t, s)["a.txt"]; f.Resolved || f.Hunks != 1 {
		t.Errorf("after one hunk: %+v", f)
	}
	if err := s.ResolveHunk(ctx, "a.txt", 0, Theirs); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFile(ctx, "notes.txt", Both); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFile(ctx, "gone.txt", Mine); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFile(ctx, "gone.txt", Mine); err == nil {
		t.Error("resolved a file twice")
	}
	stash := s.flow.StashSHA
	if err := s.Finish(ctx); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"a.txt":       "1 theirs\n2\n3\n4\n5\n6\n7\n8\n9 mine\n",
		"notes.txt":   "their notes\nmy notes\n",
		"gone.txt":    "I still need this\n",
		"scratch.txt": "untracked, no clash\n",
		"debug.log":   "ignored, never touched\n",
		"keep.txt":    "unchanged\n",
	} {
		if got := read(t, r.local, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if git(t, r.local, "rev-parse", "HEAD") != r.upstreamHead {
		t.Error("HEAD is not at the update")
	}
	if staged := git(t, r.local, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("changes left staged: %s", staged)
	}
	if git(t, r.local, "stash", "list") != "" || git(t, r.local, "for-each-ref", "refs/chitragit/backup") != "" {
		t.Error("stash or backup ref left behind")
	}
	if kept := git(t, r.local, "for-each-ref", "--format=%(objectname)", resolvedPrefix); kept != stash {
		t.Errorf("original changes not kept: %q, want %s", kept, stash)
	}
	if s, err := Open(ctx, r.git, r.local); s != nil || err != nil {
		t.Error("session still open")
	}
}

func TestAbortRestoresExactlyAndKeepsHandEdits(t *testing.T) {
	r := newRepo(t)
	r.typical()
	s := r.start()
	write(t, r.local, "a.txt", "half-done hand edit\n")
	write(t, r.local, "new-during-session.txt", "made while resolving\n")

	if err := s.Abort(r.ctx); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, r.local); !reflect.DeepEqual(r.before, after) {
		t.Errorf("not restored:\nbefore %+v\nafter  %+v", r.before, after)
	}
	saved := git(t, r.local, "for-each-ref", "--format=%(refname)", abortedPrefix)
	if saved == "" {
		t.Fatal("hand edits were not saved")
	}
	if got := git(t, r.local, "show", saved+":a.txt"); got != "half-done hand edit" {
		t.Errorf("saved a.txt = %q", got)
	}
	if got := git(t, r.local, "show", saved+":new-during-session.txt"); got != "made while resolving" {
		t.Errorf("saved new file = %q", got)
	}
	// The pending update is still there to try again.
	if git(t, r.local, "rev-parse", "origin/main") != r.upstreamHead {
		t.Error("the downloaded update was lost")
	}
}

func TestBinaryAndDeletedClashes(t *testing.T) {
	r := newRepo(t)
	write(t, r.dev, "img.bin", "base\x00image")
	r.upstream(map[string]string{"img.bin": "base\x00image"})
	git(t, r.local, "merge", "-q", "--ff-only", "origin/main")

	write(t, r.local, "img.bin", "mine\x00image")
	write(t, r.local, "drop.bin", "my new\x00binary")
	git(t, r.local, "rm", "-q", "keep.txt")
	r.upstream(map[string]string{"img.bin": "theirs\x00image", "drop.bin": "their new\x00binary", "keep.txt": "changed upstream\n"})

	s := r.start()
	files := kinds(t, s)
	for p, want := range map[string]string{"img.bin": KindBinary, "drop.bin": KindBinary, "keep.txt": KindDeletedMine} {
		if files[p].Kind != want {
			t.Errorf("%s kind = %q, want %q", p, files[p].Kind, want)
		}
	}
	if err := s.ResolveFile(r.ctx, "img.bin", Both); err == nil {
		t.Error("kept both sides of a binary file")
	}
	for p, c := range map[string]Choice{"img.bin": Mine, "drop.bin": Mine, "keep.txt": Mine} {
		if err := s.ResolveFile(r.ctx, p, c); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if err := s.Finish(r.ctx); err != nil {
		t.Fatal(err)
	}
	if read(t, r.local, "img.bin") != "mine\x00image" || read(t, r.local, "drop.bin") != "my new\x00binary" ||
		read(t, r.local, "keep.txt") != "<missing>" {
		t.Errorf("wrong result: %q %q %q", read(t, r.local, "img.bin"), read(t, r.local, "drop.bin"), read(t, r.local, "keep.txt"))
	}
	if st := git(t, r.local, "status", "--porcelain"); !strings.Contains(st, " D keep.txt") {
		t.Errorf("deletion not kept as an uncommitted change:\n%s", st)
	}
}

func TestMarkResolvedNeedsMarkersGone(t *testing.T) {
	r := newRepo(t)
	r.typical()
	s := r.start()
	if err := s.MarkResolved(r.ctx, "a.txt"); err == nil {
		t.Fatal("marked a file with markers as resolved")
	}
	write(t, r.local, "a.txt", "fixed by hand\n")
	if err := s.MarkResolved(r.ctx, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveHunk(r.ctx, "../../etc/passwd", 0, Mine); err == nil {
		t.Error("accepted a path outside the session")
	}
}

func TestStartWithoutClashFinishesAtOnce(t *testing.T) {
	r := newRepo(t)
	write(t, r.local, "keep.txt", "my edit\n")
	r.upstream(map[string]string{"a.txt": "new\n"})
	s := r.start()
	if !s.Done || read(t, r.local, "keep.txt") != "my edit\n" || read(t, r.local, "a.txt") != "new\n" {
		t.Errorf("done=%v keep=%q a=%q", s.Done, read(t, r.local, "keep.txt"), read(t, r.local, "a.txt"))
	}
	if _, err := Start(r.ctx, r.git, r.local); !errors.Is(err, ErrNothingToResolve) {
		t.Errorf("second start: %v", err)
	}
}

// crash simulates the process dying at the named step.
type crash string

func runUntil(t *testing.T, at string, fn func()) (crashed bool) {
	t.Helper()
	restore := syncer.SetStepHook(func(name string) {
		if name == at {
			panic(crash(name))
		}
	})
	defer restore()
	defer func() {
		if v := recover(); v != nil {
			if _, ok := v.(crash); !ok {
				panic(v)
			}
			crashed = true
		}
	}()
	fn()
	return false
}

func TestCrashDuringStartIsRolledBackBySync(t *testing.T) {
	for _, at := range []string{"flow.marked", "flow.stashed", "start.updated", "start.applied"} {
		t.Run(at, func(t *testing.T) {
			r := newRepo(t)
			r.typical()
			if !runUntil(t, at, func() { _, _ = Start(r.ctx, r.git, r.local) }) {
				t.Fatal("step never reached")
			}
			res := syncer.Run(r.ctx, r.git, []syncer.Job{{Repo: core.Repo{Path: "r", Kind: core.KindNormal}, Dir: r.local}},
				syncer.Options{Mode: "fetch", RetryDelay: time.Millisecond}, nil)[0]
			if res.Outcome == syncer.Failed {
				t.Fatal(res.Err)
			}
			if after := snapshot(t, r.local); !reflect.DeepEqual(r.before, after) {
				t.Errorf("not restored:\nbefore %+v\nafter  %+v", r.before, after)
			}
		})
	}
}

func TestCrashDuringFinishOrAbortLosesNothing(t *testing.T) {
	resolve := func(t *testing.T, r *repo, s *Session) {
		for _, p := range []string{"a.txt", "notes.txt", "gone.txt"} {
			if err := s.ResolveFile(r.ctx, p, Mine); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, at := range []string{"finish.unstaged", "flow.kept", "flow.unmarked"} {
		t.Run("finish/"+at, func(t *testing.T) {
			r := newRepo(t)
			r.typical()
			s := r.start()
			resolve(t, r, s)
			if !runUntil(t, at, func() { _ = s.Finish(r.ctx) }) {
				t.Fatal("step never reached")
			}
			// Whatever survived, finishing again (or nothing) ends in the
			// resolved state with your original changes still reachable.
			if s, err := Open(r.ctx, r.git, r.local); err != nil {
				t.Fatal(err)
			} else if s != nil {
				if err := s.Finish(r.ctx); err != nil {
					t.Fatal(err)
				}
			}
			for name, want := range map[string]string{"a.txt": "1 mine\n2\n3\n4\n5\n6\n7\n8\n9 mine\n", "notes.txt": "my notes\n",
				"gone.txt": "I still need this\n", "scratch.txt": "untracked, no clash\n"} {
				if got := read(t, r.local, name); got != want {
					t.Errorf("%s = %q", name, got)
				}
			}
			if git(t, r.local, "for-each-ref", resolvedPrefix) == "" && git(t, r.local, "stash", "list") == "" {
				t.Error("original changes no longer reachable")
			}
		})
	}
	t.Run("abort/abort.saved", func(t *testing.T) {
		r := newRepo(t)
		r.typical()
		s := r.start()
		if !runUntil(t, "abort.saved", func() { _ = s.Abort(r.ctx) }) {
			t.Fatal("step never reached")
		}
		if ok, err := Abort(r.ctx, r.git, r.local); !ok || err != nil {
			t.Fatalf("second abort: %v %v", ok, err)
		}
		if after := snapshot(t, r.local); !reflect.DeepEqual(r.before, after) {
			t.Errorf("not restored:\nbefore %+v\nafter  %+v", r.before, after)
		}
	})
}

// With core.autocrlf=true (the Git for Windows default) files on disk have
// CRLF; resolutions must keep that, and abort must restore every byte.
func TestCRLFWorkingTree(t *testing.T) {
	for _, finish := range []bool{true, false} {
		r := newRepo(t)
		git(t, r.local, "config", "core.autocrlf", "true")
		git(t, r.local, "rm", "-q", "--cached", "-r", ".")
		git(t, r.local, "reset", "-q", "--hard")
		write(t, r.local, "a.txt", "1 mine\r\n2\r\n3\r\n4\r\n5\r\n6\r\n7\r\n8\r\n9\r\n")
		write(t, r.local, "notes.txt", "my notes\r\n")
		r.before = snapshot(t, r.local)
		r.upstream(map[string]string{"a.txt": "1 theirs\n2\n3\n4\n5\n6\n7\n8\n9\n", "notes.txt": "their notes\n"})
		s := r.start()
		if !finish {
			if err := s.Abort(r.ctx); err != nil {
				t.Fatal(err)
			}
			if after := snapshot(t, r.local); !reflect.DeepEqual(r.before, after) {
				t.Errorf("abort did not restore every byte:\nbefore %q\nafter  %q", r.before.Files, after.Files)
			}
			continue
		}
		for _, p := range []string{"a.txt", "notes.txt"} {
			if err := s.ResolveFile(r.ctx, p, Both); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Finish(r.ctx); err != nil {
			t.Fatal(err)
		}
		if got := read(t, r.local, "a.txt"); got != "1 theirs\r\n1 mine\r\n2\r\n3\r\n4\r\n5\r\n6\r\n7\r\n8\r\n9\r\n" {
			t.Errorf("a.txt = %q", got)
		}
		if got := read(t, r.local, "notes.txt"); got != "their notes\r\nmy notes\r\n" {
			t.Errorf("notes.txt = %q", got)
		}
	}
}
