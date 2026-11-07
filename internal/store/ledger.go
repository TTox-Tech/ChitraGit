package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The events table is a hash-chained ledger (schema v5). Each row's hash
// covers its own fields and the previous row's hash, so editing or
// deleting a row breaks every hash after it.

// eventHash is the hex SHA-256 of one row and the hash before it.
// NULL columns hash as empty strings.
func eventHash(seq int64, at, repoID, typ, payload, prev string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join([]string{strconv.FormatInt(seq, 10), at, repoID, typ, payload, prev}, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

// addEvent appends one event and chains it. The row is inserted first, so
// the transaction holds SQLite's write lock before it reads the previous
// hash; no other writer can slip a row in between.
func addEvent(ctx context.Context, tx *sql.Tx, repoID, typ string, payload any) error {
	body := ""
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = string(b)
	}
	at := Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO events(at, repo_id, type, payload) VALUES (?, ?, ?, ?)`,
		at, nullable(repoID), typ, nullable(body))
	if err != nil {
		return dbError("cannot log event", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return dbError("cannot log event", err)
	}
	var prev sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT hash FROM events WHERE seq < ? ORDER BY seq DESC LIMIT 1`, seq).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbError("cannot log event", err)
	}
	hash := eventHash(seq, at, repoID, typ, body, prev.String)
	if _, err := tx.ExecContext(ctx, `UPDATE events SET prev_hash = ?, hash = ? WHERE seq = ?`,
		prev.String, hash, seq); err != nil {
		return dbError("cannot log event", err)
	}
	return nil
}

// sealEvents chains the rows written before schema v5. Only rows older
// than the first hashed row are sealed: a hash removed later stays
// missing, so VerifyLedger still reports it.
func (s *Store) sealEvents(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot seal event log", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT seq, at, COALESCE(repo_id, ''), type, COALESCE(payload, '')
		FROM events WHERE hash IS NULL
		AND seq < COALESCE((SELECT seq FROM events WHERE hash IS NOT NULL ORDER BY seq LIMIT 1), 9223372036854775807)
		ORDER BY seq`)
	if err != nil {
		return dbError("cannot seal event log", err)
	}
	type row struct {
		seq                    int64
		at, repo, typ, payload string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.seq, &r.at, &r.repo, &r.typ, &r.payload); err != nil {
			rows.Close()
			return dbError("cannot seal event log", err)
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return dbError("cannot seal event log", err)
	}
	if len(todo) == 0 {
		return nil
	}
	prev := ""
	for _, r := range todo {
		h := eventHash(r.seq, r.at, r.repo, r.typ, r.payload, prev)
		if _, err := tx.ExecContext(ctx, `UPDATE events SET prev_hash = ?, hash = ? WHERE seq = ?`, prev, h, r.seq); err != nil {
			return dbError("cannot seal event log", err)
		}
		prev = h
	}
	if err := tx.Commit(); err != nil {
		return dbError("cannot seal event log", err)
	}
	return nil
}

// LedgerCheck is the result of walking the event chain.
type LedgerCheck struct {
	Events int    `json:"events"`
	OK     bool   `json:"ok"`
	Seq    int64  `json:"broken_at,omitempty"` // first row that does not match
	Reason string `json:"reason,omitempty"`
}

// VerifyLedger walks every event in order and recomputes the chain.
//
// shortcut: removing rows from the end of the log is not detected, since
// nothing outside the table records the last hash. Upgrade when verify
// (CG-07) keeps the head hash outside the database.
func (s *Store) VerifyLedger(ctx context.Context) (LedgerCheck, error) {
	var c LedgerCheck
	rows, err := s.DB.QueryContext(ctx, `SELECT seq, at, COALESCE(repo_id, ''), type, COALESCE(payload, ''),
		COALESCE(prev_hash, ''), hash FROM events ORDER BY seq`)
	if err != nil {
		return c, dbError("cannot read event log", err)
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var seq int64
		var at, repo, typ, payload, prevHash string
		var hash sql.NullString
		if err := rows.Scan(&seq, &at, &repo, &typ, &payload, &prevHash, &hash); err != nil {
			return c, dbError("cannot read event log", err)
		}
		c.Events++
		switch {
		case !hash.Valid:
			c.Seq, c.Reason = seq, "row has no hash"
		case prevHash != prev:
			c.Seq, c.Reason = seq, "row before it was changed or removed"
		case hash.String != eventHash(seq, at, repo, typ, payload, prev):
			c.Seq, c.Reason = seq, "row was changed"
		}
		if c.Reason != "" {
			return c, nil
		}
		prev = hash.String
	}
	if err := rows.Err(); err != nil {
		return c, dbError("cannot read event log", err)
	}
	c.OK = true
	return c, nil
}
