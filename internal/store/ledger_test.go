package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openLedger(t *testing.T) (*Store, string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "c.db")
	s, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, db
}

func mustVerify(t *testing.T, s *Store, events int) {
	t.Helper()
	c, err := s.VerifyLedger(context.Background())
	if err != nil || !c.OK || c.Events != events {
		t.Fatalf("verify = %+v, %v; want ok with %d events", c, err, events)
	}
}

func TestEventsAreChained(t *testing.T) {
	ctx := context.Background()
	s, _ := openLedger(t)
	mustVerify(t, s, 0)
	for _, typ := range []string{"scan", "renamed", "sync"} {
		if err := s.AddEvent(ctx, "", typ, map[string]string{"message": typ}); err != nil {
			t.Fatal(err)
		}
	}
	mustVerify(t, s, 3)
	var prev, first string
	if err := s.DB.QueryRowContext(ctx, `SELECT prev_hash, hash FROM events WHERE seq = 2`).Scan(&prev, new(string)); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT hash FROM events WHERE seq = 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if prev != first || len(first) != 64 {
		t.Fatalf("row 2 prev_hash %q, row 1 hash %q", prev, first)
	}
}

func TestEventsFromBeforeV5AreSealedOnOpen(t *testing.T) {
	ctx := context.Background()
	s, db := openLedger(t)
	for i := 0; i < 3; i++ {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO events(at, type, payload) VALUES (?, 'old', '{}')`, Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddEvent(ctx, "", "new", nil); err != nil {
		t.Fatal(err)
	}
	mustVerify(t, s, 4)
}
