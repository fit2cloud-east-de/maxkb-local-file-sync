-- Complete system-wide MinerU artifact settings. Migration 000007 added the
-- retention policy fields; these fields and cleanup result metadata are added
-- here so v7 installations can be upgraded without rebuilding the database.
ALTER TABLE system_settings ADD COLUMN mineru_save_full_result INTEGER NOT NULL DEFAULT 0;
ALTER TABLE system_settings ADD COLUMN mineru_result_save_dir TEXT NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN mineru_cleanup_temp_results INTEGER NOT NULL DEFAULT 1;
ALTER TABLE system_settings ADD COLUMN mineru_last_cleanup_status TEXT NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN mineru_last_cleanup_deleted_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE system_settings ADD COLUMN mineru_last_cleanup_error TEXT NOT NULL DEFAULT '';
