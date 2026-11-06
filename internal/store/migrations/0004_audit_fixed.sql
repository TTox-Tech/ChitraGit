-- Schema v4 (M10): findings about a repo's current state (dependencies
-- at HEAD) can be fixed. A fixed finding is kept, with when it was last
-- seen, so the history stays; it is reported again if it comes back.

ALTER TABLE findings ADD COLUMN fixed_at TEXT;
