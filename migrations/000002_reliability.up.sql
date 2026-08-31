-- V2/V3 durable execution, crash recovery and external-operation reconciliation.
ALTER TABLE sync_files ADD COLUMN pending_remote_doc_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_files ADD COLUMN observed_size INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_files ADD COLUMN observed_modified_at TEXT;
ALTER TABLE run_files ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE active_task_locks ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE active_task_locks ADD COLUMN run_status TEXT NOT NULL DEFAULT 'QUEUED';
ALTER TABLE active_task_locks ADD COLUMN heartbeat_at TEXT NOT NULL DEFAULT '';

CREATE TABLE sync_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    folder_id TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','STOP_REQUESTED','STOPPED','SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED','INTERRUPTED','CANCELLED')),
    queued_at TEXT NOT NULL,
    started_at TEXT, pause_requested_at TEXT, paused_at TEXT, resumed_at TEXT,
    stop_requested_at TEXT, stopped_at TEXT, cancelled_at TEXT, completed_at TEXT,
    control_reason TEXT NOT NULL DEFAULT '', checkpoint_version INTEGER NOT NULL DEFAULT 1,
    current_file_ordinal INTEGER NOT NULL DEFAULT 0, total_files INTEGER NOT NULL DEFAULT 0,
    new_count INTEGER NOT NULL DEFAULT 0, updated_count INTEGER NOT NULL DEFAULT 0,
    deleted_count INTEGER NOT NULL DEFAULT 0, skipped_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0, failed_count INTEGER NOT NULL DEFAULT 0,
    reconcile_count INTEGER NOT NULL DEFAULT 0, recovery_count INTEGER NOT NULL DEFAULT 0,
    error_summary TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY(folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE TABLE file_attempts (
    id TEXT PRIMARY KEY, run_file_id TEXT NOT NULL, attempt_no INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'RUNNING' CHECK(status IN ('RUNNING','SUCCESS','FAILED','CANCELLED','RECONCILE_REQUIRED')),
    started_at TEXT NOT NULL, completed_at TEXT, error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
    mineru_remote_ref TEXT NOT NULL DEFAULT '', mineru_task_id TEXT NOT NULL DEFAULT '', mineru_status TEXT NOT NULL DEFAULT '',
    maxkb_source_file_id TEXT NOT NULL DEFAULT '', maxkb_batch_task_id TEXT NOT NULL DEFAULT '', maxkb_document_id TEXT NOT NULL DEFAULT '',
    deleting_document_id TEXT NOT NULL DEFAULT '', delete_started_at TEXT, delete_completed_at TEXT, delete_retry_count INTEGER NOT NULL DEFAULT 0,
    snapshot_path TEXT NOT NULL DEFAULT '', snapshot_size INTEGER NOT NULL DEFAULT 0, snapshot_modified_at TEXT, snapshot_md5 TEXT NOT NULL DEFAULT '',
    source_md5_before TEXT NOT NULL DEFAULT '', source_md5_after TEXT NOT NULL DEFAULT '', source_changed_during_processing INTEGER NOT NULL DEFAULT 0,
    request_fingerprint TEXT NOT NULL DEFAULT '', reconcile_reason TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(run_file_id) REFERENCES run_files(run_file_id) ON DELETE CASCADE,
    UNIQUE(run_file_id, attempt_no)
);

CREATE TABLE job_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL UNIQUE, task_id TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100, queued_at TEXT NOT NULL, available_at TEXT NOT NULL,
    claimed_at TEXT, claim_owner TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(run_id) REFERENCES sync_runs(id) ON DELETE CASCADE,
    FOREIGN KEY(task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE
);

CREATE TABLE system_settings (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK(id=1), config_version INTEGER NOT NULL DEFAULT 1,
    maxkb_base_url TEXT NOT NULL DEFAULT '', maxkb_normalized_base_url TEXT NOT NULL DEFAULT '', maxkb_user_key_ref TEXT NOT NULL DEFAULT '',
    maxkb_version TEXT NOT NULL DEFAULT '', maxkb_version_display TEXT NOT NULL DEFAULT '', maxkb_last_validated_at TEXT, maxkb_validation_success INTEGER NOT NULL DEFAULT 0,
    mineru_enabled INTEGER NOT NULL DEFAULT 0, mineru_base_url TEXT NOT NULL DEFAULT '', mineru_user_key_ref TEXT NOT NULL DEFAULT '', mineru_mode TEXT NOT NULL DEFAULT 'online',
    mineru_last_validated_at TEXT, mineru_validation_success INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO system_settings(id) VALUES(1);

-- Preserve legacy tasks as auditable durable runs.
INSERT OR IGNORE INTO sync_runs(id,task_id,folder_id,trigger_type,status,queued_at,started_at,completed_at,total_files,success_count,failed_count,skipped_count,error_summary)
SELECT task_id,task_id,folder_id,trigger_type,
 CASE WHEN run_status IN ('QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','STOP_REQUESTED','STOPPED','SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED','INTERRUPTED','CANCELLED') THEN run_status ELSE 'INTERRUPTED' END,
 created_at,started_at,completed_at,total_files,success_count,failed_count,skipped_count,COALESCE(error_message,'') FROM sync_tasks;

DELETE FROM active_task_locks WHERE task_id IN (SELECT task_id FROM sync_tasks WHERE run_status IN ('SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED','STOPPED','CANCELLED'));
UPDATE active_task_locks SET run_id=task_id,
 run_status=COALESCE((SELECT CASE WHEN run_status IN ('RUNNING','PAUSED','INTERRUPTED') THEN run_status ELSE 'QUEUED' END FROM sync_tasks WHERE sync_tasks.task_id=active_task_locks.task_id),'QUEUED'),
 heartbeat_at=CASE WHEN heartbeat_at='' THEN locked_at ELSE heartbeat_at END;
DELETE FROM active_task_locks WHERE rowid NOT IN (SELECT MAX(rowid) FROM active_task_locks GROUP BY folder_id);

CREATE INDEX idx_sync_runs_task ON sync_runs(task_id,queued_at DESC);
CREATE INDEX idx_sync_runs_status ON sync_runs(status);
CREATE INDEX idx_run_files_final ON run_files(task_id,final_status);
CREATE INDEX idx_file_attempts_run_file ON file_attempts(run_file_id,attempt_no DESC);
CREATE INDEX idx_file_attempts_status ON file_attempts(status);
CREATE INDEX idx_job_queue_priority ON job_queue(priority,available_at,queued_at);
CREATE UNIQUE INDEX uq_active_task_locks_run ON active_task_locks(run_id);
CREATE UNIQUE INDEX uq_active_task_locks_folder ON active_task_locks(folder_id);

INSERT OR IGNORE INTO job_queue(run_id,task_id,priority,queued_at,available_at)
SELECT id,task_id,10,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM sync_runs WHERE status IN ('QUEUED','RUNNING','INTERRUPTED');
UPDATE sync_runs SET status='QUEUED',control_reason='migration_recovery' WHERE status IN ('RUNNING','INTERRUPTED');
UPDATE sync_tasks SET run_status='QUEUED',control_state='ACTIVE' WHERE task_id IN (SELECT id FROM sync_runs WHERE status='QUEUED');
