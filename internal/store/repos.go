package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/TTox-Tech/chitragit/internal/core"
)

const repoColumns = `id, path, kind, COALESCE(provider,''), COALESCE(host,''), COALESCE(owner,''),
	COALESCE(name,''), status, COALESCE(status_reason,''), favourite, missing, COALESCE(last_synced_at,'')`

// Repos returns every known repo, favourites first, then least recently
// synced (never-synced first), which is also the sync priority order.
func (s *Store) Repos(ctx context.Context) ([]core.Repo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+repoColumns+` FROM repos
		ORDER BY favourite DESC, last_synced_at IS NOT NULL, last_synced_at, path`)
	if err != nil {
		return nil, dbError("cannot read repos", err)
	}
	defer rows.Close()
	var out []core.Repo
	for rows.Next() {
		var r core.Repo
		if err := rows.Scan(&r.ID, &r.Path, &r.Kind, &r.Provider, &r.Host, &r.Owner, &r.Name,
			&r.Status, &r.StatusReason, &r.Favourite, &r.Missing, &r.LastSyncedAt); err != nil {
			return nil, dbError("cannot read repos", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScanChanges summarises what ApplyScan changed.
type ScanChanges struct {
	Added, Moved, Missing, Returned int
}

// ApplyScan records the outcome of a scan in one transaction.
//
// present lists every repo found on disk, with its stable ID. Repos with
// remotes set are new or moved and get their remote info refreshed. Known
// repos that are not in present are marked missing, never deleted.
func (s *Store) ApplyScan(ctx context.Context, present []core.Repo) (ScanChanges, error) {
	var ch ScanChanges
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ch, dbError("cannot save scan", err)
	}
	defer func() { _ = tx.Rollback() }()

	type known struct {
		path    string
		missing bool
	}
	before := map[string]known{}
	rows, err := tx.QueryContext(ctx, `SELECT id, path, missing FROM repos`)
	if err != nil {
		return ch, dbError("cannot save scan", err)
	}
	for rows.Next() {
		var id string
		var k known
		if err := rows.Scan(&id, &k.path, &k.missing); err != nil {
			rows.Close()
			return ch, dbError("cannot save scan", err)
		}
		before[id] = k
	}
	rows.Close()

	now := Now()
	seen := map[string]bool{}
	for _, r := range present {
		seen[r.ID] = true
		// A different repo may still own this path (the folder was replaced).
		// Free the path; that older repo is gone from disk.
		if _, err := tx.ExecContext(ctx, `UPDATE repos SET path = path || '#' || id, missing = 1
			WHERE path = ? AND id <> ?`, r.Path, r.ID); err != nil {
			return ch, dbError("cannot save scan", err)
		}
		old, exists := before[r.ID]
		switch {
		case !exists:
			ch.Added++
			_, err = tx.ExecContext(ctx, `INSERT INTO repos(id, path, kind, provider, host, owner, name, first_seen_at, last_scanned_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.Path, r.Kind, r.Provider, r.Host, r.Owner, r.Name, now, now)
		default:
			if old.path != r.Path {
				ch.Moved++
			} else if old.missing {
				ch.Returned++
			}
			_, err = tx.ExecContext(ctx, `UPDATE repos SET path = ?, kind = ?, missing = 0, last_scanned_at = ? WHERE id = ?`,
				r.Path, r.Kind, now, r.ID)
		}
		if err != nil {
			return ch, dbError("cannot save scan", err)
		}
		if r.Remotes != nil {
			if err := setRemotes(ctx, tx, r); err != nil {
				return ch, err
			}
		}
	}

	for id, k := range before {
		if !seen[id] && !k.missing {
			ch.Missing++
			if _, err := tx.ExecContext(ctx, `UPDATE repos SET missing = 1 WHERE id = ?`, id); err != nil {
				return ch, dbError("cannot save scan", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ch, dbError("cannot save scan", err)
	}
	return ch, nil
}

func setRemotes(ctx context.Context, tx *sql.Tx, r core.Repo) error {
	if _, err := tx.ExecContext(ctx, `UPDATE repos SET provider = ?, host = ?, owner = ?, name = ? WHERE id = ?`,
		r.Provider, r.Host, r.Owner, r.Name, r.ID); err != nil {
		return dbError("cannot save remotes", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM remotes WHERE repo_id = ?`, r.ID); err != nil {
		return dbError("cannot save remotes", err)
	}
	for name, url := range r.Remotes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO remotes(repo_id, name, url) VALUES (?, ?, ?)`, r.ID, name, url); err != nil {
			return dbError("cannot save remotes", err)
		}
	}
	return nil
}

// RunItem is the journal entry for one repo in a run.
type RunItem struct {
	RepoID     string
	Outcome    string // updated | unchanged | skipped | failed
	Status     string // new repo status, e.g. ok, diverged, dirty
	Reason     string
	Category   string
	StartedAt  string
	FinishedAt string
	Changes    []core.RefChange
	Remotes    *core.Repo // refreshed remote info, when read during the sync
	// Conflict is set when local changes clashed with the update (status
	// conflict or resolving); ConflictFiles may be nil when not known.
	Conflict      bool
	ConflictFiles []string
}

// StartRun creates a run row and a pending journal entry for each repo, in
// one transaction. The pending entries are what a resume picks up.
func (s *Store) StartRun(ctx context.Context, kind, mode string, repoIDs []string, pid int, host string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, dbError("cannot start run", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO runs(kind, mode, started_at, state, total, host, pid)
		VALUES (?, ?, ?, 'running', ?, ?, ?)`, kind, mode, Now(), len(repoIDs), host, pid)
	if err != nil {
		return 0, dbError("cannot start run", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, dbError("cannot start run", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO run_items(run_id, repo_id, state) VALUES (?, ?, 'pending')`)
	if err != nil {
		return 0, dbError("cannot start run", err)
	}
	defer stmt.Close()
	for _, rid := range repoIDs {
		if _, err := stmt.ExecContext(ctx, id, rid); err != nil {
			return 0, dbError("cannot start run", err)
		}
	}
	return id, tx.Commit()
}

// UnfinishedRun is a sync that was interrupted or crashed.
type UnfinishedRun struct {
	ID        int64
	StartedAt string
	Total     int      // repos the run set out to sync
	Pending   []string // repo IDs not done yet
}

// LastUnfinishedRun returns the latest sync if it did not finish, or nil.
// The archive lock guarantees that a run still marked "running" is dead.
func (s *Store) LastUnfinishedRun(ctx context.Context) (*UnfinishedRun, error) {
	var u UnfinishedRun
	var state string
	err := s.DB.QueryRowContext(ctx, `SELECT id, started_at, state, total FROM runs WHERE kind = 'sync'
		ORDER BY id DESC LIMIT 1`).Scan(&u.ID, &u.StartedAt, &state, &u.Total)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "running" && state != "interrupted") {
		return nil, nil
	}
	if err != nil {
		return nil, dbError("cannot read the last run", err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id FROM run_items WHERE run_id = ? AND state = 'pending'`, u.ID)
	if err != nil {
		return nil, dbError("cannot read the last run", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbError("cannot read the last run", err)
		}
		u.Pending = append(u.Pending, id)
	}
	return &u, rows.Err()
}

// SetRunState changes a run's state, e.g. to "resumed" or "discarded".
func (s *Store) SetRunState(ctx context.Context, runID int64, state string) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE runs SET state = ? WHERE id = ?`, state, runID); err != nil {
		return dbError("cannot update run", err)
	}
	return nil
}

// RecordItem stores one repo's result: journal entry, ref changes and new
// status, atomically, so the journal always matches the repos table.
func (s *Store) RecordItem(ctx context.Context, runID int64, it RunItem) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot record result", err)
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(q string, args ...any) {
		if err == nil {
			_, err = tx.ExecContext(ctx, q, args...)
		}
	}
	exec(`INSERT OR REPLACE INTO run_items(run_id, repo_id, state, outcome, category, message, started_at, finished_at)
		VALUES (?, ?, 'done', ?, ?, ?, ?, ?)`, runID, it.RepoID, it.Outcome, it.Category, it.Reason, it.StartedAt, it.FinishedAt)
	for _, c := range it.Changes {
		exec(`INSERT INTO ref_changes(run_id, repo_id, ref, old_sha, new_sha, commits, kind, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, it.RepoID, c.Ref, nullable(c.OldSHA), nullable(c.NewSHA), c.Commits, c.Kind, it.FinishedAt)
	}
	synced := any(nil)
	if it.Outcome != "failed" && it.Outcome != "skipped" {
		synced = it.FinishedAt
	}
	exec(`UPDATE repos SET status = ?, status_reason = ?, last_synced_at = COALESCE(?, last_synced_at),
		last_upstream_activity_at = CASE WHEN ? THEN ? ELSE last_upstream_activity_at END WHERE id = ?`,
		it.Status, it.Reason, synced, len(it.Changes) > 0, it.FinishedAt, it.RepoID)
	if err == nil && it.Remotes != nil {
		err = setRemotes(ctx, tx, *it.Remotes)
	}
	switch {
	case err != nil:
	case it.Conflict:
		err = setConflict(ctx, tx, it.RepoID, runID, it.ConflictFiles, "", "")
	case it.Outcome == "updated" || it.Outcome == "unchanged":
		// Applied cleanly: whatever clashed before is gone.
		exec(`DELETE FROM conflicts WHERE repo_id = ?`, it.RepoID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return dbError("cannot record result", err)
	}
	return nil
}

// FinishRun closes a run with its final state and outcome counts.
func (s *Store) FinishRun(ctx context.Context, runID int64, state string, counts map[string]int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET finished_at = ?, state = ?, updated = ?, unchanged = ?, skipped = ?, failed = ?
		WHERE id = ?`, Now(), state, counts["updated"], counts["unchanged"], counts["skipped"], counts["failed"], runID)
	if err != nil {
		return dbError(fmt.Sprintf("cannot finish run %d", runID), err)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LastRun returns the most recent sync run, or nil if there has been none.
func (s *Store) LastRun(ctx context.Context) (*RunSummary, error) {
	var r RunSummary
	err := s.DB.QueryRowContext(ctx, `SELECT id, kind, COALESCE(mode,''), state, started_at, COALESCE(finished_at,''),
			total, updated, unchanged, skipped, failed
		FROM runs WHERE kind = 'sync' ORDER BY id DESC LIMIT 1`).Scan(&r.ID, &r.Kind, &r.Mode, &r.State, &r.StartedAt,
		&r.FinishedAt, &r.Total, &r.Updated, &r.Unchanged, &r.Skipped, &r.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError("cannot read the last run", err)
	}
	return &r, nil
}
