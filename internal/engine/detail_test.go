package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/scan"
	"github.com/TTox-Tech/chitragit/internal/store"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

func TestDetailReaders(t *testing.T) {
	ctx := context.Background()
	_, g, _ := setup(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	// An empty repo is not an error.
	if cs, err := Commits(ctx, g, dir, "", 10); err != nil || len(cs) != 0 {
		t.Fatalf("empty repo commits = %v, %v", cs, err)
	}
	if rd, err := ReadReadme(ctx, g, dir); err != nil || rd != nil {
		t.Fatalf("empty repo readme = %v, %v", rd, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	git(t, dir, "tag", "v1")
	git(t, dir, "checkout", "-q", "-b", "side")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "side work")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "main work")
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	git(t, dir, "update-ref", "refs/chitragit/archived/origin/old", "v1")
	// The working tree must not matter: README is read from HEAD.
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs, err := Commits(ctx, g, dir, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	graphOnly := 0
	for _, c := range cs {
		if c.SHA == "" {
			graphOnly++
			continue
		}
		subjects = append(subjects, c.Subject)
	}
	if len(subjects) != 4 || subjects[0] != "merge side" || graphOnly == 0 {
		t.Fatalf("commits = %+v", cs)
	}
	if !strings.Contains(strings.Join(cs[0].Refs, ","), "main") {
		t.Errorf("HEAD commit refs = %v", cs[0].Refs)
	}
	for _, c := range cs {
		for _, r := range c.Refs {
			if strings.Contains(r, "chitragit") {
				t.Errorf("internal ref shown in log: %s", r)
			}
		}
	}

	refs, err := Refs(ctx, g, dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range refs {
		kinds[r.Name] = r.Kind
		if r.Name == "main" && !r.Current {
			t.Error("main not marked current")
		}
	}
	if kinds["main"] != RefBranch || kinds["side"] != RefBranch || kinds["v1"] != RefTag || kinds["origin/old"] != RefArchived {
		t.Errorf("refs = %+v", refs)
	}

	rd, err := ReadReadme(ctx, g, dir)
	if err != nil || rd == nil || rd.Name != "readme.md" || rd.Text != "# Hello\n" || !rd.Markdown {
		t.Fatalf("readme = %+v, %v", rd, err)
	}

	n, err := MeasureSize(ctx, dir)
	if err != nil || n == 0 {
		t.Errorf("size = %d, %v", n, err)
	}
}

func TestActivityAndHealthAfterSync(t *testing.T) {
	ctx := context.Background()
	root, g, st := setup(t)
	up := filepath.Join(t.TempDir(), "up.git")
	git(t, ".", "init", "-q", "--bare", up)
	dev := filepath.Join(t.TempDir(), "dev")
	git(t, ".", "clone", "-q", up, dev)
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dev, "push", "-q", "origin", "HEAD")
	git(t, ".", "clone", "-q", up, filepath.Join(root, "owner", "r"))
	git(t, ".", "init", "-q", filepath.Join(root, "local"))
	git(t, dev, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dev, "tag", "v2")
	git(t, dev, "push", "-q", "--tags", "origin", "HEAD")

	if _, err := Scan(ctx, root, scan.Options{}, g, st); err != nil {
		t.Fatal(err)
	}
	h, err := st.Health(ctx, time.Hour)
	if err != nil || h.Total != 2 || h.Cards[store.CardNeverSync] != 1 { // the local-only repo is never "never synced"
		t.Fatalf("health before sync = %+v, %v", h, err)
	}
	if _, err := Sync(ctx, SyncRequest{Root: root, Options: syncer.Options{Mode: "ff"}}, g, st); err != nil {
		t.Fatal(err)
	}

	feed, err := st.Activity(ctx, store.ActivityFilter{})
	if err != nil || len(feed) != 1 {
		t.Fatalf("feed = %+v, %v", feed, err)
	}
	a := feed[0]
	if a.Path != "owner/r" || a.Type != store.ActivityRefs || len(a.Changes) < 2 {
		t.Errorf("entry = %+v", a)
	}
	if feed, _ := st.Activity(ctx, store.ActivityFilter{Since: time.Now().Add(time.Hour)}); len(feed) != 0 {
		t.Errorf("since filter ignored: %+v", feed)
	}

	repos, _ := st.Repos(ctx)
	if err := MeasureSizes(ctx, root, repos, st); err != nil {
		t.Fatal(err)
	}
	h, err = st.Health(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if h.Cards[store.CardNeverSync] != 0 || len(h.Runs) != 1 || h.Runs[0].Updated != 1 {
		t.Errorf("health after sync = %+v", h)
	}
	if h.Measured != 2 || h.DiskTotal == 0 || len(h.Largest) != 2 {
		t.Errorf("disk usage = %d bytes over %d repos, largest %+v", h.DiskTotal, h.Measured, h.Largest)
	}
}
