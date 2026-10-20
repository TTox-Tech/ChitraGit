// Package store owns the archive database (<archive>/.chitragit/chitragit.db).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO

	"github.com/TTox-Tech/chitragit/internal/core"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store wraps the archive database.
type Store struct {
	DB   *sql.DB
	path string
}

// Options tune how the database is opened.
type Options struct {
	// NetworkFS disables WAL, which is unsafe on NFS/SMB (PRD §8.4).
	NetworkFS bool
}

// Open opens (creating if needed) the database at dbPath and applies any
// pending migrations. Before migrating an existing database, a copy is
// saved next to it as <name>.bak-v<old version>.
func Open(ctx context.Context, dbPath string, opts Options) (*Store, error) {
	_, statErr := os.Stat(dbPath)
	existed := statErr == nil

	journal := "WAL"
	if opts.NetworkFS {
		journal = "DELETE"
	}
	dsn := "file:" + dbPath +
		"?_pragma=busy_timeout(10000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(" + journal + ")" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, dbError("cannot open archive database", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, dbError("cannot open archive database", err)
	}
	s := &Store{DB: db, path: dbPath}
	if err := s.migrate(ctx, existed); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("bad migration file name %q", e.Name())
		}
		body, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// Latest returns the schema version this build expects.
func Latest() int {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

func (s *Store) migrate(ctx context.Context, existed bool) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return dbError("cannot prepare archive database", err)
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return dbError("cannot read archive database version", err)
	}
	ms, err := loadMigrations()
	if err != nil {
		return dbError("broken build: cannot load migrations", err)
	}
	if latest := ms[len(ms)-1].version; current > latest {
		return &core.Error{
			Category: core.CatEnvironment,
			Message:  fmt.Sprintf("this archive was used by a newer ChitraGit (schema v%d, this build knows v%d)", current, latest),
			Hint:     "upgrade ChitraGit to open this archive",
		}
	}

	backedUp := false
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if existed && current > 0 && !backedUp {
			if err := s.backup(ctx, fmt.Sprintf("%s.bak-v%d", s.path, current)); err != nil {
				return dbError("cannot back up archive database before upgrading", err)
			}
			backedUp = true
		}
		if err := s.apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot start migration", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return dbError("migration "+m.name+" failed", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, Now()); err != nil {
		return dbError("migration "+m.name+" failed", err)
	}
	if err := tx.Commit(); err != nil {
		return dbError("migration "+m.name+" failed", err)
	}
	return nil
}

// Now returns the current time in the format stored in the database.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func dbError(msg string, err error) error {
	cat := core.CatInternal
	if errors.Is(err, fs.ErrPermission) {
		cat = core.CatPermission
	}
	return &core.Error{Category: cat, Message: msg, Err: err}
}

// backup writes a consistent copy of the database (including WAL contents)
// to dst, replacing any previous backup at that path.
func (s *Store) backup(ctx context.Context, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}
