package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// This file holds the read models behind the Repo Detail page, the
// Activity Feed and the Health Dashboard (PRD F8–F10), plus the small
// writes they offer: favourites and tags. None of it touches a repo on
// disk, so none of it needs the archive lock.

// RepoInfo is everything the database knows about one repo.
type RepoInfo struct {
	core.Repo
	DefaultBranch          string            `json:"default_branch,omitempty"`
	SizeBytes              int64             `json:"size_bytes,omitempty"`
	FirstSeenAt            string            `json:"first_seen_at"`
	LastScannedAt          string            `json:"last_scanned_at,omitempty"`
	LastUpstreamActivityAt string            `json:"last_upstream_activity_at,omitempty"`
	Remotes                map[string]string `json:"remotes"`
	Tags                   []string          `json:"tags"`
	Rescue                 *Rescue           `json:"rescue,omitempty"`
	Conflict               *Conflict         `json:"conflict,omitempty"`
}

// Rescue is the guardian's record of a repo whose upstream went away
// (PRD F5).
type Rescue struct {
	RepoID        string `json:"repo_id"`
	Reason        string `json:"reason"` // gone | legal | not_found
	Detail        string `json:"detail,omitempty"`
	Confirmations int    `json:"confirmations"`
	FirstFailedAt string `json:"first_failed_at"`
	RescuedAt     string `json:"rescued_at,omitempty"` // empty until confirmed
	BundlePath    string `json:"bundle_path,omitempty"`
	BundledAt     string `json:"bundled_at,omitempty"`
}

// Conflict is a pending stash conflict (PRD F3/F4).
type Conflict struct {
	BackupRef string   `json:"backup_ref"`
	Files     []string `json:"files"`
	CreatedAt string   `json:"created_at"`
}

// ErrNoRepo means no repo matched a lookup.
var ErrNoRepo = &core.Error{Category: core.CatNotFound, Message: "no repo matches",
	Hint: "run `chitragit ls` to see repo paths"}

// RepoInfo returns the full record of one repo.
func (s *Store) RepoInfo(ctx context.Context, id string) (*RepoInfo, error) {
	var ri RepoInfo
	var size sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT `+repoColumns+`, COALESCE(default_branch,''), size_bytes,
		first_seen_at, COALESCE(last_scanned_at,''), COALESCE(last_upstream_activity_at,'')`+repoFrom+`
		WHERE repos.id = ?`, id).Scan(&ri.ID, &ri.Path, &ri.Kind, &ri.Provider, &ri.Host, &ri.Owner, &ri.Name,
		&ri.Status, &ri.StatusReason, &ri.Favourite, &ri.Missing, &ri.LastSyncedAt,
		&ri.Locked, &ri.Rescued, &ri.RescueReason, &ri.ArchivedUpstream,
		&ri.DefaultBranch, &size, &ri.FirstSeenAt, &ri.LastScannedAt, &ri.LastUpstreamActivityAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoRepo
	}
	if err != nil {
		return nil, dbError("cannot read repo", err)
	}
	ri.SizeBytes = size.Int64

	ri.Remotes = map[string]string{}
	rows, err := s.DB.QueryContext(ctx, `SELECT name, url FROM remotes WHERE repo_id = ?`, id)
	if err != nil {
		return nil, dbError("cannot read remotes", err)
	}
	for rows.Next() {
		var n, u string
		if err := rows.Scan(&n, &u); err != nil {
			rows.Close()
			return nil, dbError("cannot read remotes", err)
		}
		ri.Remotes[n] = u
	}
	rows.Close()

	if ri.Tags, err = s.Tags(ctx, id); err != nil {
		return nil, err
	}

	switch rs, err := rescue(ctx, s.DB, id); {
	case err == nil:
		ri.Rescue = &rs
	case !errors.Is(err, sql.ErrNoRows):
		return nil, dbError("cannot read rescue state", err)
	}

	var c Conflict
	var files string
	err = s.DB.QueryRowContext(ctx, `SELECT backup_ref, files, created_at FROM conflicts WHERE repo_id = ?`, id).
		Scan(&c.BackupRef, &files, &c.CreatedAt)
	switch {
	case err == nil:
		_ = json.Unmarshal([]byte(files), &c.Files) // a malformed list still shows the conflict
		ri.Conflict = &c
	case !errors.Is(err, sql.ErrNoRows):
		return nil, dbError("cannot read conflict state", err)
	}
	return &ri, nil
}

// FindRepo resolves what a user typed to one repo: an exact path, an
// owner/name pair, an ID (or a unique ID prefix of 8+ characters), or a
// unique path suffix such as the folder name. Ambiguous input is an error
// that lists the candidates.
func (s *Store) FindRepo(ctx context.Context, query string) (core.Repo, error) {
	q := strings.Trim(strings.ReplaceAll(query, "\\", "/"), "/")
	repos, err := s.Repos(ctx)
	if err != nil {
		return core.Repo{}, err
	}
	matchers := []func(core.Repo) bool{
		func(r core.Repo) bool { return r.Path == q || r.ID == q },
		func(r core.Repo) bool { return r.Owner != "" && strings.EqualFold(r.Owner+"/"+r.Name, q) },
		func(r core.Repo) bool { return len(q) >= 8 && strings.HasPrefix(r.ID, q) },
		func(r core.Repo) bool { return strings.HasSuffix(r.Path, "/"+q) },
	}
	for _, m := range matchers {
		var hits []core.Repo
		for _, r := range repos {
			if m(r) {
				hits = append(hits, r)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil
		}
		paths := make([]string, 0, len(hits))
		for _, h := range hits {
			paths = append(paths, h.Path)
		}
		slices.Sort(paths)
		return core.Repo{}, &core.Error{Category: core.CatConfig, Message: "\"" + query + "\" matches more than one repo",
			Detail: strings.Join(paths, "\n"), Hint: "use the full path"}
	}
	return core.Repo{}, &core.Error{Category: ErrNoRepo.Category, Message: "no repo matches \"" + query + "\"", Hint: ErrNoRepo.Hint}
}

// SetFavourite marks or unmarks repos as favourites.
func (s *Store) SetFavourite(ctx context.Context, ids []string, fav bool) error {
	return s.eachID(ctx, ids, "cannot update favourites", `UPDATE repos SET favourite = ? WHERE id = ?`, fav)
}

// AddTags adds every tag to every repo. Tags are trimmed and lowercased;
// empty tags are ignored.
func (s *Store) AddTags(ctx context.Context, ids, tags []string) error {
	return s.tagEach(ctx, ids, tags, `INSERT OR IGNORE INTO tags(repo_id, tag) VALUES (?, ?)`)
}

// RemoveTags removes every tag from every repo.
func (s *Store) RemoveTags(ctx context.Context, ids, tags []string) error {
	return s.tagEach(ctx, ids, tags, `DELETE FROM tags WHERE repo_id = ? AND tag = ?`)
}

// NormalizeTag is the form tags are stored in.
func NormalizeTag(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

func (s *Store) tagEach(ctx context.Context, ids, tags []string, q string) error {
	var clean []string
	for _, t := range tags {
		if t = NormalizeTag(t); t != "" {
			clean = append(clean, t)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError("cannot update tags", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		for _, t := range clean {
			if _, err := tx.ExecContext(ctx, q, id, t); err != nil {
				return dbError("cannot update tags", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return dbError("cannot update tags", err)
	}
	return nil
}

func (s *Store) eachID(ctx context.Context, ids []string, msg, q string, v any) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return dbError(msg, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, q, v, id); err != nil {
			return dbError(msg, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return dbError(msg, err)
	}
	return nil
}

// Tags returns one repo's tags, sorted.
func (s *Store) Tags(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tag FROM tags WHERE repo_id = ? ORDER BY tag`, id)
	if err != nil {
		return nil, dbError("cannot read tags", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, dbError("cannot read tags", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagCount is a tag and how many repos carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Repos int    `json:"repos"`
}

// AllTags lists every tag in use, most used first.
func (s *Store) AllTags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tag, COUNT(*) FROM tags GROUP BY tag ORDER BY COUNT(*) DESC, tag`)
	if err != nil {
		return nil, dbError("cannot read tags", err)
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Repos); err != nil {
			return nil, dbError("cannot read tags", err)
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// RepoTags maps repo ID to its tags, for filtering a whole list at once.
func (s *Store) RepoTags(ctx context.Context) (map[string][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT repo_id, tag FROM tags ORDER BY repo_id, tag`)
	if err != nil {
		return nil, dbError("cannot read tags", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, dbError("cannot read tags", err)
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// SetSize stores a repo's size on disk, as measured by the engine.
func (s *Store) SetSize(ctx context.Context, id string, bytes int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE repos SET size_bytes = ? WHERE id = ?`, bytes, id); err != nil {
		return dbError("cannot save repo size", err)
	}
	return nil
}

// AddEvent appends to the event log. Events that are not ref changes
// (rescues, renames, conflicts…) appear in the activity feed through it.
// payload is stored as JSON; a "message" string field is shown in the feed.
func (s *Store) AddEvent(ctx context.Context, repoID, typ string, payload any) error {
	return addEvent(ctx, s.DB, repoID, typ, payload)
}

// ---- Activity feed (PRD F9) ----

// Activity types. "refs" is a sync that moved refs; every other type comes
// from the event log as written by AddEvent.
const ActivityRefs = "refs"

// ActivityFilter narrows the activity feed. Zero values mean "any".
type ActivityFilter struct {
	Since  time.Time
	RepoID string
	Owner  string
	Tag    string
	Types  []string
	Limit  int // default 200
}

// Activity is one line in the feed: everything one run changed in one
// repo, or one logged event.
type Activity struct {
	Type    string           `json:"type"`
	At      string           `json:"at"`
	RepoID  string           `json:"repo_id,omitempty"`
	Path    string           `json:"path,omitempty"`
	Owner   string           `json:"owner,omitempty"`
	Name    string           `json:"name,omitempty"`
	RunID   int64            `json:"run_id,omitempty"`
	Changes []core.RefChange `json:"changes,omitempty"`
	Message string           `json:"message,omitempty"`
}

// Activity returns the feed, newest first. It reads only the database, so
// it works offline.
func (s *Store) Activity(ctx context.Context, f ActivityFilter) ([]Activity, error) {
	if f.Limit <= 0 {
		f.Limit = 200
	}
	wantType := func(t string) bool { return len(f.Types) == 0 || slices.Contains(f.Types, t) }
	var where []string
	var args []any
	if !f.Since.IsZero() {
		where = append(where, "x.at >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if f.RepoID != "" {
		where = append(where, "x.repo_id = ?")
		args = append(args, f.RepoID)
	}
	if f.Owner != "" {
		where = append(where, "r.owner = ? COLLATE NOCASE")
		args = append(args, f.Owner)
	}
	if f.Tag != "" {
		where = append(where, "x.repo_id IN (SELECT repo_id FROM tags WHERE tag = ?)")
		args = append(args, NormalizeTag(f.Tag))
	}
	cond := ""
	if len(where) > 0 {
		cond = " AND " + strings.Join(where, " AND ")
	}

	var out []Activity
	if wantType(ActivityRefs) {
		// Group a run's changes per repo. The limit applies to groups, so the
		// inner query picks the newest (run, repo) pairs first.
		q := `SELECT x.run_id, x.repo_id, r.path, COALESCE(r.owner,''), COALESCE(r.name,''),
				x.ref, COALESCE(x.old_sha,''), COALESCE(x.new_sha,''), COALESCE(x.commits,0), x.kind, x.at
			FROM ref_changes x JOIN repos r ON r.id = x.repo_id
			WHERE (COALESCE(x.run_id,-x.id), x.repo_id) IN (
				SELECT COALESCE(x.run_id,-x.id), x.repo_id FROM ref_changes x JOIN repos r ON r.id = x.repo_id
				WHERE 1` + cond + `
				GROUP BY COALESCE(x.run_id,-x.id), x.repo_id ORDER BY MAX(x.at) DESC LIMIT ?)
			ORDER BY x.at DESC, x.id`
		rows, err := s.DB.QueryContext(ctx, q, append(slices.Clone(args), f.Limit)...)
		if err != nil {
			return nil, dbError("cannot read activity", err)
		}
		idx := map[[2]string]int{}
		for rows.Next() {
			var a Activity
			var runID sql.NullInt64
			var c core.RefChange
			if err := rows.Scan(&runID, &a.RepoID, &a.Path, &a.Owner, &a.Name,
				&c.Ref, &c.OldSHA, &c.NewSHA, &c.Commits, &c.Kind, &a.At); err != nil {
				rows.Close()
				return nil, dbError("cannot read activity", err)
			}
			key := [2]string{a.At, a.RepoID}
			if runID.Valid {
				key[0] = "run" + strconv.FormatInt(runID.Int64, 10)
			}
			if i, ok := idx[key]; ok {
				out[i].Changes = append(out[i].Changes, c)
				continue
			}
			a.Type, a.RunID, a.Changes = ActivityRefs, runID.Int64, []core.RefChange{c}
			idx[key] = len(out)
			out = append(out, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, dbError("cannot read activity", err)
		}
	}

	evCond := cond
	evArgs := slices.Clone(args)
	if len(f.Types) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(f.Types)), ",")
		evCond += " AND x.type IN (" + ph + ")"
		for _, t := range f.Types {
			evArgs = append(evArgs, t)
		}
	}
	if len(f.Types) == 0 || slices.ContainsFunc(f.Types, func(t string) bool { return t != ActivityRefs }) {
		rows, err := s.DB.QueryContext(ctx, `SELECT x.type, x.at, COALESCE(x.repo_id,''), COALESCE(r.path,''),
				COALESCE(r.owner,''), COALESCE(r.name,''), COALESCE(x.payload,'')
			FROM events x LEFT JOIN repos r ON r.id = x.repo_id
			WHERE 1`+evCond+` ORDER BY x.seq DESC LIMIT ?`, append(evArgs, f.Limit)...)
		if err != nil {
			return nil, dbError("cannot read activity", err)
		}
		for rows.Next() {
			var a Activity
			var payload string
			if err := rows.Scan(&a.Type, &a.At, &a.RepoID, &a.Path, &a.Owner, &a.Name, &payload); err != nil {
				rows.Close()
				return nil, dbError("cannot read activity", err)
			}
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(payload), &p)
			a.Message = p.Message
			out = append(out, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, dbError("cannot read activity", err)
		}
	}

	slices.SortStableFunc(out, func(x, y Activity) int { return compareTimes(y.At, x.At) })
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// compareTimes orders stored timestamps. RFC 3339 with variable fractions
// does not sort as text ("05Z" vs "05.1Z"), so parse when possible.
func compareTimes(a, b string) int {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	if ea != nil || eb != nil {
		return strings.Compare(a, b)
	}
	return ta.Compare(tb)
}

// RepoRun is one repo's entry in a past run, for the detail page.
type RepoRun struct {
	RunID      int64  `json:"run_id"`
	Mode       string `json:"mode,omitempty"`
	Outcome    string `json:"outcome"`
	Category   string `json:"category,omitempty"`
	Message    string `json:"message,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// RepoHistory returns a repo's sync history, newest first.
func (s *Store) RepoHistory(ctx context.Context, id string, limit int) ([]RepoRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT i.run_id, COALESCE(r.mode,''), COALESCE(i.outcome,''), COALESCE(i.category,''),
			COALESCE(i.message,''), COALESCE(i.finished_at,'')
		FROM run_items i JOIN runs r ON r.id = i.run_id
		WHERE i.repo_id = ? AND i.state = 'done' ORDER BY i.run_id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, dbError("cannot read repo history", err)
	}
	defer rows.Close()
	var out []RepoRun
	for rows.Next() {
		var r RepoRun
		if err := rows.Scan(&r.RunID, &r.Mode, &r.Outcome, &r.Category, &r.Message, &r.FinishedAt); err != nil {
			return nil, dbError("cannot read repo history", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Health dashboard (PRD F10) ----

// Health cards. Each is also a filter for the repo browser (RepoIDs).
const (
	CardRescued    = "rescued"
	CardFailing    = "failing"
	CardConflicts  = "conflicts"
	CardDiverged   = "diverged"
	CardRewritten  = "rewritten"
	CardDirty      = "dirty"
	CardStale      = "stale"
	CardNeverSync  = "never_synced"
	CardMissing    = "missing"
	CardLocked     = "locked"
	CardFavourites = "favourites"
)

// Cards lists the health cards in display order.
var Cards = []string{CardRescued, CardFailing, CardConflicts, CardDiverged, CardRewritten, CardDirty,
	CardStale, CardNeverSync, CardMissing, CardLocked, CardFavourites}

// cardWhere is the SQL condition (on repos r) for each card. "now" and
// "stale" cutoffs are bound as ?1-style named args by Health.
var cardWhere = map[string]string{
	CardRescued:    `r.id IN (SELECT repo_id FROM rescues WHERE rescued_at IS NOT NULL)`,
	CardFailing:    `r.missing = 0 AND r.status = 'failed'`,
	CardConflicts:  `r.missing = 0 AND (r.status = 'conflict' OR r.id IN (SELECT repo_id FROM conflicts))`,
	CardDiverged:   `r.missing = 0 AND r.status = 'diverged'`,
	CardRewritten:  `r.missing = 0 AND r.status = 'rewritten'`,
	CardDirty:      `r.missing = 0 AND r.status = 'dirty'`,
	CardStale:      `r.missing = 0 AND r.last_synced_at IS NOT NULL AND r.id IN (SELECT repo_id FROM remotes) AND COALESCE(r.last_upstream_activity_at, r.first_seen_at) < :stale`,
	CardNeverSync:  `r.missing = 0 AND r.last_synced_at IS NULL AND r.kind <> 'worktree' AND r.id IN (SELECT repo_id FROM remotes)`,
	CardMissing:    `r.missing = 1`,
	CardLocked:     `r.locked = 1`,
	CardFavourites: `r.favourite = 1`,
}

// RunSummary is one past run.
type RunSummary struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Mode       string `json:"mode,omitempty"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Total      int    `json:"total"`
	Updated    int    `json:"updated"`
	Unchanged  int    `json:"unchanged"`
	Skipped    int    `json:"skipped"`
	Failed     int    `json:"failed"`
}

// RepoNote is a repo with a short note, e.g. its last error or its size.
type RepoNote struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Category  string `json:"category,omitempty"`
	Reason    string `json:"reason,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// Health is the dashboard's data.
type Health struct {
	Total     int            `json:"total"`
	Cards     map[string]int `json:"cards"`
	Failing   []RepoNote     `json:"failing,omitempty"` // with last error, newest failures first
	DiskTotal int64          `json:"disk_total_bytes"`
	Measured  int            `json:"disk_measured"` // repos with a known size
	Largest   []RepoNote     `json:"largest,omitempty"`
	Runs      []RunSummary   `json:"runs,omitempty"` // newest first; Runs[0] is the last run
}

// Health computes the dashboard. staleAfter is guardian.stale_after.
func (s *Store) Health(ctx context.Context, staleAfter time.Duration) (*Health, error) {
	h := &Health{Cards: map[string]int{}}
	stale := sql.Named("stale", time.Now().Add(-staleAfter).UTC().Format(time.RFC3339Nano))
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COUNT(size_bytes)
		FROM repos WHERE missing = 0`).Scan(&h.Total, &h.DiskTotal, &h.Measured); err != nil {
		return nil, dbError("cannot read health", err)
	}
	for _, c := range Cards {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM repos r WHERE `+cardWhere[c], stale).Scan(&n); err != nil {
			return nil, dbError("cannot read health", err)
		}
		h.Cards[c] = n
	}

	notes := func(q string, args ...any) ([]RepoNote, error) {
		rows, err := s.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, dbError("cannot read health", err)
		}
		defer rows.Close()
		var out []RepoNote
		for rows.Next() {
			var n RepoNote
			if err := rows.Scan(&n.ID, &n.Path, &n.Category, &n.Reason, &n.SizeBytes); err != nil {
				return nil, dbError("cannot read health", err)
			}
			out = append(out, n)
		}
		return out, rows.Err()
	}
	var err error
	h.Failing, err = notes(`SELECT r.id, r.path,
			COALESCE((SELECT category FROM run_items i WHERE i.repo_id = r.id AND i.state = 'done' ORDER BY i.run_id DESC LIMIT 1),''),
			COALESCE(r.status_reason,''), COALESCE(r.size_bytes,0)
		FROM repos r WHERE ` + cardWhere[CardFailing] + ` ORDER BY r.path LIMIT 20`)
	if err != nil {
		return nil, err
	}
	h.Largest, err = notes(`SELECT r.id, r.path, '', '', r.size_bytes FROM repos r
		WHERE r.missing = 0 AND r.size_bytes IS NOT NULL ORDER BY r.size_bytes DESC, r.path LIMIT 20`)
	if err != nil {
		return nil, err
	}
	h.Runs, err = s.Runs(ctx, 10)
	return h, err
}

// RepoIDs returns the repos behind one health card, for a filtered view.
func (s *Store) RepoIDs(ctx context.Context, card string, staleAfter time.Duration) ([]string, error) {
	w, ok := cardWhere[card]
	if !ok {
		return nil, &core.Error{Category: core.CatConfig, Message: "unknown health card " + card,
			Hint: "one of: " + strings.Join(Cards, ", ")}
	}
	stale := sql.Named("stale", time.Now().Add(-staleAfter).UTC().Format(time.RFC3339Nano))
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id FROM repos r WHERE `+w+` ORDER BY r.path`, stale)
	if err != nil {
		return nil, dbError("cannot read repos", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbError("cannot read repos", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Runs returns the latest runs, newest first.
func (s *Store) Runs(ctx context.Context, limit int) ([]RunSummary, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, COALESCE(mode,''), state, started_at, COALESCE(finished_at,''),
			total, updated, unchanged, skipped, failed
		FROM runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, dbError("cannot read runs", err)
	}
	defer rows.Close()
	var out []RunSummary
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(&r.ID, &r.Kind, &r.Mode, &r.State, &r.StartedAt, &r.FinishedAt,
			&r.Total, &r.Updated, &r.Unchanged, &r.Skipped, &r.Failed); err != nil {
			return nil, dbError("cannot read runs", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
