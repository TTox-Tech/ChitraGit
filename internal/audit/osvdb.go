package audit

import (
	"archive/zip"
	"context"
	"crypto/md5" //nolint:gosec // only to compare with the checksum the server publishes
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver
)

// The offline vulnerability database is a copy of the OSV database
// (https://osv.dev), one export per ecosystem, turned into a small SQLite
// index on this machine. Downloading it sends nothing about the archive:
// the same files are fetched whatever the repos hold. Matching happens
// locally, so no package list ever leaves the machine.

// OSVBaseURL serves the per-ecosystem exports as <ecosystem>/all.zip.
const OSVBaseURL = "https://osv-vulnerabilities.storage.googleapis.com"

const (
	osvDBFile        = "osv.sqlite"
	maxExportSize    = 2 << 30  // 2 GiB, far above the largest export
	maxRecordSize    = 16 << 20 // one advisory
	osvSchemaVersion = 1
)

// ErrNoVulnDB means the vulnerability database has not been downloaded.
var ErrNoVulnDB = errors.New("the vulnerability database has not been downloaded yet")

// VulnDB is the local vulnerability index. It is safe for concurrent use.
type VulnDB struct {
	db   *sql.DB
	path string

	mu    sync.Mutex
	cache map[string][]advisory // ecosystem + "\x00" + name
	has   map[string]bool
}

// EcosystemStatus describes one ecosystem's part of the database.
type EcosystemStatus struct {
	Ecosystem  string `json:"ecosystem"`
	Advisories int    `json:"advisories"`
	UpdatedAt  string `json:"updated_at"` // RFC 3339
}

// advisory is what the index keeps of one OSV record for one package.
type advisory struct {
	ID       string     `json:"id"`
	Summary  string     `json:"summary,omitempty"`
	Aliases  []string   `json:"aliases,omitempty"`
	Severity Severity   `json:"severity"`
	Score    float64    `json:"score,omitempty"` // CVSS base score, when known
	Ranges   []osvRange `json:"ranges,omitempty"`
	Versions []string   `json:"versions,omitempty"`
}

type osvRange struct {
	Type   string     `json:"type"`
	Events []osvEvent `json:"events"`
}

type osvEvent struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
	Limit        string `json:"limit,omitempty"`
}

// VulnDBPath is where the database lives inside dir.
func VulnDBPath(dir string) string { return filepath.Join(dir, osvDBFile) }

// OpenVulnDB opens the database in dir, creating an empty one if needed.
func OpenVulnDB(dir string) (*VulnDB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	p := VulnDBPath(dir)
	dsn := "file:" + p + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ecosystems (
			ecosystem  TEXT PRIMARY KEY,
			advisories INTEGER NOT NULL,
			source_md5 TEXT,
			updated_at TEXT NOT NULL,
			schema     INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS advisories (
			ecosystem TEXT NOT NULL,
			name      TEXT NOT NULL,
			data      TEXT NOT NULL,        -- JSON array of advisories
			PRIMARY KEY (ecosystem, name)
		) WITHOUT ROWID;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("open vulnerability database %s: %w", p, err)
	}
	return &VulnDB{db: db, path: p, cache: map[string][]advisory{}, has: map[string]bool{}}, nil
}

// Close closes the database.
func (v *VulnDB) Close() error { return v.db.Close() }

// Path is the database file.
func (v *VulnDB) Path() string { return v.path }

// Status lists the ecosystems downloaded so far.
func (v *VulnDB) Status(ctx context.Context) ([]EcosystemStatus, error) {
	rows, err := v.db.QueryContext(ctx, `SELECT ecosystem, advisories, updated_at FROM ecosystems WHERE schema = ? ORDER BY ecosystem`, osvSchemaVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EcosystemStatus
	for rows.Next() {
		var s EcosystemStatus
		if err := rows.Scan(&s.Ecosystem, &s.Advisories, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Has reports whether an ecosystem has been downloaded.
func (v *VulnDB) Has(ctx context.Context, eco string) (bool, error) {
	v.mu.Lock()
	ok, known := v.has[eco]
	v.mu.Unlock()
	if known {
		return ok, nil
	}
	var n int
	if err := v.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ecosystems WHERE ecosystem = ? AND schema = ?`, eco, osvSchemaVersion).Scan(&n); err != nil {
		return false, err
	}
	v.mu.Lock()
	v.has[eco] = n > 0
	v.mu.Unlock()
	return n > 0, nil
}

// lookup returns the advisories for one package.
func (v *VulnDB) lookup(ctx context.Context, eco, name string) ([]advisory, error) {
	key := eco + "\x00" + normalizeName(eco, name)
	v.mu.Lock()
	if a, ok := v.cache[key]; ok {
		v.mu.Unlock()
		return a, nil
	}
	v.mu.Unlock()
	var data string
	err := v.db.QueryRowContext(ctx, `SELECT data FROM advisories WHERE ecosystem = ? AND name = ?`, eco, normalizeName(eco, name)).Scan(&data)
	var out []advisory
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal([]byte(data), &out); err != nil {
			return nil, fmt.Errorf("vulnerability database entry for %s %s is damaged: %w", eco, name, err)
		}
	}
	v.mu.Lock()
	v.cache[key] = out
	v.mu.Unlock()
	return out, nil
}

// UpdateProgress reports a download: done and total bytes (total is -1
// when unknown), and the stage, "download" or "index".
type UpdateProgress func(eco, stage string, done, total int64)

// UpdateResult is what one ecosystem's update did.
type UpdateResult struct {
	Ecosystem  string `json:"ecosystem"`
	Advisories int    `json:"advisories"`
	Bytes      int64  `json:"bytes"`
	Unchanged  bool   `json:"unchanged,omitempty"` // the export had not changed since the last update
	Error      string `json:"error,omitempty"`
}

// Update downloads the given ecosystems (all when empty) and replaces
// each one's part of the index in a single transaction. If a download or
// its checks fail, that ecosystem keeps its previous data. It returns one
// result per ecosystem; err is set only when nothing could be updated.
func (v *VulnDB) Update(ctx context.Context, client *http.Client, baseURL, userAgent string, ecosystems []string, progress UpdateProgress) ([]UpdateResult, error) {
	if len(ecosystems) == 0 {
		ecosystems = Ecosystems
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	if baseURL == "" {
		baseURL = OSVBaseURL
	}
	var results []UpdateResult
	ok := 0
	for _, eco := range ecosystems {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		res := v.updateOne(ctx, client, baseURL, userAgent, eco, progress)
		if res.Error == "" {
			ok++
		}
		results = append(results, res)
	}
	if ok == 0 && len(results) > 0 {
		return results, fmt.Errorf("could not update the vulnerability database: %s", results[0].Error)
	}
	return results, nil
}

func (v *VulnDB) updateOne(ctx context.Context, client *http.Client, baseURL, userAgent, eco string, progress UpdateProgress) UpdateResult {
	res := UpdateResult{Ecosystem: eco}
	fail := func(err error) UpdateResult {
		res.Error = err.Error()
		return res
	}
	tmp, err := os.CreateTemp(filepath.Dir(v.path), "osv-download-*.zip")
	if err != nil {
		return fail(err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	sum, n, err := download(ctx, client, baseURL+"/"+eco+"/all.zip", userAgent, tmp, func(done, total int64) {
		if progress != nil {
			progress(eco, "download", done, total)
		}
	})
	res.Bytes = n
	if err != nil {
		return fail(err)
	}
	var prev string
	_ = v.db.QueryRowContext(ctx, `SELECT COALESCE(source_md5, '') FROM ecosystems WHERE ecosystem = ? AND schema = ?`, eco, osvSchemaVersion).Scan(&prev)
	if prev != "" && prev == sum {
		res.Unchanged = true
		_ = v.db.QueryRowContext(ctx, `SELECT advisories FROM ecosystems WHERE ecosystem = ?`, eco).Scan(&res.Advisories)
		_, _ = v.db.ExecContext(ctx, `UPDATE ecosystems SET updated_at = ? WHERE ecosystem = ?`, time.Now().UTC().Format(time.RFC3339), eco)
		return res
	}

	if progress != nil {
		progress(eco, "index", 0, -1)
	}
	idx, count, err := indexExport(ctx, tmp, n, eco)
	if err != nil {
		return fail(err)
	}
	if err := v.replace(ctx, eco, idx, count, sum); err != nil {
		return fail(err)
	}
	res.Advisories = count
	v.mu.Lock()
	v.cache, v.has = map[string][]advisory{}, map[string]bool{}
	v.mu.Unlock()
	return res
}

// download streams url into w and returns the MD5 of what it read. When
// the server publishes a checksum (x-goog-hash) or a length, the body must
// match them.
func download(ctx context.Context, client *http.Client, url, userAgent string, w io.Writer, progress func(done, total int64)) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	if resp.ContentLength > maxExportSize {
		return "", 0, fmt.Errorf("download %s: %d bytes is more than the %d allowed", url, resp.ContentLength, int64(maxExportSize))
	}
	h := md5.New() //nolint:gosec // nosemgrep: use-of-md5 -- matches the checksum GCS publishes; TLS gives authenticity
	cw := &countingWriter{progress: progress, total: resp.ContentLength}
	n, err := io.Copy(io.MultiWriter(w, h, cw), io.LimitReader(resp.Body, maxExportSize+1))
	if err != nil {
		return "", n, fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxExportSize {
		return "", n, fmt.Errorf("download %s: more than %d bytes", url, int64(maxExportSize))
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", n, fmt.Errorf("download %s: got %d of %d bytes", url, n, resp.ContentLength)
	}
	sum := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if want := googMD5(resp.Header); want != "" && want != sum {
		return "", n, fmt.Errorf("download %s: checksum does not match, the file is damaged", url)
	}
	return sum, n, nil
}

func googMD5(h http.Header) string {
	for _, v := range h.Values("x-goog-hash") {
		for _, part := range strings.Split(v, ",") {
			if s, ok := strings.CutPrefix(strings.TrimSpace(part), "md5="); ok {
				return s
			}
		}
	}
	return ""
}

type countingWriter struct {
	n, total int64
	last     time.Time
	progress func(done, total int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.progress != nil && time.Since(c.last) > 100*time.Millisecond {
		c.last = time.Now()
		c.progress(c.n, c.total)
	}
	return len(p), nil
}

// osvRecord is the part of an OSV record (https://ossf.github.io/osv-schema/)
// the index needs.
type osvRecord struct {
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	Details   string   `json:"details"`
	Aliases   []string `json:"aliases"`
	Withdrawn string   `json:"withdrawn"`
	Severity  []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Ranges   []osvRange `json:"ranges"`
		Versions []string   `json:"versions"`
	} `json:"affected"`
	DatabaseSpecific map[string]any `json:"database_specific"`
}

// indexExport reads an ecosystem export (a zip of OSV JSON records) into
// per-package advisory lists.
func indexExport(ctx context.Context, r io.ReaderAt, size int64, eco string) (map[string][]advisory, int, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s export: %w", eco, err)
	}
	idx := map[string][]advisory{}
	count := 0
	for _, f := range zr.File {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if !strings.HasSuffix(f.Name, ".json") || f.UncompressedSize64 > maxRecordSize {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, 0, fmt.Errorf("read %s export: %w", eco, err)
		}
		var rec osvRecord
		err = json.NewDecoder(io.LimitReader(rc, maxRecordSize)).Decode(&rec)
		rc.Close()
		if err != nil {
			continue // one bad record does not spoil the rest
		}
		if rec.ID == "" || rec.Withdrawn != "" {
			continue
		}
		sev, score := recordSeverity(&rec)
		summary := rec.Summary
		if summary == "" {
			summary, _, _ = strings.Cut(strings.TrimSpace(rec.Details), "\n")
		}
		if len(summary) > 160 {
			summary = strings.TrimSpace(summary[:157]) + "..."
		}
		added := false
		for _, a := range rec.Affected {
			if ecoName(a.Package.Ecosystem) != eco || a.Package.Name == "" {
				continue
			}
			var ranges []osvRange
			for _, rg := range a.Ranges {
				if rg.Type == "SEMVER" || rg.Type == "ECOSYSTEM" {
					ranges = append(ranges, rg)
				}
			}
			if len(ranges) == 0 && len(a.Versions) == 0 {
				continue
			}
			key := normalizeName(eco, a.Package.Name)
			idx[key] = append(idx[key], advisory{
				ID: rec.ID, Summary: summary, Aliases: rec.Aliases, Severity: sev, Score: score,
				Ranges: ranges, Versions: a.Versions,
			})
			added = true
		}
		if added {
			count++
		}
	}
	if count == 0 {
		return nil, 0, fmt.Errorf("the %s export holds no advisories; keeping the previous data", eco)
	}
	return idx, count, nil
}

// ecoName drops a release suffix such as "Debian:12".
func ecoName(s string) string {
	name, _, _ := strings.Cut(s, ":")
	return name
}

func (v *VulnDB) replace(ctx context.Context, eco string, idx map[string][]advisory, count int, sum string) error {
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM advisories WHERE ecosystem = ?`, eco); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO advisories(ecosystem, name, data) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for name, list := range idx {
		b, err := json.Marshal(list)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, eco, name, string(b)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ecosystems(ecosystem, advisories, source_md5, updated_at, schema) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ecosystem) DO UPDATE SET advisories = excluded.advisories, source_md5 = excluded.source_md5,
		updated_at = excluded.updated_at, schema = excluded.schema`,
		eco, count, sum, time.Now().UTC().Format(time.RFC3339), osvSchemaVersion); err != nil {
		return err
	}
	return tx.Commit()
}
