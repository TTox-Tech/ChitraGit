package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The ledger head (CG-07.8): the hash chain shows a row that was changed
// or removed in the middle, but rows removed from the end leave a shorter,
// still valid chain. So the last event's seq and hash are also kept in a
// small file next to the database, <db>.head. VerifyLedger checks that the
// event it names is still in the table, unchanged.
//
// The file only moves forward from a head that still matches: once the
// recorded event is gone or changed, it is left alone, so the evidence
// stays until verify reports it. A head file that falls behind (a crash
// between the commit and the write, or another process) only means fewer
// events are covered; it never reports a sound ledger as broken.

// LedgerHead is the last event as recorded outside the database.
type LedgerHead struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

func (s *Store) headPath() string { return s.path + ".head" }

// ReadLedgerHead reads the head file. ok is false when there is none yet.
func (s *Store) ReadLedgerHead() (h LedgerHead, ok bool, err error) {
	b, err := os.ReadFile(s.headPath())
	if errors.Is(err, os.ErrNotExist) {
		return h, false, nil
	}
	if err != nil {
		return h, false, err
	}
	if err := json.Unmarshal(b, &h); err != nil || h.Seq < 0 || h.Seq > 0 && len(h.Hash) != 64 {
		return h, false, errors.New("ledger head file is not readable")
	}
	return h, true, nil
}

// loadHead reads the head file on open. A new database starts a new head:
// a file left by a database that no longer exists does not describe it.
func (s *Store) loadHead(existed bool) {
	s.headMu.Lock()
	defer s.headMu.Unlock()
	if !existed {
		s.head, s.headStuck = LedgerHead{}, false
		return
	}
	h, _, err := s.ReadLedgerHead()
	s.head, s.headStuck = h, err != nil // an unreadable file is kept for verify to report
}

// commit commits tx and then moves the head file to the newest event.
// Writing the head is best effort: the change is already committed, and a
// head left behind only weakens the check.
func (s *Store) commit(ctx context.Context, tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	s.noteHead(context.WithoutCancel(ctx))
	return nil
}

func (s *Store) noteHead(ctx context.Context) {
	s.headMu.Lock()
	defer s.headMu.Unlock()
	if s.headStuck {
		return
	}
	var last LedgerHead
	var hash sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT seq, hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&last.Seq, &hash)
	if err != nil || !hash.Valid || last.Seq <= s.head.Seq {
		return
	}
	last.Hash = hash.String
	if s.head.Seq > 0 {
		var cur sql.NullString
		err := s.DB.QueryRowContext(ctx, `SELECT hash FROM events WHERE seq = ?`, s.head.Seq).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) || err == nil && cur.String != s.head.Hash {
			s.headStuck = true // keep what the file says for verify
			return
		}
		if err != nil {
			return
		}
	}
	if writeHead(s.headPath(), last) == nil {
		s.head = last
	}
}

// writeHead replaces the head file atomically.
func writeHead(path string, h LedgerHead) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
