-- Schema v7 (CG-42.17): KodeKavach Shield verdicts of repos on disk. The
-- baseline scan judges the HEAD of every repo that has no verdict yet,
-- such as every repo of an archive made before Shield. A repo whose
-- verdict is 'block' is held: syncs still download into it, but never
-- move its branches or touch its files. Nothing is ever deleted.

CREATE TABLE shield_verdicts (
    repo_id    TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,              -- the HEAD judged; '' for an empty repo
    verdict    TEXT NOT NULL,              -- pass | warn | block
    findings   INTEGER NOT NULL,
    reason     TEXT NOT NULL,              -- plain words, shown as the repo's status reason
    report     TEXT NOT NULL,              -- JSON list of findings
    at         TEXT NOT NULL
);
