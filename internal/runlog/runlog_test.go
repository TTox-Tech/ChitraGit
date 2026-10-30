package runlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreatePruneLatest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, 11, 5, 2, 0, 0, 0, time.Local)

	old, err := Create(dir, "sync", now.Add(-31*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	var names []string
	for i := 0; i < 3; i++ {
		f, err := Create(dir, "sync", now)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(f.Name()))
		f.Close()
	}
	if names[0] != "sync-20261105-020000.log" || names[1] != "sync-20261105-020000-2.log" {
		t.Fatalf("names = %v", names)
	}
	if got := filepath.Base(Latest(dir, "sync")); got != names[2] {
		t.Errorf("Latest = %s, want %s", got, names[2])
	}
	if Latest(dir, "scan") != "" {
		t.Error("no scan logs exist")
	}

	// Files ChitraGit did not create are never touched, however old.
	mine := filepath.Join(dir, "notes.log")
	if err := os.WriteFile(mine, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(mine, now.Add(-400*24*time.Hour), now.Add(-400*24*time.Hour))

	n, err := Prune(dir, Keep, now)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d %v", n, err)
	}
	if _, err := os.Stat(old.Name()); !os.IsNotExist(err) {
		t.Error("old log should be gone")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("user file must survive")
	}
	if n, err := Prune(filepath.Join(dir, "missing"), Keep, now); n != 0 || err != nil {
		t.Errorf("missing dir: %d %v", n, err)
	}
}
