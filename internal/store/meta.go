package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// SetRepoMeta stores what the forge said about each repo, by repo id, in
// one transaction. It is a cache, so it is not written to the ledger.
func (s *Store) SetRepoMeta(ctx context.Context, meta map[string]core.RepoMeta) error {
	if len(meta) == 0 {
		return nil
	}
	const msg = "cannot save repo details"
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError(msg, err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO repo_meta(repo_id, description, topics, stars, language,
		license, homepage, fork, updated_at) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM repos WHERE id = ?)`)
	if err != nil {
		return dbError(msg, err)
	}
	defer stmt.Close()
	now := Now()
	for id, m := range meta {
		topics, err := json.Marshal(m.Topics)
		if err != nil {
			return err
		}
		if m.Topics == nil {
			topics = []byte("[]")
		}
		if _, err := stmt.ExecContext(ctx, id, m.Description, string(topics), m.Stars, m.Language, m.License, m.Homepage,
			m.Fork, now, id); err != nil {
			return dbError(msg, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return dbError(msg, err)
	}
	return nil
}

// RepoMeta returns what the forge last said about a repo; ok is false when
// it never said anything (a plain git remote, or never synced with a token).
func (s *Store) RepoMeta(ctx context.Context, repoID string) (m core.RepoMeta, ok bool, err error) {
	all, err := s.repoMeta(ctx, `WHERE repo_id = ?`, repoID)
	if err != nil {
		return m, false, err
	}
	m, ok = all[repoID]
	return m, ok, nil
}

// AllRepoMeta returns every stored row, by repo id.
func (s *Store) AllRepoMeta(ctx context.Context) (map[string]core.RepoMeta, error) {
	return s.repoMeta(ctx, "")
}

func (s *Store) repoMeta(ctx context.Context, where string, args ...any) (map[string]core.RepoMeta, error) {
	const msg = "cannot read repo details"
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id, description, topics, stars, language, license, homepage, fork,
		updated_at FROM repo_meta `+where, args...)
	if err != nil {
		return nil, dbError(msg, err)
	}
	defer rows.Close()
	out := map[string]core.RepoMeta{}
	for rows.Next() {
		var id, topics string
		var m core.RepoMeta
		if err := rows.Scan(&id, &m.Description, &topics, &m.Stars, &m.Language, &m.License, &m.Homepage, &m.Fork,
			&m.UpdatedAt); err != nil {
			return nil, dbError(msg, err)
		}
		if err := json.Unmarshal([]byte(topics), &m.Topics); err != nil {
			return nil, dbError(msg, errors.Join(errors.New("topics are not a JSON list"), err))
		}
		out[id] = m
	}
	return out, rows.Err()
}
