package store

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
)

// TimelineSnapshot is the timeline kind of a stored snapshot. Ref changes
// keep their own kind (create, update, rewrite, delete) and logged events
// their type (rescued, renamed, locked, unlocked).
const TimelineSnapshot = "snapshot"

// TimelineEntry is one thing that happened to a repo.
type TimelineEntry struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	RunID   int64  `json:"run_id,omitempty"`
	Ref     string `json:"ref,omitempty"`
	OldSHA  string `json:"old,omitempty"`
	NewSHA  string `json:"new,omitempty"`
	Commits int    `json:"commits,omitempty"`
	// Snapshot and SnapshotKind (pre, post, manual) are set on snapshots.
	Snapshot     int64  `json:"snapshot,omitempty"`
	SnapshotKind string `json:"snapshot_kind,omitempty"`
	Message      string `json:"message,omitempty"`

	rank, seq int64 // tie-breakers for entries with the same time
}

// Timeline returns a repo's history, newest first: every ref a sync moved,
// every snapshot and every logged event such as a rescue. Within one
// moment a pre snapshot comes before the ref changes and a post snapshot
// after them.
func (s *Store) Timeline(ctx context.Context, repoID string, limit int) ([]TimelineEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	const msg = "cannot read repo history"
	var out []TimelineEntry
	read := func(q string, scan func(*TimelineEntry, func(...any) error) error) error {
		rows, err := s.DB.QueryContext(ctx, q, repoID, limit)
		if err != nil {
			return dbError(msg, err)
		}
		defer rows.Close()
		for rows.Next() {
			var e TimelineEntry
			if err := scan(&e, rows.Scan); err != nil {
				return dbError(msg, err)
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return dbError(msg, err)
		}
		return nil
	}

	// Each source contributes at most limit rows, so the newest limit of
	// the union are among them.
	err := read(`SELECT id, at, COALESCE(run_id,0), ref, COALESCE(old_sha,''), COALESCE(new_sha,''), COALESCE(commits,0), kind
		FROM ref_changes WHERE repo_id = ? ORDER BY id DESC LIMIT ?`,
		func(e *TimelineEntry, scan func(...any) error) error {
			e.rank = 1
			return scan(&e.seq, &e.At, &e.RunID, &e.Ref, &e.OldSHA, &e.NewSHA, &e.Commits, &e.Kind)
		})
	if err == nil {
		err = read(`SELECT id, at, COALESCE(run_id,0), kind FROM snapshots WHERE repo_id = ? ORDER BY id DESC LIMIT ?`,
			func(e *TimelineEntry, scan func(...any) error) error {
				if err := scan(&e.Snapshot, &e.At, &e.RunID, &e.SnapshotKind); err != nil {
					return err
				}
				e.Kind, e.seq = TimelineSnapshot, e.Snapshot
				switch e.SnapshotKind {
				case SnapshotPre:
					e.rank = 0
				case SnapshotPost:
					e.rank = 2
				default:
					e.rank = 1
				}
				return nil
			})
	}
	if err == nil {
		err = read(`SELECT seq, at, type, COALESCE(payload,'') FROM events
			WHERE repo_id = ? AND type NOT LIKE '`+opPrefix+`%' ORDER BY seq DESC LIMIT ?`,
			func(e *TimelineEntry, scan func(...any) error) error {
				var payload string
				if err := scan(&e.seq, &e.At, &e.Kind, &payload); err != nil {
					return err
				}
				var p struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal([]byte(payload), &p)
				e.Message, e.rank = p.Message, 1
				return nil
			})
	}
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(out, func(x, y TimelineEntry) int {
		return cmp.Or(compareTimes(y.At, x.At), cmp.Compare(y.rank, x.rank), cmp.Compare(y.seq, x.seq))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
