package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Conflict is a repo whose local changes clash with an update: either
// rolled back and waiting (status "conflict") or in a resolve session
// (status "resolving") (PRD F3/F4). RepoInfo fills only the files, backup
// ref and time.
type Conflict struct {
	Repo      string   `json:"path,omitempty"`
	RepoID    string   `json:"id,omitempty"`
	Status    string   `json:"status,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Files     []string `json:"files"`
	BackupRef string   `json:"backup_ref"`
	CreatedAt string   `json:"created_at"`
}

// Conflicts lists pending conflicts, oldest first.
func (s *Store) Conflicts(ctx context.Context) ([]Conflict, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.path, r.id, r.status, COALESCE(r.status_reason,''), c.files, c.backup_ref, c.created_at
		FROM conflicts c JOIN repos r ON r.id = c.repo_id ORDER BY c.created_at, r.path`)
	if err != nil {
		return nil, dbError("cannot read conflicts", err)
	}
	defer rows.Close()
	var out []Conflict
	for rows.Next() {
		var c Conflict
		var files string
		if err := rows.Scan(&c.Repo, &c.RepoID, &c.Status, &c.Reason, &files, &c.BackupRef, &c.CreatedAt); err != nil {
			return nil, dbError("cannot read conflicts", err)
		}
		_ = json.Unmarshal([]byte(files), &c.Files)
		if c.Files == nil {
			c.Files = []string{}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetConflict records that a repo has a conflict, with its new status. A
// nil files list keeps the files already recorded.
func (s *Store) SetConflict(ctx context.Context, repoID, status, reason string, files []string, backupRef, stashMsg string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot record conflict", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := setConflict(ctx, tx, repoID, nil, files, backupRef, stashMsg); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE repos SET status = ?, status_reason = ? WHERE id = ?`, status, reason, repoID)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return dbError("cannot record conflict", err)
	}
	return nil
}

// ClearConflict removes a repo's conflict and sets its new status.
func (s *Store) ClearConflict(ctx context.Context, repoID, status, reason string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot clear conflict", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `DELETE FROM conflicts WHERE repo_id = ?`, repoID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE repos SET status = ?, status_reason = ? WHERE id = ?`, status, reason, repoID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return dbError("cannot clear conflict", err)
	}
	return nil
}

func setConflict(ctx context.Context, tx *sql.Tx, repoID string, runID any, files []string, backupRef, stashMsg string) error {
	var list any
	if files != nil {
		b, _ := json.Marshal(files)
		list = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO conflicts(repo_id, run_id, backup_ref, stash_msg, files, created_at)
		VALUES (?, ?, ?, ?, COALESCE(?, '[]'), ?)
		ON CONFLICT(repo_id) DO UPDATE SET run_id = COALESCE(excluded.run_id, run_id),
			backup_ref = CASE WHEN excluded.backup_ref = '' THEN backup_ref ELSE excluded.backup_ref END,
			stash_msg = CASE WHEN excluded.stash_msg = '' THEN stash_msg ELSE excluded.stash_msg END,
			files = COALESCE(?, files)`, repoID, runID, backupRef, stashMsg, list, Now(), list)
	if err != nil {
		return dbError("cannot record conflict", err)
	}
	return nil
}

// EndConflictSession keeps a repo's conflict listed but forgets the
// session that was open for it, and sets the repo's new status.
func (s *Store) EndConflictSession(ctx context.Context, repoID, status, reason string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot record conflict", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE conflicts SET backup_ref = '', stash_msg = '' WHERE repo_id = ?`, repoID)
	if err == nil {
		err = setConflict(ctx, tx, repoID, nil, nil, "", "")
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE repos SET status = ?, status_reason = ? WHERE id = ?`, status, reason, repoID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return dbError("cannot record conflict", err)
	}
	return nil
}
