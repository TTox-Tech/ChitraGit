-- Schema v9 (CG-32): what the forge says about a repo, kept offline. Filled
-- from API answers a sync or follow already gets, so it costs no extra
-- calls. Plain git remotes never get a row. A rescued repo keeps its last
-- row, since upstream can no longer be asked.

CREATE TABLE IF NOT EXISTS repo_meta (
    repo_id     TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    description TEXT NOT NULL DEFAULT '',
    topics      TEXT NOT NULL DEFAULT '[]', -- JSON array of strings
    stars       INTEGER NOT NULL DEFAULT 0,
    language    TEXT NOT NULL DEFAULT '',
    license     TEXT NOT NULL DEFAULT '',   -- SPDX id, e.g. MIT
    homepage    TEXT NOT NULL DEFAULT '',
    fork        INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL
) WITHOUT ROWID;
