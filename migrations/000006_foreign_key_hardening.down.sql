-- Restore the v5 active_task_locks shape. The run_id column remains, but the
-- foreign key was not present before migration 6.
CREATE TABLE active_task_locks_old (
    lock_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    folder_id TEXT NOT NULL,
    locked_at TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    run_status TEXT NOT NULL DEFAULT 'QUEUED',
    heartbeat_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

INSERT INTO active_task_locks_old(
    lock_id, task_id, folder_id, locked_at, run_id, run_status, heartbeat_at
)
SELECT lock_id, task_id, folder_id, locked_at, run_id, run_status, heartbeat_at
FROM active_task_locks;

DROP TABLE active_task_locks;
ALTER TABLE active_task_locks_old RENAME TO active_task_locks;

CREATE INDEX idx_active_task_locks_folder ON active_task_locks(folder_id);
CREATE UNIQUE INDEX uq_active_task_locks_run ON active_task_locks(run_id);
CREATE UNIQUE INDEX uq_active_task_locks_folder ON active_task_locks(folder_id);
CREATE INDEX idx_active_task_locks_status ON active_task_locks(run_status, heartbeat_at);
