package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Snapshot kinds.
const (
	SnapshotPre    = "pre"    // before a sync touched the repo
	SnapshotPost   = "post"   // after a sync
	SnapshotManual = "manual" // `chitragit snapshot take`
)

// Snapshot is one repo's full ref state at one moment.
type Snapshot struct {
	ID     int64             `json:"id"`
	RepoID string            `json:"repo_id"`
	Path   string            `json:"path,omitempty"`
	RunID  int64             `json:"run_id,omitempty"`
	At     string            `json:"at"`
	Kind   string            `json:"kind"`
	Label  string            `json:"label,omitempty"`
	Count  int               `json:"refs_count"`
	Refs   map[string]string `json:"refs,omitempty"` // ref -> SHA; filled by Snapshot, not by Snapshots
}

// saveSnapshot stores refs, as read at time at, as a new snapshot of repoID
// unless they equal the repo's last snapshot, in which case that one is
// returned and created is false. Unchanged repos therefore cost one indexed
// read per sync.
func saveSnapshot(ctx context.Context, tx *sql.Tx, repoID string, runID int64, kind, at string, refs map[string]string) (id int64, created bool, err error) {
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE repo_id = ? ORDER BY id DESC LIMIT 1`, repoID).Scan(&last)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, false, dbError("cannot read snapshots", err)
	default:
		old, err := snapshotRefs(ctx, tx, last)
		if err != nil {
			return 0, false, err
		}
		if maps.Equal(old, refs) {
			return last, false, nil
		}
	}
	id, err = insertSnapshot(ctx, tx, repoID, runID, kind, at, refs)
	return id, err == nil, err
}

// insertSnapshot stores refs as a new snapshot, without comparing them
// with the last one.
func insertSnapshot(ctx context.Context, tx *sql.Tx, repoID string, runID int64, kind, at string, refs map[string]string) (int64, error) {
	if at == "" {
		at = Now()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO snapshots(repo_id, run_id, at, kind) VALUES (?, ?, ?, ?)`,
		repoID, nullableID(runID), at, kind)
	if err != nil {
		return 0, dbError("cannot save snapshot", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, dbError("cannot save snapshot", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO snapshot_refs(snapshot_id, ref, sha) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, dbError("cannot save snapshot", err)
	}
	defer stmt.Close()
	for ref, sha := range refs {
		if _, err := stmt.ExecContext(ctx, id, ref, sha); err != nil {
			return 0, dbError("cannot save snapshot", err)
		}
	}
	return id, nil
}

type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func snapshotRefs(ctx context.Context, q rowsQuerier, id int64) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT ref, sha FROM snapshot_refs WHERE snapshot_id = ?`, id)
	if err != nil {
		return nil, dbError("cannot read snapshot", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ref, sha string
		if err := rows.Scan(&ref, &sha); err != nil {
			return nil, dbError("cannot read snapshot", err)
		}
		out[ref] = sha
	}
	return out, rows.Err()
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// TakeSnapshot records refs as a manual snapshot of repoID. When nothing
// changed since the repo's last snapshot, that one is returned with
// created false.
func (s *Store) TakeSnapshot(ctx context.Context, repoID string, refs map[string]string) (id int64, created bool, err error) {
	const msg = "cannot save snapshot"
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, dbError(msg, err)
	}
	defer func() { _ = tx.Rollback() }()
	if id, created, err = saveSnapshot(ctx, tx, repoID, 0, SnapshotManual, Now(), refs); err != nil || !created {
		return id, created, err
	}
	if err := addEvent(ctx, tx, repoID, opPrefix+"snapshot", map[string]int64{"snapshot": id}); err != nil {
		return 0, false, err
	}
	if err := s.commit(ctx, tx); err != nil {
		return 0, false, dbError(msg, err)
	}
	return id, true, nil
}

// Snapshots lists snapshots newest first, for one repo or (repoID "") all.
func (s *Store) Snapshots(ctx context.Context, repoID string, limit int) ([]Snapshot, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT s.id, s.repo_id, COALESCE(r.path, ''), COALESCE(s.run_id, 0), s.at, s.kind, COALESCE(s.label, ''),
		(SELECT COUNT(*) FROM snapshot_refs x WHERE x.snapshot_id = s.id)
		FROM snapshots s LEFT JOIN repos r ON r.id = s.repo_id`
	args := []any{}
	if repoID != "" {
		q += " WHERE s.repo_id = ?"
		args = append(args, repoID)
	}
	rows, err := s.DB.QueryContext(ctx, q+" ORDER BY s.id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, dbError("cannot read snapshots", err)
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		if err := rows.Scan(&sn.ID, &sn.RepoID, &sn.Path, &sn.RunID, &sn.At, &sn.Kind, &sn.Label, &sn.Count); err != nil {
			return nil, dbError("cannot read snapshots", err)
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// Snapshot returns one snapshot with its refs.
func (s *Store) Snapshot(ctx context.Context, id int64) (*Snapshot, error) {
	var sn Snapshot
	err := s.DB.QueryRowContext(ctx, `SELECT s.id, s.repo_id, COALESCE(r.path, ''), COALESCE(s.run_id, 0), s.at, s.kind,
		COALESCE(s.label, '') FROM snapshots s LEFT JOIN repos r ON r.id = s.repo_id WHERE s.id = ?`, id).
		Scan(&sn.ID, &sn.RepoID, &sn.Path, &sn.RunID, &sn.At, &sn.Kind, &sn.Label)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no snapshot %d", id),
			Hint: "list snapshots with `chitragit snapshot ls`"}
	}
	if err != nil {
		return nil, dbError("cannot read snapshot", err)
	}
	if sn.Refs, err = snapshotRefs(ctx, s.DB, id); err != nil {
		return nil, err
	}
	sn.Count = len(sn.Refs)
	return &sn, nil
}

// SnapshotPair is one repo's state before and after a sync run: From is
// the repo's newest snapshot before the run's post snapshot To (0 when the
// run cloned the repo).
type SnapshotPair struct {
	RepoID string `json:"repo_id"`
	Path   string `json:"path"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
}

// RunChanges lists the repos whose refs a sync run changed, by path. A run
// stores a post snapshot only for those; the pre snapshot may be an older
// one, since an unchanged pre is not stored twice.
func (s *Store) RunChanges(ctx context.Context, runID int64) ([]SnapshotPair, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.repo_id, COALESCE(r.path, ''),
		COALESCE((SELECT MAX(p.id) FROM snapshots p WHERE p.repo_id = s.repo_id AND p.id < s.id), 0), s.id
		FROM snapshots s LEFT JOIN repos r ON r.id = s.repo_id
		WHERE s.run_id = ? AND s.kind = ? ORDER BY 2`, runID, SnapshotPost)
	if err != nil {
		return nil, dbError("cannot read snapshots", err)
	}
	defer rows.Close()
	var out []SnapshotPair
	for rows.Next() {
		var p SnapshotPair
		if err := rows.Scan(&p.RepoID, &p.Path, &p.From, &p.To); err != nil {
			return nil, dbError("cannot read snapshots", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NameSnapshot gives refs, repoID's refs now, the name: the repo's newest
// snapshot takes it when it holds exactly these refs and has no name yet,
// else a new manual snapshot is stored with it. A name already used for
// this repo is refused. The caller pins the refs first, as for a snapshot.
func (s *Store) NameSnapshot(ctx context.Context, repoID, name string, refs map[string]string) (id int64, err error) {
	const msg = "cannot name snapshot"
	err = s.writeTx(ctx, msg, "snapshot_name", repoID, map[string]string{"name": name}, func(tx *sql.Tx) error {
		var other int64
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE repo_id = ? AND label = ?`, repoID, name).Scan(&other); {
		case err == nil:
			return &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("the name %q is already used (snapshot %d)", name, other),
				Hint: "pick another name, or remove it first with `chitragit tag -d " + name + "`"}
		case !errors.Is(err, sql.ErrNoRows):
			return dbError(msg, err)
		}
		var label sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, label FROM snapshots WHERE repo_id = ? ORDER BY id DESC LIMIT 1`, repoID).Scan(&id, &label)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return dbError(msg, err)
		case !label.Valid:
			last, err := snapshotRefs(ctx, tx, id)
			if err != nil {
				return err
			}
			if maps.Equal(last, refs) {
				return exec1(ctx, msg, `UPDATE snapshots SET label = ? WHERE id = ?`, name, id)(tx)
			}
		}
		if id, err = insertSnapshot(ctx, tx, repoID, 0, SnapshotManual, Now(), refs); err != nil {
			return err
		}
		return exec1(ctx, msg, `UPDATE snapshots SET label = ? WHERE id = ?`, name, id)(tx)
	})
	return id, err
}

// UnnameSnapshots takes the name off every snapshot that has it and returns
// how many did. The snapshots stay; retention decides about them later.
func (s *Store) UnnameSnapshots(ctx context.Context, name string) (n int64, err error) {
	const msg = "cannot remove the name"
	err = s.writeTx(ctx, msg, "snapshot_unname", "", map[string]string{"name": name}, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE snapshots SET label = NULL WHERE label = ?`, name)
		if err == nil {
			n, err = res.RowsAffected()
		}
		if err != nil {
			return dbError(msg, err)
		}
		return nil
	})
	return n, err
}

// SnapshotName is one name and the snapshots that carry it.
type SnapshotName struct {
	Name  string `json:"name"`
	Repos int    `json:"repos"`
	At    string `json:"at"` // when the newest of them was taken
}

// SnapshotNames lists every name, newest first.
func (s *Store) SnapshotNames(ctx context.Context) ([]SnapshotName, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT label, COUNT(*), MAX(at) FROM snapshots
		WHERE label IS NOT NULL GROUP BY label ORDER BY MAX(at) DESC, label`)
	if err != nil {
		return nil, dbError("cannot read snapshot names", err)
	}
	defer rows.Close()
	var out []SnapshotName
	for rows.Next() {
		var n SnapshotName
		if err := rows.Scan(&n.Name, &n.Repos, &n.At); err != nil {
			return nil, dbError("cannot read snapshot names", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NamedSnapshots returns, by repo id, the snapshot that carries name.
func (s *Store) NamedSnapshots(ctx context.Context, name string) (map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id, id FROM snapshots WHERE label = ?`, name)
	if err != nil {
		return nil, dbError("cannot read snapshot names", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var repo string
		var id int64
		if err := rows.Scan(&repo, &id); err != nil {
			return nil, dbError("cannot read snapshot names", err)
		}
		out[repo] = id
	}
	return out, rows.Err()
}

// SnapshotObjects returns, by repo id, every distinct object that any
// snapshot points at, for verify to find in the repo.
func (s *Store) SnapshotObjects(ctx context.Context) (map[string][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT s.repo_id, x.sha FROM snapshot_refs x
		JOIN snapshots s ON s.id = x.snapshot_id ORDER BY s.repo_id, x.sha`)
	if err != nil {
		return nil, dbError("cannot read snapshots", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var repo, sha string
		if err := rows.Scan(&repo, &sha); err != nil {
			return nil, dbError("cannot read snapshots", err)
		}
		out[repo] = append(out[repo], sha)
	}
	return out, rows.Err()
}

// AllSnapshots returns every snapshot with its refs, oldest first, for
// retention to decide what to keep.
func (s *Store) AllSnapshots(ctx context.Context) ([]Snapshot, error) {
	const msg = "cannot read snapshots"
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, s.repo_id, COALESCE(r.path, ''), COALESCE(s.run_id, 0), s.at, s.kind,
		COALESCE(s.label, ''), x.ref, x.sha
		FROM snapshots s LEFT JOIN repos r ON r.id = s.repo_id LEFT JOIN snapshot_refs x ON x.snapshot_id = s.id
		ORDER BY s.id`)
	if err != nil {
		return nil, dbError(msg, err)
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var ref, sha sql.NullString
		if err := rows.Scan(&sn.ID, &sn.RepoID, &sn.Path, &sn.RunID, &sn.At, &sn.Kind, &sn.Label, &ref, &sha); err != nil {
			return nil, dbError(msg, err)
		}
		if n := len(out); n == 0 || out[n-1].ID != sn.ID {
			sn.Refs = map[string]string{}
			out = append(out, sn)
		}
		if ref.Valid {
			last := &out[len(out)-1]
			last.Refs[ref.String] = sha.String
			last.Count++
		}
	}
	return out, rows.Err()
}

// DeleteSnapshots removes the given snapshots and their refs in one
// transaction, logged as one retention event. It does not touch the pins
// in the repos; retention unpins only after this has committed, so a
// crash in between leaves extra pins, never a snapshot without its objects.
func (s *Store) DeleteSnapshots(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	const msg = "cannot delete snapshots"
	return s.writeTx(ctx, msg, "retention", "", map[string]int{"snapshots": len(ids)}, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `DELETE FROM snapshots WHERE id = ?`)
		if err != nil {
			return dbError(msg, err)
		}
		defer stmt.Close()
		for _, id := range ids {
			if _, err := stmt.ExecContext(ctx, id); err != nil {
				return dbError(msg, err)
			}
		}
		return nil
	})
}
