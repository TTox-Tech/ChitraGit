package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "c.db")

	s, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v < 1 {
		t.Fatalf("schema version = %d, %v ", v, err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO repos(id, path, kind, first_seen_at) VALUES ('u1', 'a/b', 'normal', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM repos`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("data lost on reopen: %d, %v", n, err)
	}
	if _, err := os.Stat(db + ".bak-v1"); err == nil {
		t.Error("no backup expected when nothing was migrated")
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO tags(repo_id, tag) VALUES ('missing', 'x')`)
	if err == nil {
		t.Fatal("foreign keys must be enforced")
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "c.db")
	s, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (999, 'future', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = Open(ctx, db)
	if core.CategoryOf(err) != core.CatEnvironment {
		t.Fatalf("want environment error for newer schema, got %v", err)
	}
}

func TestBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO repos(id, path, kind, first_seen_at) VALUES ('u1', 'a/b', 'normal', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "backup.db")
	for i := 0; i < 2; i++ { // second call must replace the first backup
		if err := s.backup(ctx, dst); err != nil {
			t.Fatal(err)
		}
	}
	b, err := Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM repos`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup content: %d, %v", n, err)
	}
}
