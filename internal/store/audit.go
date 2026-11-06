package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
)

// BeginAudit records the start of an audit run and returns its ID.
func (s *Store) BeginAudit(ctx context.Context, checks []string, history string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO audit_runs(started_at, checks, history) VALUES (?, ?, ?)`,
		Now(), strings.Join(checks, ","), history)
	if err != nil {
		return 0, dbError("cannot record audit run", err)
	}
	return res.LastInsertId()
}

// FinishAudit closes an audit run.
func (s *Store) FinishAudit(ctx context.Context, runID int64, repos, newFindings, failed int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE audit_runs SET finished_at = ?, repos = ?, new_findings = ?, failed_repos = ? WHERE id = ?`,
		Now(), repos, newFindings, failed, runID)
	if err != nil {
		return dbError("cannot record audit run", err)
	}
	return nil
}

// LastAudit returns when the last finished audit ended, or "".
func (s *Store) LastAudit(ctx context.Context) (string, error) {
	var at sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(finished_at) FROM audit_runs`).Scan(&at)
	if err != nil {
		return "", dbError("cannot read audit runs", err)
	}
	return at.String, nil
}

// AuditCursor returns the ref tips the last audit of check covered in repo.
func (s *Store) AuditCursor(ctx context.Context, repoID, check string) ([]string, error) {
	var tips string
	err := s.DB.QueryRowContext(ctx, `SELECT tips FROM audit_cursors WHERE repo_id = ? AND check_kind = ?`, repoID, check).Scan(&tips)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError("cannot read audit progress", err)
	}
	return strings.Fields(tips), nil
}

// SaveRepoAudit stores what one check found in one repo, in one
// transaction, and marks each finding New if no earlier audit had it.
// Findings already known keep their oldest location. With tips set, the
// repo's cursor moves forward so the next incremental audit starts there.
func (s *Store) SaveRepoAudit(ctx context.Context, runID int64, repoID, check string, fs []audit.Finding, tips []string) ([]audit.Finding, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbError("cannot save audit results", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := Now()
	out := make([]audit.Finding, len(fs))
	for i, f := range fs {
		var oldDate sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT authored_at FROM findings WHERE id = ?`, f.ID).Scan(&oldDate)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO findings(id, repo_id, check_kind, rule, title, severity, confidence,
				file, line, commit_sha, author, authored_at, preview, hint, occurrences, first_seen_at, last_seen_at, first_run_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				f.ID, repoID, check, f.Rule, f.Title, string(f.Severity), f.Confidence,
				f.File, f.Line, f.Commit, f.Author, f.Date, f.Preview, f.Hint, f.Occurrences, now, now, runID)
			f.New = true
		case err == nil:
			older := f.Date != "" && (!oldDate.Valid || oldDate.String == "" || f.Date < oldDate.String)
			if older {
				_, err = tx.ExecContext(ctx, `UPDATE findings SET file = ?, line = ?, commit_sha = ?, author = ?, authored_at = ?, hint = ?,
					occurrences = occurrences + ?, last_seen_at = ? WHERE id = ?`,
					f.File, f.Line, f.Commit, f.Author, f.Date, f.Hint, f.Occurrences, now, f.ID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE findings SET occurrences = MAX(occurrences, ?), last_seen_at = ? WHERE id = ?`,
					f.Occurrences, now, f.ID)
			}
		}
		if err != nil {
			return nil, dbError("cannot save audit results", err)
		}
		out[i] = f
	}
	if tips != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_cursors(repo_id, check_kind, tips, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(repo_id, check_kind) DO UPDATE SET tips = excluded.tips, updated_at = excluded.updated_at`,
			repoID, check, strings.Join(tips, "\n"), now); err != nil {
			return nil, dbError("cannot save audit progress", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, dbError("cannot save audit results", err)
	}
	// Ignored status comes from the database, so a re-found ignored secret stays quiet.
	ignored, err := s.ignoredSet(ctx, repoID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if reason, ok := ignored[out[i].ID]; ok {
			out[i].Status, out[i].IgnoreNote = audit.StatusIgnored, reason
		}
	}
	return out, nil
}

func (s *Store) ignoredSet(ctx context.Context, repoID string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.finding_id, i.reason FROM finding_ignores i
		JOIN findings f ON f.id = i.finding_id WHERE f.repo_id = ?`, repoID)
	if err != nil {
		return nil, dbError("cannot read ignored findings", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return nil, dbError("cannot read ignored findings", err)
		}
		out[id] = reason
	}
	return out, rows.Err()
}

// FindingFilter narrows Findings. Zero values match everything.
type FindingFilter struct {
	RepoID         string
	Check          string
	MinSeverity    audit.Severity
	IncludeIgnored bool
	SinceRun       int64 // only findings first seen in this run or later
	IncludeFixed   bool  // also findings a later audit no longer saw (dependency checks)
}

// Findings lists stored findings, most severe first.
func (s *Store) Findings(ctx context.Context, f FindingFilter) ([]audit.Finding, error) {
	q := `SELECT f.id, f.repo_id, r.path, f.check_kind, f.rule, f.title, f.severity, f.confidence, f.file,
		COALESCE(f.line, 0), COALESCE(f.commit_sha, ''), COALESCE(f.author, ''), COALESCE(f.authored_at, ''),
		COALESCE(f.preview, ''), COALESCE(f.hint, ''), f.occurrences, f.first_seen_at, f.last_seen_at,
		COALESCE(f.first_run_id, 0), i.reason, COALESCE(f.fixed_at, '')
		FROM findings f JOIN repos r ON r.id = f.repo_id LEFT JOIN finding_ignores i ON i.finding_id = f.id WHERE 1=1`
	var args []any
	if f.RepoID != "" {
		q += ` AND f.repo_id = ?`
		args = append(args, f.RepoID)
	}
	if f.Check != "" {
		q += ` AND f.check_kind = ?`
		args = append(args, f.Check)
	}
	if !f.IncludeIgnored {
		q += ` AND i.finding_id IS NULL`
	}
	q += fixedClause(f.IncludeFixed)
	if f.SinceRun > 0 {
		q += ` AND f.first_run_id >= ?`
		args = append(args, f.SinceRun)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY r.path, f.file, f.id`, args...)
	if err != nil {
		return nil, dbError("cannot read findings", err)
	}
	defer rows.Close()
	var out []audit.Finding
	for rows.Next() {
		var x audit.Finding
		var sev string
		var runID int64
		var reason sql.NullString
		if err := rows.Scan(&x.ID, &x.RepoID, &x.RepoPath, &x.Check, &x.Rule, &x.Title, &sev, &x.Confidence, &x.File,
			&x.Line, &x.Commit, &x.Author, &x.Date, &x.Preview, &x.Hint, &x.Occurrences, &x.FirstSeenAt, &x.LastSeenAt,
			&runID, &reason, &x.FixedAt); err != nil {
			return nil, dbError("cannot read findings", err)
		}
		x.Severity = audit.Severity(sev)
		if f.MinSeverity != "" && x.Severity.Rank() < f.MinSeverity.Rank() {
			continue
		}
		x.Status = audit.StatusOpen
		if reason.Valid {
			x.Status, x.IgnoreNote = audit.StatusIgnored, reason.String
		}
		if x.FixedAt != "" {
			x.Status = audit.StatusFixed
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("cannot read findings", err)
	}
	sortFindings(out)
	return out, nil
}

func sortFindings(fs []audit.Finding) {
	// Most severe first; the query already ordered by repo and file.
	slices.SortStableFunc(fs, func(a, b audit.Finding) int { return b.Severity.Rank() - a.Severity.Rank() })
}

// ResolveFinding finds a finding by its ID or a unique prefix of it (at
// least 4 characters), so users can type the short ID the CLI prints.
func (s *Store) ResolveFinding(ctx context.Context, idOrPrefix string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(idOrPrefix))
	if len(p) < 4 || strings.ContainsAny(p, "%_") {
		return "", &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%q is not a finding ID", idOrPrefix),
			Hint: "use the ID shown by `chitragit audit findings` (at least 4 characters)"}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM findings WHERE id LIKE ? LIMIT 2`, p+"%")
	if err != nil {
		return "", dbError("cannot read findings", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", dbError("cannot read findings", err)
		}
		ids = append(ids, id)
	}
	switch len(ids) {
	case 0:
		return "", &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no finding with ID %q", idOrPrefix)}
	case 1:
		return ids[0], nil
	}
	return "", &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%q matches more than one finding", idOrPrefix),
		Hint: "type more characters of the ID"}
}

// IgnoreFinding marks a finding as accepted, with the user's reason.
func (s *Store) IgnoreFinding(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO finding_ignores(finding_id, reason, created_at) VALUES (?, ?, ?)
		ON CONFLICT(finding_id) DO UPDATE SET reason = excluded.reason`, id, reason, Now())
	if err != nil {
		return dbError("cannot ignore finding", err)
	}
	return nil
}

// UnignoreFinding makes an ignored finding open again.
func (s *Store) UnignoreFinding(ctx context.Context, id string) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM finding_ignores WHERE finding_id = ?`, id); err != nil {
		return dbError("cannot un-ignore finding", err)
	}
	return nil
}
