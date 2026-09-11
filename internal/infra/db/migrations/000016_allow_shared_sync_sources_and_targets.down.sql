CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_normalized_local_path
    ON sync_folders(normalized_local_path);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_remote_binding
    ON sync_folders(normalized_maxkb_base_url, workspace_id, kb_id);
