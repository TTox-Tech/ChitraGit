-- Schema v5 (CG-06): the event log becomes a hash-chained ledger. Each row
-- stores the hash of the row before it and its own hash, so editing or
-- deleting a row breaks the chain. Rows written before v5 get their hashes
-- when the archive is next opened (Store.sealEvents).

ALTER TABLE events ADD COLUMN prev_hash TEXT;
ALTER TABLE events ADD COLUMN hash TEXT;
