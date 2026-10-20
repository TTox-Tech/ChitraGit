-- ChitraGit archive database, schema v1.
-- Repo identity is a UUID stored in the repo's own git config
-- (chitragit.id), so moves and renames on disk keep their history.

CREATE TABLE repos (
    id              TEXT PRIMARY KEY,              -- UUID
    path            TEXT NOT NULL UNIQUE,          -- relative to archive root, '/' separated
    kind            TEXT NOT NULL,                 -- normal | bare | worktree | nested
    provider        TEXT,                          -- github | gitlab | gitea | generic
    host            TEXT,
    owner           TEXT,
    name            TEXT,
    default_branch  TEXT,
    status          TEXT NOT NULL DEFAULT 'unknown',
    status_reason   TEXT,
    favourite       INTEGER NOT NULL DEFAULT 0,
    locked          INTEGER NOT NULL DEFAULT 0,    -- delete-protected
    missing         INTEGER NOT NULL DEFAULT 0,    -- no longer found on disk
    size_bytes      INTEGER,
    first_seen_at   TEXT NOT NULL,
    last_scanned_at TEXT,
    last_synced_at  TEXT,
    last_upstream_activity_at TEXT
);
CREATE INDEX repos_owner ON repos(owner);
CREATE INDEX repos_status ON repos(status);

CREATE TABLE remotes (
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name    TEXT NOT NULL,
    url     TEXT NOT NULL,
    PRIMARY KEY (repo_id, name)
);

CREATE TABLE runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,                    -- sync | scan | clone
    mode        TEXT,
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    state       TEXT NOT NULL,                    -- running | finished | interrupted | discarded
    total       INTEGER NOT NULL DEFAULT 0,
    updated     INTEGER NOT NULL DEFAULT 0,
    unchanged   INTEGER NOT NULL DEFAULT 0,
    skipped     INTEGER NOT NULL DEFAULT 0,
    failed      INTEGER NOT NULL DEFAULT 0,
    host        TEXT,
    pid         INTEGER
);

-- Per-repo journal of a run; this is what makes resume possible.
CREATE TABLE run_items (
    run_id      INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    repo_id     TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    state       TEXT NOT NULL,                    -- pending | running | done
    outcome     TEXT,                             -- updated | unchanged | skipped | failed | rescued
    category    TEXT,
    message     TEXT,
    started_at  TEXT,
    finished_at TEXT,
    PRIMARY KEY (run_id, repo_id)
);

-- Old → new SHA for every ref a run changed. Feeds the activity feed.
CREATE TABLE ref_changes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id    INTEGER REFERENCES runs(id) ON DELETE SET NULL,
    repo_id   TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    ref       TEXT NOT NULL,
    old_sha   TEXT,                               -- NULL = new ref
    new_sha   TEXT,                               -- NULL = deleted upstream
    commits   INTEGER,                            -- new commits, when known
    kind      TEXT NOT NULL,                      -- update | create | delete | rewrite
    at        TEXT NOT NULL
);
CREATE INDEX ref_changes_at ON ref_changes(at);
CREATE INDEX ref_changes_repo ON ref_changes(repo_id, at);

-- Append-only event log; basis for future multi-device sync.
CREATE TABLE events (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    at       TEXT NOT NULL,
    repo_id  TEXT,
    type     TEXT NOT NULL,
    payload  TEXT                                  -- JSON
);
CREATE INDEX events_repo ON events(repo_id, seq);

CREATE TABLE tags (
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY (repo_id, tag)
);

CREATE TABLE rescues (
    repo_id        TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    reason         TEXT NOT NULL,                 -- deleted_or_private | dmca | ...
    confirmations  INTEGER NOT NULL DEFAULT 0,
    first_failed_at TEXT NOT NULL,
    rescued_at     TEXT,                          -- set once confirmations reach the threshold
    bundle_path    TEXT
);

CREATE TABLE conflicts (
    repo_id    TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    run_id     INTEGER REFERENCES runs(id) ON DELETE SET NULL,
    backup_ref TEXT NOT NULL,
    stash_msg  TEXT NOT NULL,
    files      TEXT NOT NULL,                     -- JSON array
    created_at TEXT NOT NULL
);

CREATE TABLE sources (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    spec       TEXT NOT NULL UNIQUE,              -- e.g. github:torvalds, github:@starred
    filters    TEXT,                              -- JSON
    added_at   TEXT NOT NULL,
    last_checked_at TEXT
);
