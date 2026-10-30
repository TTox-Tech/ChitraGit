package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// NoteFailure counts one confirmed "upstream is gone" answer for a repo.
// A run counts at most once, so threshold confirmations always come from
// that many different runs. When the count reaches threshold, the repo is
// marked rescued and delete-protected; newly is true for that one call.
func (s *Store) NoteFailure(ctx context.Context, repoID string, runID int64, reason, detail string, threshold int) (r Rescue, newly bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, false, dbError("cannot record rescue", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := Now()
	var lastRun sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT last_run_id FROM rescues WHERE repo_id = ?`, repoID).Scan(&lastRun)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO rescues(repo_id, reason, detail, confirmations, first_failed_at, last_run_id)
			VALUES (?, ?, ?, 1, ?, ?)`, repoID, reason, detail, now, runID)
	case err == nil && (!lastRun.Valid || lastRun.Int64 != runID):
		_, err = tx.ExecContext(ctx, `UPDATE rescues SET confirmations = confirmations + 1, reason = ?, detail = ?, last_run_id = ?
			WHERE repo_id = ?`, reason, detail, runID, repoID)
	}
	if err != nil {
		return r, false, dbError("cannot record rescue", err)
	}
	if r, err = rescue(ctx, tx, repoID); err != nil {
		return r, false, dbError("cannot read rescue", err)
	}
	if r.RescuedAt == "" && r.Confirmations >= threshold {
		r.RescuedAt, newly = now, true
		if _, err := tx.ExecContext(ctx, `UPDATE rescues SET rescued_at = ? WHERE repo_id = ?`, now, repoID); err != nil {
			return r, false, dbError("cannot record rescue", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE repos SET locked = 1 WHERE id = ?`, repoID); err != nil {
			return r, false, dbError("cannot record rescue", err)
		}
		if err := addEvent(ctx, tx, repoID, "rescued", map[string]string{"reason": reason, "detail": detail,
			"message": RescueMessage(reason)}); err != nil {
			return r, false, err
		}
	}
	if r.RescuedAt != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE repos SET status = 'rescued', status_reason = ? WHERE id = ?`,
			RescueMessage(r.Reason), repoID); err != nil {
			return r, false, dbError("cannot record rescue", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return r, false, dbError("cannot record rescue", err)
	}
	return r, newly, nil
}

// RescueMessage is the plain-language reason shown for a rescued repo.
func RescueMessage(reason string) string {
	switch reason {
	case "legal":
		return "upstream was taken down for legal reasons (e.g. DMCA); your copy is kept safe"
	case "gone":
		return "upstream says this repo no longer exists (deleted or made private); your copy is kept safe"
	}
	return "the upstream repo can no longer be found; your copy is kept safe"
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func rescue(ctx context.Context, q querier, repoID string) (Rescue, error) {
	r := Rescue{RepoID: repoID}
	err := q.QueryRowContext(ctx, `SELECT reason, COALESCE(detail,''), confirmations, first_failed_at,
		COALESCE(rescued_at,''), COALESCE(bundle_path,''), COALESCE(bundled_at,'') FROM rescues WHERE repo_id = ?`, repoID).
		Scan(&r.Reason, &r.Detail, &r.Confirmations, &r.FirstFailedAt, &r.RescuedAt, &r.BundlePath, &r.BundledAt)
	return r, err
}

// Rescues lists confirmed rescues.
func (s *Store) Rescues(ctx context.Context) ([]Rescue, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id FROM rescues WHERE rescued_at IS NOT NULL ORDER BY rescued_at`)
	if err != nil {
		return nil, dbError("cannot read rescues", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, dbError("cannot read rescues", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]Rescue, 0, len(ids))
	for _, id := range ids {
		r, err := rescue(ctx, s.DB, id)
		if err != nil {
			return nil, dbError("cannot read rescue", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// SetBundle records a fresh bundle backup on the repo's rescue record. A
// bundle of a repo with no rescue record is not tracked in the database.
func (s *Store) SetBundle(ctx context.Context, repoID, path string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rescues SET bundle_path = ?, bundled_at = ? WHERE repo_id = ?`, path, Now(), repoID)
	if err != nil {
		return dbError("cannot record bundle", err)
	}
	return nil
}

// SetLocked turns delete-protection on or off.
func (s *Store) SetLocked(ctx context.Context, repoID string, locked bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot change protection", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE repos SET locked = ? WHERE id = ?`, locked, repoID)
	if err != nil {
		return dbError("cannot change protection", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &core.Error{Category: core.CatConfig, Message: "no such repo"}
	}
	typ, msg := "unlocked", "protection removed"
	if locked {
		typ, msg = "locked", "protected from deletion"
	}
	if err := addEvent(ctx, tx, repoID, typ, map[string]string{"message": msg}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return dbError("cannot change protection", err)
	}
	return nil
}

// SetArchivedUpstream stores the "Archived upstream" badge.
func (s *Store) SetArchivedUpstream(ctx context.Context, repoID string, archived bool) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE repos SET archived_upstream = ? WHERE id = ?`, archived, repoID); err != nil {
		return dbError("cannot update repo", err)
	}
	return nil
}

// RepoMoved stores a repo's new remotes after its upstream was renamed or
// transferred, and logs the move.
func (s *Store) RepoMoved(ctx context.Context, r core.Repo, from, to string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot record move", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := setRemotes(ctx, tx, r); err != nil {
		return err
	}
	if err := addEvent(ctx, tx, r.ID, "renamed", map[string]string{"from": from, "to": to,
		"message": "upstream moved; now syncing from " + to}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return dbError("cannot record move", err)
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func addEvent(ctx context.Context, x execer, repoID, typ string, payload any) error {
	var body any
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = string(b)
	}
	if _, err := x.ExecContext(ctx, `INSERT INTO events(at, repo_id, type, payload) VALUES (?, ?, ?, ?)`,
		Now(), nullable(repoID), typ, body); err != nil {
		return dbError("cannot log event", err)
	}
	return nil
}

// SourceRow is a followed source.
type SourceRow struct {
	ID            int64  `json:"id"`
	Spec          string `json:"spec"`
	Filters       string `json:"filters,omitempty"` // JSON
	AddedAt       string `json:"added_at"`
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// AddSource follows a source, or updates its filters. It reports whether
// the source is new.
func (s *Store) AddSource(ctx context.Context, spec, filters string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sources(spec, filters, added_at) VALUES (?, ?, ?)
		ON CONFLICT(spec) DO NOTHING`, spec, nullable(filters), Now())
	if err != nil {
		return false, dbError("cannot add source", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE sources SET filters = ? WHERE spec = ?`, nullable(filters), spec)
	if err != nil {
		return false, dbError("cannot update source", err)
	}
	return false, nil
}

// RemoveSource stops following a source. Repos already cloned stay.
func (s *Store) RemoveSource(ctx context.Context, spec string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sources WHERE spec = ?`, spec)
	if err != nil {
		return false, dbError("cannot remove source", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Sources lists followed sources in the order they were added.
func (s *Store) Sources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, spec, COALESCE(filters,''), added_at, COALESCE(last_checked_at,''),
		COALESCE(last_error,'') FROM sources ORDER BY id`)
	if err != nil {
		return nil, dbError("cannot read sources", err)
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(&r.ID, &r.Spec, &r.Filters, &r.AddedAt, &r.LastCheckedAt, &r.LastError); err != nil {
			return nil, dbError("cannot read sources", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceChecked records the outcome of checking a source.
func (s *Store) SourceChecked(ctx context.Context, id int64, problem string) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE sources SET last_checked_at = ?, last_error = ? WHERE id = ?`,
		Now(), nullable(problem), id); err != nil {
		return dbError("cannot update source", err)
	}
	return nil
}
