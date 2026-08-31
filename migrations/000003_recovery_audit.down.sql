DROP INDEX IF EXISTS idx_sync_runs_recovery;
ALTER TABLE sync_runs DROP COLUMN parent_run_id;
ALTER TABLE sync_runs DROP COLUMN last_recovery_at;
ALTER TABLE sync_runs DROP COLUMN last_interrupted_at;
