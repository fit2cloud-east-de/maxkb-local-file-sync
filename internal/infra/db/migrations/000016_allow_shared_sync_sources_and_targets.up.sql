-- Sync tasks are isolated by folder_id, so both their local source and MaxKB
-- target may be shared. The table-level local_path constraint is removed by
-- the post-migration rebuild because SQLite cannot drop it in place.
DROP INDEX IF EXISTS uq_sync_folders_normalized_local_path;
DROP INDEX IF EXISTS uq_sync_folders_remote_binding;
