-- MinerU ZIPs uploaded before v14 were split with with_filter=true. MaxKB
-- collapses consecutive newlines in that mode, which can break Markdown image
-- blocks. Queue each currently synchronized MinerU document for one replacement
-- upload so the corrected split request takes effect.
UPDATE sync_files
SET file_status = 'STALE_REMOTE_EXISTS',
    updated_at = CURRENT_TIMESTAMP
WHERE last_success_used_mineru = 1
  AND COALESCE(remote_doc_id, '') <> ''
  AND file_status = 'SYNCED';
