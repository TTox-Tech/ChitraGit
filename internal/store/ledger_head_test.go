package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func addEvents(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.AddEvent(context.Background(), "", "sync", map[string]int{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
}

// Rows removed from the end leave a valid, shorter chain. The head file
// still names the last event, so verify finds the cut, also after more
// events are written and the database is reopened.
func TestLedgerHeadFindsRowsRemovedFromTheEnd(t *testing.T) {
	for name, cut := range map[string]string{
		"last two": `DELETE FROM events WHERE seq > 2`,
		"all":      `DELETE FROM events`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, db := openLedger(t)
			addEvents(t, s, 4)
			if h, ok, err := s.ReadLedgerHead(); err != nil || !ok || h.Seq != 4 {
				t.Fatalf("head = %+v %v %v, want seq 4", h, ok, err)
			}
			if _, err := s.DB.ExecContext(ctx, cut); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err := Open(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }() // s is reopened below; close the last one
			check := func() {
				t.Helper()
				c, err := s.VerifyLedger(ctx)
				if err != nil || c.OK || c.Head != 4 || !strings.Contains(c.Reason, "removed") && !strings.Contains(c.Reason, "recorded outside") {
					t.Fatalf("verify = %+v, %v; want the cut found", c, err)
				}
			}
			check()
			addEvents(t, s, 3) // new events must not hide the cut
			check()
			s.Close()
			if s, err = Open(ctx, db); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

// A head file that is behind (a crash before it was written) only covers
// fewer events; it never makes a sound ledger look broken.
func TestLedgerHeadBehindIsNotABreak(t *testing.T) {
	ctx := context.Background()
	s, db := openLedger(t)
	addEvents(t, s, 2)
	old, err := os.ReadFile(db + ".head")
	if err != nil {
		t.Fatal(err)
	}
	addEvents(t, s, 3)
	if err := os.WriteFile(db+".head", old, 0o644); err != nil {
		t.Fatal(err)
	}
	mustVerify(t, s, 5)
	s.Close()
	if s, err = Open(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addEvents(t, s, 1)
	mustVerify(t, s, 6)
	if h, _, _ := s.ReadLedgerHead(); h.Seq != 6 {
		t.Fatalf("head did not catch up: %+v", h)
	}
}

// A new database starts its own head, even over a file a removed database
// left behind.
func TestLedgerHeadOfANewDatabase(t *testing.T) {
	ctx := context.Background()
	s, db := openLedger(t)
	addEvents(t, s, 5)
	s.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(db + suffix)
	}
	s, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addEvents(t, s, 2)
	mustVerify(t, s, 2)
}

func TestLedgerHeadUnreadable(t *testing.T) {
	s, db := openLedger(t)
	addEvents(t, s, 1)
	if err := os.WriteFile(db+".head", []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := s.VerifyLedger(context.Background())
	if err != nil || c.OK || !strings.Contains(c.Reason, "head") {
		t.Fatalf("verify = %+v, %v", c, err)
	}
}
