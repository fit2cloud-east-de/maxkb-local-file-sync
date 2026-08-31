-- V3 recovery audit fields. InitSchema also applies these additively for
-- installations that do not run golang-migrate at startup.
ALTER TABLE sync_runs ADD COLUMN last_interrupted_at TEXT;
ALTER TABLE sync_runs ADD COLUMN last_recovery_at TEXT;
ALTER TABLE sync_runs ADD COLUMN parent_run_id TEXT;

CREATE INDEX IF NOT EXISTS idx_sync_runs_recovery ON sync_runs(last_recovery_at DESC);
