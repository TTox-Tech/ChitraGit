-- Schema v3 (M9): KodeKavach security audit.
-- Findings never hold a secret in full: only its redacted preview, and the
-- finding ID, which is derived from a hash of the value.

CREATE TABLE audit_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at   TEXT NOT NULL,
    finished_at  TEXT,
    checks       TEXT NOT NULL,                 -- comma separated
    history      TEXT NOT NULL,                 -- head | incremental | full
    repos        INTEGER NOT NULL DEFAULT 0,
    new_findings INTEGER NOT NULL DEFAULT 0,
    failed_repos INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE findings (
    id            TEXT PRIMARY KEY,             -- stable: repo + check + rule + value hash
    repo_id       TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    check_kind    TEXT NOT NULL,                -- secrets | deps | actions | agents
    rule          TEXT NOT NULL,
    title         TEXT NOT NULL,
    severity      TEXT NOT NULL,                -- info | low | medium | high | critical
    confidence    TEXT NOT NULL,                -- high | low
    file          TEXT NOT NULL,
    line          INTEGER,
    commit_sha    TEXT,
    author        TEXT,
    authored_at   TEXT,
    preview       TEXT,
    hint          TEXT,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    first_run_id  INTEGER
);
CREATE INDEX findings_repo ON findings(repo_id);
CREATE INDEX findings_severity ON findings(severity);

-- Ignored findings stay in findings; this table says why and since when.
CREATE TABLE finding_ignores (
    finding_id TEXT PRIMARY KEY,
    reason     TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- What each check already covered in a repo, for incremental audits:
-- the sorted ref tips seen by the last successful run, one per line.
CREATE TABLE audit_cursors (
    repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    check_kind TEXT NOT NULL,
    tips       TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (repo_id, check_kind)
);
