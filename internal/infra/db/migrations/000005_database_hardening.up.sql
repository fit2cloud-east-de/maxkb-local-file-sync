-- Database hardening: durable checkpoints, normalized identities and file audit data.
-- This migration is additive so existing installations retain all local state.
ALTER TABLE sync_folders ADD COLUMN normalized_local_path TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN maxkb_base_url_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN normalized_maxkb_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN workspace_name TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN knowledge_folder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN knowledge_name TEXT NOT NULL DEFAULT '';

ALTER TABLE sync_files ADD COLUMN normalized_relative_path TEXT NOT NULL DEFAULT '';

ALTER TABLE sync_runs ADD COLUMN checkpoint_data TEXT NOT NULL DEFAULT '{}';

ALTER TABLE file_attempts ADD COLUMN mineru_batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE file_attempts ADD COLUMN mineru_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE file_attempts ADD COLUMN markdown_main_file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE file_attempts ADD COLUMN maxkb_document_status TEXT NOT NULL DEFAULT '';
ALTER TABLE file_attempts ADD COLUMN document_url TEXT NOT NULL DEFAULT '';
ALTER TABLE file_attempts ADD COLUMN phase_timings_json TEXT NOT NULL DEFAULT '{}';

-- Existing rows were created before normalized identities existed. Backfill from
-- the stored values; repository writes keep these values synchronized.
UPDATE sync_folders
SET normalized_local_path = CASE
        WHEN normalized_local_path = '' THEN replace(local_path, '\\', '/')
        ELSE normalized_local_path
    END,
    normalized_maxkb_base_url = CASE
        WHEN normalized_maxkb_base_url = '' THEN rtrim(COALESCE(maxkb_base_url_snapshot, ''), '/')
        ELSE normalized_maxkb_base_url
    END
WHERE normalized_local_path = '' OR normalized_maxkb_base_url = '';

UPDATE sync_files
SET normalized_relative_path = CASE
        WHEN normalized_relative_path = '' THEN replace(relative_path, '\\', '/')
        ELSE normalized_relative_path
    END
WHERE normalized_relative_path = '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_normalized_local_path
    ON sync_folders(normalized_local_path);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_remote_binding
    ON sync_folders(normalized_maxkb_base_url, workspace_id, kb_id);
DROP INDEX IF EXISTS uq_sync_files_normalized_path;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_files_normalized_path
    ON sync_files(folder_id, normalized_relative_path)
    WHERE normalized_relative_path <> '';

CREATE INDEX IF NOT EXISTS idx_job_queue_claimable
    ON job_queue(available_at, priority, queued_at, id);
CREATE INDEX IF NOT EXISTS idx_job_queue_task
    ON job_queue(task_id);
CREATE INDEX IF NOT EXISTS idx_active_task_locks_status
    ON active_task_locks(run_status, heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_file_attempts_remote_refs
    ON file_attempts(mineru_task_id, maxkb_batch_task_id, maxkb_document_id);
CREATE INDEX IF NOT EXISTS idx_sync_runs_checkpoint
    ON sync_runs(status, current_file_ordinal);
