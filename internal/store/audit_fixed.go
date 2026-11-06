package store

import "context"

// MarkFixed records which of a check's findings in one repo are gone.
// It is for checks that look at a repo's current state (such as
// dependencies at HEAD), never for history checks such as secrets, where
// a finding stays true forever. Findings in stillSeen are open again if
// they had been marked fixed; every other finding of that check in that
// repo is marked fixed now. Nothing is deleted, so ignores and first-seen
// dates survive a fix and a regression.
func (s *Store) MarkFixed(ctx context.Context, repoID, check string, stillSeen []string) (fixed int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := Now()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS seen_findings (id TEXT PRIMARY KEY)`); err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM seen_findings`); err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	for _, id := range stillSeen {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO seen_findings(id) VALUES (?)`, id); err != nil {
			return 0, dbError("cannot save audit results", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET fixed_at = NULL
		WHERE repo_id = ? AND check_kind = ? AND fixed_at IS NOT NULL AND id IN (SELECT id FROM seen_findings)`, repoID, check); err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE findings SET fixed_at = ?
		WHERE repo_id = ? AND check_kind = ? AND fixed_at IS NULL AND id NOT IN (SELECT id FROM seen_findings)`, now, repoID, check)
	if err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM seen_findings`); err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, dbError("cannot save audit results", err)
	}
	return int(n), nil
}

// fixedClause hides fixed findings unless asked for.
func fixedClause(include bool) string {
	if include {
		return ""
	}
	return ` AND f.fixed_at IS NULL`
}
