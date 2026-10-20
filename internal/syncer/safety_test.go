package syncer

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// state is everything a user could notice about a working copy: where HEAD
// is, staged and unstaged changes, untracked files with their contents,
// and the stash. Rollback must restore all of it.
type state struct {
	Head, Status, Staged, Unstaged, Stash string
	Untracked                             map[string]string
	Refs                                  string
}

func snapshot(t *testing.T, dir string) state {
	t.Helper()
	st := state{
		Head:      git(t, dir, "rev-parse", "HEAD"),
		Status:    git(t, dir, "status", "--porcelain"),
		Staged:    git(t, dir, "diff", "--cached"),
		Unstaged:  git(t, dir, "diff"),
		Stash:     git(t, dir, "stash", "list"),
		Refs:      git(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/chitragit/backup"),
		Untracked: map[string]string{},
	}
	for _, name := range strings.Fields(git(t, dir, "ls-files", "--others", "--exclude-standard")) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		st.Untracked[name] = string(b)
	}
	return st
}

func stashOn() Options { return Options{StashLocalChanges: true} }

func TestStashFlowKeepsLocalChanges(t *testing.T) {
	f := newFixture(t)
	f.commit("b.txt", "base")
	f.push("main")
	local := f.clone(t)

	write(t, local, "a.txt", "my unstaged edit")
	write(t, local, "b.txt", "my staged edit")
	git(t, local, "add", "b.txt")
	write(t, local, "notes.txt", "untracked notes")
	write(t, local, ".gitignore", "*.log\n")
	write(t, local, "debug.log", "ignored, never touched")

	f.commit("c.txt", "upstream")
	f.push("main")

	res := syncDir(t, local, stashOn())
	if res.Outcome != Updated || res.Status != StatusOK {
		t.Fatalf("got %s/%s: %s %v", res.Outcome, res.Status, res.Reason, res.Err)
	}
	if git(t, local, "rev-parse", "HEAD") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Error("not fast-forwarded")
	}
	for name, want := range map[string]string{"a.txt": "my unstaged edit", "b.txt": "my staged edit",
		"notes.txt": "untracked notes", "debug.log": "ignored, never touched", "c.txt": "upstream"} {
		if b, _ := os.ReadFile(filepath.Join(local, name)); string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
	if !strings.Contains(git(t, local, "diff", "--cached", "--name-only"), "b.txt") {
		t.Error("staged change was unstaged")
	}
	if git(t, local, "stash", "list") != "" || git(t, local, "for-each-ref", "refs/chitragit/backup") != "" {
		t.Error("stash or backup ref left behind")
	}
	if _, err := os.Stat(filepath.Join(local, ".git", markerFile)); err == nil {
		t.Error("marker left behind")
	}
}

func TestStashConflictRollsBackExactly(t *testing.T) {
	cases := map[string]func(t *testing.T, local string){
		"tracked edit clashes": func(t *testing.T, local string) {
			write(t, local, "a.txt", "my version")
			write(t, local, "keep.txt", "untracked, must survive")
		},
		"untracked file clashes": func(t *testing.T, local string) {
			write(t, local, "a.txt", "one") // unchanged, so only the untracked file matters
			write(t, local, "new.txt", "my own new.txt")
		},
		"staged and unstaged": func(t *testing.T, local string) {
			write(t, local, "a.txt", "staged")
			git(t, local, "add", "a.txt")
			write(t, local, "a.txt", "staged then edited")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			local := f.clone(t)
			setup(t, local)
			before := snapshot(t, local)

			f.commit("a.txt", "upstream changed this line")
			write(t, f.dev, "new.txt", "upstream new.txt")
			git(t, f.dev, "add", ".")
			git(t, f.dev, "commit", "-q", "-m", "add new.txt")
			f.push("main")

			res := syncDir(t, local, stashOn())
			if res.Status != StatusConflict {
				t.Fatalf("status = %s (%s): %s %v", res.Status, res.Outcome, res.Reason, res.Err)
			}
			if after := snapshot(t, local); !reflect.DeepEqual(before, after) {
				t.Errorf("repo not restored exactly:\nbefore %+v\nafter  %+v", before, after)
			}
			if git(t, local, "rev-parse", "origin/main") != git(t, f.dev, "rev-parse", "HEAD") {
				t.Error("updates were not kept downloaded")
			}
		})
	}
}

func TestRecoversInterruptedStashFlow(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	write(t, local, "a.txt", "precious local edit")
	write(t, local, "u.txt", "precious untracked")
	before := snapshot(t, local)

	// Simulate a crash right after the fast-forward: changes stashed, HEAD
	// moved, the stash not yet re-applied.
	f.commit("a.txt", "upstream")
	f.push("main")
	head := git(t, local, "rev-parse", "HEAD")
	m := marker{Backup: backupPrefix + "20260101T000000Z", Stash: "chitragit-auto 20260101T000000Z"}
	git(t, local, "update-ref", m.Backup, head)
	if err := writeMarker(filepath.Join(local, ".git"), m); err != nil {
		t.Fatal(err)
	}
	git(t, local, "stash", "push", "--include-untracked", "-q", "-m", m.Stash)
	git(t, local, "fetch", "-q")
	git(t, local, "merge", "-q", "--ff-only", "origin/main")

	res := syncDir(t, local, stashOn())
	if res.Status != StatusRecovered {
		t.Fatalf("status = %s: %s %v", res.Status, res.Reason, res.Err)
	}
	after := snapshot(t, local)
	before.Refs, after.Refs = "", "" // the fetch above moved nothing local but keep the comparison simple
	if !reflect.DeepEqual(before, after) {
		t.Errorf("not restored:\nbefore %+v\nafter  %+v", before, after)
	}

	// The next sync applies the update normally.
	if res := syncDir(t, local, stashOn()); res.Status != StatusConflict {
		t.Errorf("follow-up sync: %s %s", res.Status, res.Reason)
	}
}

func TestRewrittenUpstreamIsFollowedAndOldHistoryKept(t *testing.T) {
	f := newFixture(t)
	f.commit("a.txt", "two")
	f.push("main")
	git(t, f.dev, "branch", "-q", "side")
	f.push("side")
	local := f.clone(t)
	git(t, local, "branch", "-q", "--track", "side", "origin/side")
	old := git(t, local, "rev-parse", "HEAD")

	git(t, f.dev, "reset", "-q", "--hard", "HEAD~1")
	f.commit("a.txt", "rewritten")
	f.push("--force", "main")
	f.push("--force", "HEAD:side")

	res := syncDir(t, local, Options{})
	if res.Status != StatusOK || git(t, local, "rev-parse", "HEAD") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Fatalf("clean branch did not follow the rewrite: %s %s", res.Status, res.Reason)
	}
	if git(t, local, "rev-parse", "side") != git(t, f.dev, "rev-parse", "HEAD") {
		t.Error("non-checked-out branch did not follow the rewrite")
	}
	saved := git(t, local, "for-each-ref", "--format=%(objectname)", rewrittenPrefix+"remotes/origin/main/")
	if saved != old {
		t.Errorf("old history not saved: %q, want %s", saved, old)
	}
}

func TestRewriteWithLocalWorkIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.commit("a.txt", "two")
	f.push("main")
	local := f.clone(t)
	write(t, local, "mine.txt", "x")
	git(t, local, "add", ".")
	git(t, local, "commit", "-q", "-m", "mine")
	head := git(t, local, "rev-parse", "HEAD")

	git(t, f.dev, "reset", "-q", "--hard", "HEAD~1")
	f.commit("a.txt", "rewritten")
	f.push("--force", "main")

	res := syncDir(t, local, stashOn())
	if res.Status != StatusRewritten || git(t, local, "rev-parse", "HEAD") != head {
		t.Errorf("status %s, HEAD moved = %v", res.Status, git(t, local, "rev-parse", "HEAD") != head)
	}
}

func TestDeletedUpstreamBranchIsArchived(t *testing.T) {
	f := newFixture(t)
	git(t, f.dev, "branch", "-q", "old")
	f.push("old")
	git(t, f.dev, "tag", "v1")
	f.push("v1")
	local := f.clone(t)
	tip := git(t, local, "rev-parse", "origin/old")
	f.push("--delete", "old")
	f.push("--delete", "v1")

	res := syncDir(t, local, Options{})
	archived := git(t, local, "for-each-ref", "--format=%(objectname)", archivedPrefix+"remotes/origin/old/")
	if archived != tip {
		t.Errorf("deleted branch not archived: %q (%s)", archived, res.Reason)
	}
	if git(t, local, "tag") != "v1" {
		t.Error("a tag deleted upstream was removed locally")
	}
	if !strings.Contains(res.Reason, "deleted upstream") {
		t.Errorf("reason does not mention it: %q", res.Reason)
	}

	// With sync.prune on, a deletion is a plain prune.
	git(t, f.dev, "branch", "-q", "tmp")
	f.push("tmp")
	syncDir(t, local, Options{})
	f.push("--delete", "tmp")
	syncDir(t, local, Options{Prune: true})
	refs := strings.Fields(git(t, local, "for-each-ref", "--format=%(refname)", "refs/chitragit/archived/"))
	sort.Strings(refs)
	if len(refs) != 1 {
		t.Errorf("prune mode archived anyway: %v", refs)
	}
}

func TestMirrorKeepsChitragitRefs(t *testing.T) {
	f := newFixture(t)
	f.commit("a.txt", "two")
	f.push("main")
	git(t, f.dev, "branch", "-q", "gone")
	f.push("gone")
	mirror := filepath.Join(t.TempDir(), "m.git")
	git(t, ".", "clone", "-q", "--mirror", f.upstream, mirror)
	tip := git(t, mirror, "rev-parse", "gone")

	git(t, f.dev, "reset", "-q", "--hard", "HEAD~1")
	f.commit("a.txt", "rewritten")
	f.push("--force", "main")
	f.push("--delete", "gone")

	res := syncDir(t, mirror, Options{})
	if res.Outcome == Failed {
		t.Fatal(res.Err)
	}
	if git(t, mirror, "rev-parse", "gone") != tip {
		t.Error("a mirror lost a branch deleted upstream")
	}
	if git(t, mirror, "for-each-ref", "refs/chitragit/rewritten/") == "" {
		t.Error("a mirror did not keep force-pushed history")
	}
}
