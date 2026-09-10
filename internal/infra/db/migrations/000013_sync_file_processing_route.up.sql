-- Persist the processing route used by the most recent successful upload.
-- A task configuration change can then replace an unchanged file when its
-- direct-upload/MinerU route changes.
ALTER TABLE sync_files ADD COLUMN last_success_used_mineru INTEGER NOT NULL DEFAULT 0;

-- Preserve the route used by the most recent successful attempt when
-- upgrading an existing DB. A file may have switched routes more than once,
-- so the existence of any historical MinerU attempt is not sufficient.
UPDATE sync_files
SET last_success_used_mineru = COALESCE((
    SELECT CASE WHEN COALESCE(fa.mineru_task_id, '') <> '' THEN 1 ELSE 0 END
    FROM run_files rf
    JOIN file_attempts fa ON fa.run_file_id = rf.run_file_id
    WHERE rf.file_id = sync_files.file_id
      AND rf.final_status = 'SUCCESS'
      AND fa.status = 'SUCCESS'
    ORDER BY COALESCE(fa.completed_at, fa.started_at, rf.completed_at, rf.created_at) DESC,
             fa.attempt_no DESC
    LIMIT 1
), 0);
