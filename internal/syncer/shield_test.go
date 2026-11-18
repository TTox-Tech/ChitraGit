package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// blockSHA is a Shield hook that blocks one commit and records every call.
func blockSHA(bad string, calls *[]string) ShieldFunc {
	return func(_ context.Context, _ string, commit string) (Inspection, error) {
		*calls = append(*calls, commit)
		if commit == bad {
			return Inspection{Block: true, Findings: 2, Worst: "A VS Code task that runs when the folder is opened"}, nil
		}
		return Inspection{}, nil
	}
}

// repoState is what a sync must leave alone when Shield blocks: local
// branches, HEAD, the index and the working tree.
func repoState(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(git(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"))
	b.WriteString("\n" + git(t, dir, "rev-parse", "HEAD"))
	b.WriteString("\n" + git(t, dir, "status", "--porcelain", "--untracked-files=all"))
	b.WriteString("\n" + git(t, dir, "ls-files", "-s"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b.WriteString("\n" + e.Name())
	}
	return b.String()
}

func TestShieldHoldsBlockedUpstream(t *testing.T) {
	f := newFixture(t)
	git(t, f.dev, "branch", "-q", "release")
	f.push("release")
	local := f.clone(t)
	git(t, local, "branch", "-q", "--track", "release", "origin/release")
	before := repoState(t, local)

	f.commit("evil.txt", "payload")
	f.push("main")
	git(t, f.dev, "push", "-q", "origin", "main:release")
	bad := git(t, f.dev, "rev-parse", "HEAD")

	var calls []string
	res := syncDir(t, local, Options{Shield: blockSHA(bad, &calls)})
	if res.Outcome != Skipped || res.Status != StatusBlocked || res.Category != string(core.CatBlocked) {
		t.Fatalf("got %s/%s/%s: %s %v", res.Outcome, res.Status, res.Category, res.Reason, res.Err)
	}
	if !strings.Contains(res.Reason, "KodeKavach blocked upstream "+bad[:12]) || !strings.Contains(res.Reason, "VS Code task") {
		t.Errorf("reason = %q", res.Reason)
	}
	if len(calls) != 1 {
		t.Errorf("inspected %q, want the one new tip once", calls)
	}
	if after := repoState(t, local); after != before {
		t.Fatalf("a blocked sync changed the repo:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(local, "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file of the blocked commit was written")
	}
	// The new commits are downloaded and pinned, so nothing is lost.
	if git(t, local, "rev-parse", "origin/main") != bad {
		t.Error("the blocked commit was not downloaded")
	}
	if !strings.Contains(git(t, local, "for-each-ref", "--format=%(objectname)", "refs/chitragit/snap/"), bad) {
		t.Error("the blocked commit is not pinned")
	}

	// It stays held on the next sync, with nothing new upstream.
	if again := syncDir(t, local, Options{Shield: blockSHA(bad, &calls)}); again.Status != StatusBlocked || repoState(t, local) != before {
		t.Fatalf("second sync: %s/%s %s", again.Outcome, again.Status, again.Reason)
	}

	// A clean commit on top is still held: the tip is what gets judged,
	// and a hook that passes it lets the update through.
	res = syncDir(t, local, Options{Shield: blockSHA("none", &calls)})
	if res.Status != StatusOK || git(t, local, "rev-parse", "HEAD") != bad {
		t.Fatalf("pass after block: %s/%s %s", res.Outcome, res.Status, res.Reason)
	}
}

func TestShieldPassesCleanUpstream(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	f.commit("b.txt", "two")
	f.push("main")
	want := git(t, f.dev, "rev-parse", "HEAD")

	var calls []string
	res := syncDir(t, local, Options{Shield: blockSHA("none", &calls)})
	if res.Outcome != Updated || res.Status != StatusOK || git(t, local, "rev-parse", "HEAD") != want {
		t.Fatalf("got %s/%s: %s", res.Outcome, res.Status, res.Reason)
	}
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("inspected %q", calls)
	}
	// Nothing new upstream: nothing to inspect.
	calls = nil
	if syncDir(t, local, Options{Shield: blockSHA("none", &calls)}); len(calls) != 0 {
		t.Errorf("inspected %q with nothing new", calls)
	}
}

// A hook that fails, or a sync killed while Shield reads, leaves the repo
// exactly as it was before the integrate step.
func TestShieldFailureOrKillLeavesRepoAsItWas(t *testing.T) {
	f := newFixture(t)
	local := f.clone(t)
	write(t, local, "mine.txt", "untracked work\n")
	before := repoState(t, local)
	f.commit("b.txt", "two")
	f.push("main")

	failing := func(context.Context, string, string) (Inspection, error) {
		return Inspection{}, &core.Error{Category: core.CatCorrupt, Message: "cannot read the commit"}
	}
	if res := syncDir(t, local, Options{Shield: failing}); res.Outcome != Failed || repoState(t, local) != before {
		t.Fatalf("failing hook: %s/%s %s", res.Outcome, res.Status, res.Reason)
	}

	reached := false
	restore := SetStepHook(func(name string) {
		if name == "sync.inspect" {
			reached = true
			panic(crashAt(name))
		}
	})
	var calls []string
	syncDir(t, local, Options{Shield: blockSHA("none", &calls), StashLocalChanges: true})
	restore()
	if !reached {
		t.Fatal("step never reached")
	}
	if after := repoState(t, local); after != before {
		t.Fatalf("a kill during inspect changed the repo:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(git(t, local, "rev-parse", "--absolute-git-dir"), markerFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a kill during inspect left an update marker")
	}
}
