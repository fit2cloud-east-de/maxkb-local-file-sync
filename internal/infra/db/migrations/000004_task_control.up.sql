-- Add task enable/disable and sync control columns
ALTER TABLE sync_folders ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE sync_folders ADD COLUMN disabled_at TEXT;
ALTER TABLE sync_folders ADD COLUMN sync_delete_local_removed INTEGER NOT NULL DEFAULT 0;

-- Add MinerU advanced configuration columns
ALTER TABLE sync_folders ADD COLUMN mineru_retry_count INTEGER NOT NULL DEFAULT 3;
ALTER TABLE sync_folders ADD COLUMN mineru_request_timeout_ms INTEGER NOT NULL DEFAULT 60000;
ALTER TABLE sync_folders ADD COLUMN mineru_task_timeout_ms INTEGER NOT NULL DEFAULT 300000;
ALTER TABLE sync_folders ADD COLUMN mineru_poll_interval_ms INTEGER NOT NULL DEFAULT 2000;
ALTER TABLE sync_folders ADD COLUMN mineru_save_full_result INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_folders ADD COLUMN mineru_result_save_dir TEXT NOT NULL DEFAULT '';

-- Add file filtering configuration
ALTER TABLE sync_folders ADD COLUMN include_patterns TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN exclude_patterns TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_folders ADD COLUMN mineru_file_extensions TEXT NOT NULL DEFAULT '';

-- Add next execution time for display
ALTER TABLE sync_folders ADD COLUMN next_execution_at TEXT;

-- Update existing folders to be enabled by default
UPDATE sync_folders SET enabled = 1 WHERE enabled IS NULL;

-- Create index for enabled tasks
CREATE INDEX IF NOT EXISTS idx_sync_folders_enabled ON sync_folders(enabled) WHERE enabled = 1;
