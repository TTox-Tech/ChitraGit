package archive

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestInitAndFind(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	a, created, err := Init(ctx, root)
	if err != nil || !created {
		t.Fatalf("Init: %v created=%v", err, created)
	}
	for _, p := range []string{a.ConfigFile(), a.DBFile(), a.LogsDir(), a.BundlesDir()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}

	// Idempotent and does not overwrite the user's config.
	if err := os.WriteFile(a.ConfigFile(), []byte("[sync]\nworkers = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, created, err := Init(ctx, root); err != nil || created {
		t.Fatalf("second Init: %v created=%v", err, created)
	}
	if b, _ := os.ReadFile(a.ConfigFile()); string(b) != "[sync]\nworkers = 3\n" {
		t.Error("Init overwrote existing config")
	}

	deep := filepath.Join(root, "torvalds", "linux", "kernel")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	if found.Root != a.Root {
		t.Errorf("Find = %s, want %s", found.Root, a.Root)
	}

	if _, _, err := Init(ctx, filepath.Join(root, "torvalds")); core.CategoryOf(err) != core.CatConfig {
		t.Errorf("nested archive should be refused, got %v", err)
	}
}

func TestFindNotFound(t *testing.T) {
	_, err := Find(t.TempDir())
	if !errors.Is(err, ErrNotFound) && core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := Open(t.TempDir()); core.CategoryOf(err) != core.CatConfig {
		t.Fatalf("Open on plain folder: %v", err)
	}
}

func TestLock(t *testing.T) {
	a, _, err := Init(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l1, err := a.Acquire("sync")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Acquire("sync again")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.Holder.PID != os.Getpid() {
		t.Fatalf("second acquire should report holder, got %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := a.Acquire("sync")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	mustRelease(t, l2)
}

func TestStaleLockReplaced(t *testing.T) {
	a, _, err := Init(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	write := func(info LockInfo) {
		data, _ := json.Marshal(info)
		if err := os.WriteFile(a.lockFile(), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Dead process on this host.
	write(LockInfo{PID: 999_999_99, Host: host, Started: time.Now()})
	l, err := a.Acquire("sync")
	if err != nil {
		t.Fatalf("stale lock should be replaced: %v", err)
	}
	mustRelease(t, l)

	// Corrupt lock file (crash mid-write).
	if err := os.WriteFile(a.lockFile(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err = a.Acquire("sync")
	if err != nil {
		t.Fatalf("corrupt lock should be replaced: %v", err)
	}
	mustRelease(t, l)

	// Fresh lock from another machine (shared drive) is honoured.
	write(LockInfo{PID: 1, Host: "other-machine", Started: time.Now()})
	if _, err := a.Acquire("sync"); err == nil {
		t.Fatal("lock held by another host must be honoured")
	}
	// ...unless it is very old.
	write(LockInfo{PID: 1, Host: "other-machine", Started: time.Now().Add(-48 * time.Hour)})
	l, err = a.Acquire("sync")
	if err != nil {
		t.Fatalf("old foreign lock should be replaced: %v", err)
	}
	mustRelease(t, l)
}

func mustRelease(t *testing.T, l *Lock) {
	t.Helper()
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}
