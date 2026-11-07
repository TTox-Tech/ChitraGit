-- Schema v6 (CG-01): snapshots. A snapshot is the full ref state of one
-- repo at one moment: every branch, remote-tracking branch and tag with its
-- SHA. Syncs take one before (pre) and one after (post) each repo; a
-- snapshot identical to the repo's last one is not stored again. The
-- objects every snapshot points at are pinned under refs/chitragit/snap/
-- inside the repo, so git gc never removes them.

CREATE TABLE snapshots (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    run_id  INTEGER REFERENCES runs(id) ON DELETE SET NULL,
    at      TEXT NOT NULL,
    kind    TEXT NOT NULL,                      -- pre | post | manual
    label   TEXT                                -- named snapshots (CG-08)
);
CREATE INDEX snapshots_repo ON snapshots(repo_id, id);
CREATE INDEX snapshots_run ON snapshots(run_id);

CREATE TABLE snapshot_refs (
    snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    ref         TEXT NOT NULL,
    sha         TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, ref)
) WITHOUT ROWID;
