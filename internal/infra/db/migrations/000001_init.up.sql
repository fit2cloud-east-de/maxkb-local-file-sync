-- 同步文件夹表
CREATE TABLE sync_folders (
    folder_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    local_path TEXT NOT NULL UNIQUE,
    kb_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    enable_mineru INTEGER NOT NULL DEFAULT 0,
    mineru_mode TEXT,
    mineru_endpoint TEXT,
    cron_expression TEXT,
    cron_enabled INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_sync_folders_kb ON sync_folders(kb_id);
CREATE INDEX idx_sync_folders_cron ON sync_folders(cron_enabled);

-- 同步文件表
CREATE TABLE sync_files (
    file_id TEXT PRIMARY KEY,
    folder_id TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    file_status TEXT NOT NULL,
    observed_md5 TEXT,
    last_success_md5 TEXT,
    remote_doc_id TEXT,
    last_synced_at TEXT,
    last_checked_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(folder_id, relative_path),
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE INDEX idx_sync_files_folder ON sync_files(folder_id);
CREATE INDEX idx_sync_files_status ON sync_files(file_status);
CREATE INDEX idx_sync_files_path ON sync_files(folder_id, relative_path);

-- 同步任务表
CREATE TABLE sync_tasks (
    task_id TEXT PRIMARY KEY,
    folder_id TEXT NOT NULL,
    kb_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    run_status TEXT NOT NULL,
    processing_stage TEXT NOT NULL,
    control_state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    error_message TEXT,
    total_files INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE INDEX idx_sync_tasks_folder ON sync_tasks(folder_id);
CREATE INDEX idx_sync_tasks_status ON sync_tasks(run_status);
CREATE INDEX idx_sync_tasks_created ON sync_tasks(created_at DESC);

-- 运行文件表（执行计划）
CREATE TABLE run_files (
    run_file_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    file_id TEXT NOT NULL,
    processing_stage TEXT NOT NULL,
    control_state TEXT NOT NULL,
    final_status TEXT,
    snapshot_path TEXT,
    snapshot_size INTEGER,
    snapshot_modified_at TEXT,
    snapshot_md5 TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (file_id) REFERENCES sync_files(file_id) ON DELETE CASCADE
);

CREATE INDEX idx_run_files_task ON run_files(task_id);
CREATE INDEX idx_run_files_file ON run_files(file_id);
CREATE INDEX idx_run_files_stage ON run_files(processing_stage);

-- 活动任务锁表
CREATE TABLE active_task_locks (
    lock_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    folder_id TEXT NOT NULL,
    locked_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE INDEX idx_active_task_locks_folder ON active_task_locks(folder_id);

-- 操作历史表
CREATE TABLE operation_history (
    history_id TEXT PRIMARY KEY,
    task_id TEXT,
    operation_type TEXT NOT NULL,
    operation_detail TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE SET NULL
);

CREATE INDEX idx_operation_history_task ON operation_history(task_id);
CREATE INDEX idx_operation_history_created ON operation_history(created_at DESC);
