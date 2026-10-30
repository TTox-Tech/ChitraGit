-- Schema v2 (M5): provider facts, rescue bookkeeping and follow errors.

-- Upstream marked the repo read-only ("Archived upstream" badge, info only).
ALTER TABLE repos ADD COLUMN archived_upstream INTEGER NOT NULL DEFAULT 0;

-- A failure is confirmed at most once per run, so "2 confirmations"
-- always means two different runs (PRD F5).
ALTER TABLE rescues ADD COLUMN last_run_id INTEGER;
ALTER TABLE rescues ADD COLUMN detail TEXT;          -- what the API or git said
ALTER TABLE rescues ADD COLUMN bundled_at TEXT;

ALTER TABLE sources ADD COLUMN last_error TEXT;
