package store

import (
	"context"
	"database/sql"
	"errors"
)

// ShieldVerdict is KodeKavach Shield's judgement of one repo's HEAD
// (CG-42.17). Verdict "block" holds the repo on later syncs.
type ShieldVerdict struct {
	RepoID   string `json:"repo_id"`
	Commit   string `json:"commit"`
	Verdict  string `json:"verdict"`
	Findings int    `json:"findings"`
	Reason   string `json:"reason"`
	Report   string `json:"report"` // JSON list of findings
	At       string `json:"at"`
}

// ShieldVerdicts returns every repo's verdict by repo id.
func (s *Store) ShieldVerdicts(ctx context.Context) (map[string]ShieldVerdict, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id, commit_sha, verdict, findings, reason, report, at FROM shield_verdicts`)
	if err != nil {
		return nil, dbError("cannot read Shield verdicts", err)
	}
	defer rows.Close()
	out := map[string]ShieldVerdict{}
	for rows.Next() {
		var v ShieldVerdict
		if err := rows.Scan(&v.RepoID, &v.Commit, &v.Verdict, &v.Findings, &v.Reason, &v.Report, &v.At); err != nil {
			return nil, dbError("cannot read Shield verdicts", err)
		}
		out[v.RepoID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("cannot read Shield verdicts", err)
	}
	return out, nil
}

// ShieldVerdictOf returns one repo's verdict, or nil.
func (s *Store) ShieldVerdictOf(ctx context.Context, repoID string) (*ShieldVerdict, error) {
	v := ShieldVerdict{RepoID: repoID}
	err := s.DB.QueryRowContext(ctx, `SELECT commit_sha, verdict, findings, reason, report, at FROM shield_verdicts WHERE repo_id = ?`,
		repoID).Scan(&v.Commit, &v.Verdict, &v.Findings, &v.Reason, &v.Report, &v.At)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError("cannot read a Shield verdict", err)
	}
	return &v, nil
}

// SetShieldVerdict saves a repo's verdict. A block also marks the repo
// blocked with the reason and adds event (with payload) to the ledger, in
// the same transaction, so a verdict is never saved without its trace.
func (s *Store) SetShieldVerdict(ctx context.Context, v ShieldVerdict, event string, payload any) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot save a Shield verdict", err)
	}
	defer func() { _ = tx.Rollback() }()
	if v.At == "" {
		v.At = Now()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO shield_verdicts(repo_id, commit_sha, verdict, findings, reason, report, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET commit_sha = excluded.commit_sha, verdict = excluded.verdict,
			findings = excluded.findings, reason = excluded.reason, report = excluded.report, at = excluded.at`,
		v.RepoID, v.Commit, v.Verdict, v.Findings, v.Reason, v.Report, v.At); err != nil {
		return dbError("cannot save a Shield verdict", err)
	}
	if v.Verdict == "block" {
		if _, err := tx.ExecContext(ctx, `UPDATE repos SET status = 'blocked', status_reason = ? WHERE id = ?`, v.Reason, v.RepoID); err != nil {
			return dbError("cannot save a Shield verdict", err)
		}
	}
	if event != "" {
		if err := addEvent(ctx, tx, v.RepoID, event, payload); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return dbError("cannot save a Shield verdict", err)
	}
	return nil
}
