-- Schema v8 (CG-08): named snapshots. A name (`chitragit tag pre-move`)
-- marks one snapshot per repo, so the same name can stand for a moment of
-- the whole archive. Retention always keeps named snapshots.

CREATE UNIQUE INDEX IF NOT EXISTS snapshots_label ON snapshots(repo_id, label) WHERE label IS NOT NULL;
