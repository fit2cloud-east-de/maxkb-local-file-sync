package db

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// MigrateUp 执行数据库迁移
func (db *DB) MigrateUp(migrationsFS fs.FS) error {
	// A previous version of the application could leave golang-migrate's
	// marker at v1/dirty=true even though migrations 2-4 had already completed
	// (SQLite DDL was not rolled back by that version of the driver). Refuse to
	// guess for arbitrary dirty databases, but repair this known, verifiable
	// state so an otherwise healthy installation does not render the Wails UI
	// with an unusable database.
	if err := db.repairKnownDirtyState(); err != nil {
		return fmt.Errorf("failed to repair known dirty migration state: %w", err)
	}

	driver, err := sqlite.WithInstance(db.conn, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Migration 000008 was shipped by an earlier build with an incomplete
	// system_settings schema. A database can therefore legitimately report
	// version 8/clean while still missing one or more of the columns introduced
	// by that migration. Since golang-migrate never re-runs an already recorded
	// migration, repair that released schema after the normal migration pass.
	// The repair is additive only and is limited to clean databases at/after v8;
	// older versions are left to their versioned migrations.
	if err := db.repairReleasedMinerUArtifactSchema(); err != nil {
		return fmt.Errorf("failed to repair MinerU artifact schema: %w", err)
	}

	return nil
}

// repairKnownDirtyState advances only a precisely identified migration state:
// the marker says v1/dirty while the schema contains every additive change
// through v4 and none of the v5 columns. Unknown or partially applied states
// remain dirty and are rejected by golang-migrate, preserving the database for
// manual recovery instead of silently guessing a version.
func (db *DB) repairKnownDirtyState() error {
	version, dirty, err := db.GetMigrationVersion()
	if err != nil {
		// A new database has no migration marker yet; migrate.Up will create it.
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	if !dirty {
		return nil
	}
	if version != 1 {
		return nil
	}

	required := map[string][]string{
		"sync_folders": {
			"enabled", "disabled_at", "sync_delete_local_removed",
			"mineru_retry_count", "mineru_request_timeout_ms", "mineru_task_timeout_ms",
			"mineru_poll_interval_ms", "mineru_save_full_result", "mineru_result_save_dir",
			"include_patterns", "exclude_patterns", "mineru_file_extensions", "next_execution_at",
		},
		"sync_files":        {"pending_remote_doc_id", "observed_size", "observed_modified_at"},
		"run_files":         {"ordinal"},
		"active_task_locks": {"run_id", "run_status", "heartbeat_at"},
		"sync_runs":         {"last_interrupted_at", "last_recovery_at", "parent_run_id"},
		"system_settings":   {},
		"file_attempts":     {},
		"job_queue":         {},
	}
	for table, columns := range required {
		present, err := db.tableColumns(table)
		if err != nil {
			return err
		}
		if present == nil {
			return fmt.Errorf("known dirty state check failed: table %s is missing", table)
		}
		for _, column := range columns {
			if _, ok := present[column]; !ok {
				return fmt.Errorf("known dirty state check failed: column %s.%s is missing", table, column)
			}
		}
	}

	// If any v5 column exists, the database may be partially through v5 and
	// cannot safely be classified as the v4 state handled here.
	for table, columns := range map[string][]string{
		"sync_folders":  {"normalized_local_path", "maxkb_base_url_snapshot", "normalized_maxkb_base_url", "workspace_name", "knowledge_folder_id", "knowledge_name"},
		"sync_files":    {"normalized_relative_path"},
		"sync_runs":     {"checkpoint_data"},
		"file_attempts": {"mineru_batch_id", "mineru_mode", "markdown_main_file_path", "maxkb_document_status", "document_url", "phase_timings_json"},
	} {
		present, err := db.tableColumns(table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if _, ok := present[column]; ok {
				return fmt.Errorf("known dirty state check found partially applied v5 column %s.%s", table, column)
			}
		}
	}

	if _, err := db.Exec(`UPDATE schema_migrations SET version=4, dirty=0 WHERE version=1 AND dirty=1`); err != nil {
		return fmt.Errorf("clear stale migration marker: %w", err)
	}
	return nil
}

// repairReleasedMinerUArtifactSchema repairs databases that were marked at
// migration 000008 before all of its system_settings columns were present.
// This can happen when an older binary recorded the migration and a later
// binary only extended the migration file; golang-migrate correctly treats the
// migration as already applied and will not execute the new statements.
//
// Only clean v8+ databases are eligible. We intentionally do not modify dirty
// databases or databases below v8 because doing so could hide a partially
// applied migration and could make the next versioned migration fail with
// "duplicate column". Every operation here is an idempotent ADD COLUMN and
// existing settings/data are preserved.
func (db *DB) repairReleasedMinerUArtifactSchema() error {
	version, dirty, err := db.GetMigrationVersion()
	if err != nil {
		// A new database has no migration marker yet; migrate.Up will create it.
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	if dirty || version < 8 {
		return nil
	}

	present, err := db.tableColumns("system_settings")
	if err != nil {
		return err
	}
	if present == nil {
		return fmt.Errorf("system_settings table is missing")
	}

	columns := []struct {
		name       string
		definition string
	}{
		{"mineru_save_full_result", "INTEGER NOT NULL DEFAULT 0"},
		{"mineru_result_save_dir", "TEXT NOT NULL DEFAULT ''"},
		{"mineru_cleanup_temp_results", "INTEGER NOT NULL DEFAULT 1"},
		{"mineru_last_cleanup_status", "TEXT NOT NULL DEFAULT ''"},
		{"mineru_last_cleanup_deleted_count", "INTEGER NOT NULL DEFAULT 0"},
		{"mineru_last_cleanup_error", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if _, ok := present[column.name]; ok {
			continue
		}
		if err := db.ensureColumn("system_settings", column.name, column.definition); err != nil {
			return err
		}
		// Keep the snapshot in sync when a column is added during this repair.
		// This makes the repair idempotent even if a future compatibility field
		// is accidentally listed more than once.
		present[column.name] = struct{}{}
	}
	return nil
}

func (db *DB) tableColumns(table string) (map[string]struct{}, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("inspect table %s columns: %w", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect table %s columns: %w", table, err)
	}
	if len(columns) == 0 {
		return nil, nil
	}
	return columns, nil
}

// MigrateDown 回滚数据库迁移
func (db *DB) MigrateDown(migrationsFS fs.FS, steps int) error {
	driver, err := sqlite.WithInstance(db.conn, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	return nil
}

// GetMigrationVersion 获取当前迁移版本
func (db *DB) GetMigrationVersion() (uint, bool, error) {
	driver, err := sqlite.WithInstance(db.conn, &sqlite.Config{})
	if err != nil {
		return 0, false, fmt.Errorf("failed to create migration driver: %w", err)
	}

	version, dirty, err := driver.Version()
	if err != nil {
		return 0, false, fmt.Errorf("failed to get migration version: %w", err)
	}

	return uint(version), dirty, nil
}

// InitSchema 直接初始化数据库结构（不使用 migrate）
// 用于测试环境快速初始化
func (db *DB) InitSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS sync_folders (
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
    enabled INTEGER NOT NULL DEFAULT 1,
    disabled_at TEXT,
    sync_delete_local_removed INTEGER NOT NULL DEFAULT 0,
    mineru_retry_count INTEGER NOT NULL DEFAULT 3,
    mineru_request_timeout_ms INTEGER NOT NULL DEFAULT 60000,
    mineru_task_timeout_ms INTEGER NOT NULL DEFAULT 300000,
    mineru_poll_interval_ms INTEGER NOT NULL DEFAULT 2000,
    mineru_save_full_result INTEGER NOT NULL DEFAULT 0,
    mineru_result_save_dir TEXT NOT NULL DEFAULT '',
    include_patterns TEXT NOT NULL DEFAULT '',
    exclude_patterns TEXT NOT NULL DEFAULT '',
    mineru_file_extensions TEXT NOT NULL DEFAULT '',
    next_execution_at TEXT,
    normalized_local_path TEXT NOT NULL DEFAULT '',
    maxkb_base_url_snapshot TEXT NOT NULL DEFAULT '',
    normalized_maxkb_base_url TEXT NOT NULL DEFAULT '',
    workspace_name TEXT NOT NULL DEFAULT '',
    knowledge_folder_id TEXT NOT NULL DEFAULT '',
    knowledge_name TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_files (
    file_id TEXT PRIMARY KEY,
    folder_id TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    normalized_relative_path TEXT NOT NULL DEFAULT '',
    file_status TEXT NOT NULL,
    observed_md5 TEXT,
    last_success_md5 TEXT,
    remote_doc_id TEXT,
    pending_remote_doc_id TEXT NOT NULL DEFAULT '',
    observed_size INTEGER NOT NULL DEFAULT 0,
    observed_modified_at TEXT,
    last_synced_at TEXT,
    last_checked_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(folder_id, relative_path),
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

-- Kept as the public run record for backward-compatible Wails APIs.  sync_runs
-- is the authoritative control/checkpoint record used by the durable queue.
CREATE TABLE IF NOT EXISTS sync_tasks (
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
    total_files INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sync_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    folder_id TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN (
        'QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','STOP_REQUESTED',
        'STOPPED','SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED',
        'INTERRUPTED','CANCELLED')),
    queued_at TEXT NOT NULL,
    started_at TEXT,
    pause_requested_at TEXT,
    paused_at TEXT,
    resumed_at TEXT,
    stop_requested_at TEXT,
    stopped_at TEXT,
    cancelled_at TEXT,
    completed_at TEXT,
    control_reason TEXT NOT NULL DEFAULT '',
    checkpoint_version INTEGER NOT NULL DEFAULT 1,
    checkpoint_data TEXT NOT NULL DEFAULT '{}',
    current_file_ordinal INTEGER NOT NULL DEFAULT 0,
    total_files INTEGER NOT NULL DEFAULT 0,
    new_count INTEGER NOT NULL DEFAULT 0,
    updated_count INTEGER NOT NULL DEFAULT 0,
    deleted_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    reconcile_count INTEGER NOT NULL DEFAULT 0,
    recovery_count INTEGER NOT NULL DEFAULT 0,
    last_interrupted_at TEXT,
    last_recovery_at TEXT,
    parent_run_id TEXT,
    error_summary TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS run_files (
    run_file_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    file_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    processing_stage TEXT NOT NULL DEFAULT 'INIT',
    control_state TEXT NOT NULL DEFAULT 'ACTIVE' CHECK(control_state IN ('ACTIVE','PAUSED','STOPPED')),
    final_status TEXT NOT NULL DEFAULT 'PENDING' CHECK(final_status IN (
        'PENDING','SUCCESS','FAILED','SKIPPED','STOPPED','RECONCILE_REQUIRED')),
    snapshot_path TEXT,
    snapshot_size INTEGER,
    snapshot_modified_at TEXT,
    snapshot_md5 TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (file_id) REFERENCES sync_files(file_id) ON DELETE CASCADE,
    UNIQUE(task_id, file_id)
);

CREATE TABLE IF NOT EXISTS file_attempts (
    id TEXT PRIMARY KEY,
    run_file_id TEXT NOT NULL,
    attempt_no INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'RUNNING' CHECK(status IN (
        'RUNNING','SUCCESS','FAILED','CANCELLED','RECONCILE_REQUIRED')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    mineru_remote_ref TEXT NOT NULL DEFAULT '',
    mineru_task_id TEXT NOT NULL DEFAULT '',
    mineru_status TEXT NOT NULL DEFAULT '',
    maxkb_source_file_id TEXT NOT NULL DEFAULT '',
    maxkb_batch_task_id TEXT NOT NULL DEFAULT '',
    maxkb_document_id TEXT NOT NULL DEFAULT '',
    deleting_document_id TEXT NOT NULL DEFAULT '',
    delete_started_at TEXT,
    delete_completed_at TEXT,
    delete_retry_count INTEGER NOT NULL DEFAULT 0,
    snapshot_path TEXT NOT NULL DEFAULT '',
    snapshot_size INTEGER NOT NULL DEFAULT 0,
    snapshot_modified_at TEXT,
    snapshot_md5 TEXT NOT NULL DEFAULT '',
    source_md5_before TEXT NOT NULL DEFAULT '',
    source_md5_after TEXT NOT NULL DEFAULT '',
    source_changed_during_processing INTEGER NOT NULL DEFAULT 0,
    request_fingerprint TEXT NOT NULL DEFAULT '',
    reconcile_reason TEXT NOT NULL DEFAULT '',
    mineru_batch_id TEXT NOT NULL DEFAULT '',
    mineru_mode TEXT NOT NULL DEFAULT '',
    markdown_main_file_path TEXT NOT NULL DEFAULT '',
    maxkb_document_status TEXT NOT NULL DEFAULT '',
    document_url TEXT NOT NULL DEFAULT '',
    phase_timings_json TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (run_file_id) REFERENCES run_files(run_file_id) ON DELETE CASCADE,
    UNIQUE(run_file_id, attempt_no)
);

CREATE TABLE IF NOT EXISTS job_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL UNIQUE,
    task_id TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    queued_at TEXT NOT NULL,
    available_at TEXT NOT NULL,
    claimed_at TEXT,
    claim_owner TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (run_id) REFERENCES sync_runs(id) ON DELETE CASCADE,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS active_task_locks (
    lock_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE,
    run_id TEXT NOT NULL UNIQUE,
    folder_id TEXT NOT NULL UNIQUE,
    run_status TEXT NOT NULL CHECK(run_status IN ('QUEUED','RUNNING','PAUSED','INTERRUPTED')),
    locked_at TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE CASCADE,
    FOREIGN KEY (run_id) REFERENCES sync_runs(id) ON DELETE CASCADE,
    FOREIGN KEY (folder_id) REFERENCES sync_folders(folder_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS system_settings (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK(id = 1),
    config_version INTEGER NOT NULL DEFAULT 1,
    maxkb_base_url TEXT NOT NULL DEFAULT '',
    maxkb_normalized_base_url TEXT NOT NULL DEFAULT '',
    maxkb_user_key_ref TEXT NOT NULL DEFAULT '',
    maxkb_version TEXT NOT NULL DEFAULT '',
    maxkb_version_display TEXT NOT NULL DEFAULT '',
    maxkb_last_validated_at TEXT,
    maxkb_validation_success INTEGER NOT NULL DEFAULT 0,
    mineru_enabled INTEGER NOT NULL DEFAULT 0,
    mineru_base_url TEXT NOT NULL DEFAULT '',
    mineru_user_key_ref TEXT NOT NULL DEFAULT '',
    mineru_mode TEXT NOT NULL DEFAULT 'online',
    mineru_last_validated_at TEXT,
    mineru_validation_success INTEGER NOT NULL DEFAULT 0,
    mineru_save_full_result INTEGER NOT NULL DEFAULT 0,
    mineru_result_save_dir TEXT NOT NULL DEFAULT '',
    mineru_cleanup_temp_results INTEGER NOT NULL DEFAULT 1,
    mineru_cleanup_policy TEXT NOT NULL DEFAULT 'never',
    mineru_cleanup_after_days INTEGER NOT NULL DEFAULT 30,
    mineru_cleanup_after_value INTEGER NOT NULL DEFAULT 30,
    mineru_cleanup_after_unit TEXT NOT NULL DEFAULT 'day',
    mineru_cleanup_keep_batches INTEGER NOT NULL DEFAULT 10,
    mineru_cleanup_cron TEXT NOT NULL DEFAULT '0 3 * * *',
    mineru_last_cleanup_at TEXT,
    mineru_last_cleanup_summary TEXT NOT NULL DEFAULT '',
    mineru_last_cleanup_status TEXT NOT NULL DEFAULT '',
    mineru_last_cleanup_deleted_count INTEGER NOT NULL DEFAULT 0,
    mineru_last_cleanup_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO system_settings(id) VALUES (1);

CREATE TABLE IF NOT EXISTS operation_history (
    history_id TEXT PRIMARY KEY,
    task_id TEXT,
    operation_type TEXT NOT NULL,
    operation_detail TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES sync_tasks(task_id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_sync_folders_kb ON sync_folders(kb_id);
CREATE INDEX IF NOT EXISTS idx_sync_folders_cron ON sync_folders(cron_enabled);
CREATE INDEX IF NOT EXISTS idx_sync_folders_enabled ON sync_folders(enabled) WHERE enabled = 1;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_normalized_local_path ON sync_folders(normalized_local_path);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_remote_binding ON sync_folders(normalized_maxkb_base_url, workspace_id, kb_id);
DROP INDEX IF EXISTS uq_sync_files_normalized_path;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_files_normalized_path ON sync_files(folder_id, normalized_relative_path) WHERE normalized_relative_path <> '';
CREATE INDEX IF NOT EXISTS idx_job_queue_claimable ON job_queue(available_at, priority, queued_at, id);
CREATE INDEX IF NOT EXISTS idx_job_queue_task ON job_queue(task_id);
CREATE INDEX IF NOT EXISTS idx_active_task_locks_status ON active_task_locks(run_status, heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_file_attempts_remote_refs ON file_attempts(mineru_task_id, maxkb_batch_task_id, maxkb_document_id);
CREATE INDEX IF NOT EXISTS idx_sync_runs_checkpoint ON sync_runs(status, current_file_ordinal);
CREATE INDEX IF NOT EXISTS idx_sync_files_folder ON sync_files(folder_id);
CREATE INDEX IF NOT EXISTS idx_sync_files_status ON sync_files(file_status);
CREATE INDEX IF NOT EXISTS idx_sync_files_path ON sync_files(folder_id, relative_path);
CREATE INDEX IF NOT EXISTS idx_sync_tasks_folder ON sync_tasks(folder_id);
CREATE INDEX IF NOT EXISTS idx_sync_tasks_status ON sync_tasks(run_status);
CREATE INDEX IF NOT EXISTS idx_sync_tasks_created ON sync_tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sync_runs_task ON sync_runs(task_id, queued_at DESC);
CREATE INDEX IF NOT EXISTS idx_sync_runs_status ON sync_runs(status);
CREATE INDEX IF NOT EXISTS idx_run_files_task ON run_files(task_id);
CREATE INDEX IF NOT EXISTS idx_run_files_file ON run_files(file_id);
CREATE INDEX IF NOT EXISTS idx_run_files_stage ON run_files(processing_stage);
CREATE INDEX IF NOT EXISTS idx_run_files_final ON run_files(task_id, final_status);
CREATE INDEX IF NOT EXISTS idx_file_attempts_run_file ON file_attempts(run_file_id, attempt_no DESC);
CREATE INDEX IF NOT EXISTS idx_file_attempts_status ON file_attempts(status);
CREATE INDEX IF NOT EXISTS idx_job_queue_priority ON job_queue(priority, available_at, queued_at);
CREATE INDEX IF NOT EXISTS idx_active_task_locks_folder ON active_task_locks(folder_id);
CREATE INDEX IF NOT EXISTS idx_operation_history_task ON operation_history(task_id);
CREATE INDEX IF NOT EXISTS idx_operation_history_created ON operation_history(created_at DESC);
`

	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Upgrade databases created by the pre-V2 schema. SQLite cannot add several
	// columns in one ALTER statement, so each additive change is idempotent.
	upgrades := []struct{ table, column, definition string }{
		{"sync_files", "pending_remote_doc_id", "TEXT NOT NULL DEFAULT ''"},
		{"sync_files", "observed_size", "INTEGER NOT NULL DEFAULT 0"},
		{"sync_files", "observed_modified_at", "TEXT"},
		{"run_files", "ordinal", "INTEGER NOT NULL DEFAULT 0"},
		{"active_task_locks", "run_id", "TEXT NOT NULL DEFAULT ''"},
		{"active_task_locks", "run_status", "TEXT NOT NULL DEFAULT 'QUEUED'"},
		{"active_task_locks", "heartbeat_at", "TEXT NOT NULL DEFAULT ''"},
		{"sync_runs", "recovery_count", "INTEGER NOT NULL DEFAULT 0"},
		{"sync_runs", "last_interrupted_at", "TEXT"},
		{"sync_runs", "last_recovery_at", "TEXT"},
		{"sync_runs", "parent_run_id", "TEXT"},
		{"sync_runs", "checkpoint_data", "TEXT NOT NULL DEFAULT '{}'"},
		{"sync_folders", "enabled", "INTEGER NOT NULL DEFAULT 1"},
		{"sync_folders", "disabled_at", "TEXT"},
		{"sync_folders", "sync_delete_local_removed", "INTEGER NOT NULL DEFAULT 0"},
		{"sync_folders", "mineru_retry_count", "INTEGER NOT NULL DEFAULT 3"},
		{"sync_folders", "mineru_request_timeout_ms", "INTEGER NOT NULL DEFAULT 60000"},
		{"sync_folders", "mineru_task_timeout_ms", "INTEGER NOT NULL DEFAULT 300000"},
		{"sync_folders", "mineru_poll_interval_ms", "INTEGER NOT NULL DEFAULT 2000"},
		{"sync_folders", "mineru_save_full_result", "INTEGER NOT NULL DEFAULT 0"},
		{"sync_folders", "mineru_result_save_dir", "TEXT NOT NULL DEFAULT ''"},
		{"system_settings", "mineru_save_full_result", "INTEGER NOT NULL DEFAULT 0"},
		{"system_settings", "mineru_result_save_dir", "TEXT NOT NULL DEFAULT ''"},
		{"system_settings", "mineru_cleanup_temp_results", "INTEGER NOT NULL DEFAULT 1"},
		{"system_settings", "mineru_cleanup_policy", "TEXT NOT NULL DEFAULT 'never'"},
		{"system_settings", "mineru_cleanup_after_days", "INTEGER NOT NULL DEFAULT 30"},
		{"system_settings", "mineru_cleanup_after_value", "INTEGER NOT NULL DEFAULT 30"},
		{"system_settings", "mineru_cleanup_after_unit", "TEXT NOT NULL DEFAULT 'day'"},
		{"system_settings", "mineru_cleanup_keep_batches", "INTEGER NOT NULL DEFAULT 10"},
		{"system_settings", "mineru_cleanup_cron", "TEXT NOT NULL DEFAULT '0 3 * * *'"},
		{"system_settings", "mineru_last_cleanup_at", "TEXT"},
		{"system_settings", "mineru_last_cleanup_summary", "TEXT NOT NULL DEFAULT ''"},
		{"system_settings", "mineru_last_cleanup_status", "TEXT NOT NULL DEFAULT ''"},
		{"system_settings", "mineru_last_cleanup_deleted_count", "INTEGER NOT NULL DEFAULT 0"},
		{"system_settings", "mineru_last_cleanup_error", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "include_patterns", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "exclude_patterns", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "mineru_file_extensions", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "next_execution_at", "TEXT"},
		{"sync_folders", "normalized_local_path", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "maxkb_base_url_snapshot", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "normalized_maxkb_base_url", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "workspace_name", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "knowledge_folder_id", "TEXT NOT NULL DEFAULT ''"},
		{"sync_folders", "knowledge_name", "TEXT NOT NULL DEFAULT ''"},
		{"sync_files", "normalized_relative_path", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "mineru_batch_id", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "mineru_mode", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "markdown_main_file_path", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "maxkb_document_status", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "document_url", "TEXT NOT NULL DEFAULT ''"},
		{"file_attempts", "phase_timings_json", "TEXT NOT NULL DEFAULT '{}'"},
	}
	for _, u := range upgrades {
		if err := db.ensureColumn(u.table, u.column, u.definition); err != nil {
			return err
		}
	}

	// Complete the additive upgrade after all columns exist. These statements
	// are deliberately idempotent so InitSchema is safe on every startup.
	backfill := `
INSERT OR IGNORE INTO sync_runs(id,task_id,folder_id,trigger_type,status,queued_at,started_at,completed_at,total_files,success_count,failed_count,skipped_count,error_summary)
SELECT task_id,task_id,folder_id,trigger_type,
 CASE WHEN run_status IN ('QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','STOP_REQUESTED','STOPPED','SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED','INTERRUPTED','CANCELLED') THEN run_status ELSE 'INTERRUPTED' END,
 created_at,started_at,completed_at,total_files,success_count,failed_count,skipped_count,COALESCE(error_message,'') FROM sync_tasks;
DELETE FROM active_task_locks WHERE task_id IN (SELECT task_id FROM sync_tasks WHERE run_status IN ('SUCCESS','COMPLETED','PARTIAL_SUCCESS','FAILED','STOPPED','CANCELLED'));
UPDATE active_task_locks SET run_id=CASE WHEN run_id='' THEN task_id ELSE run_id END,
 run_status=COALESCE((SELECT CASE WHEN run_status IN ('RUNNING','PAUSED','INTERRUPTED') THEN run_status ELSE 'QUEUED' END FROM sync_tasks WHERE sync_tasks.task_id=active_task_locks.task_id),'QUEUED'),
 heartbeat_at=CASE WHEN heartbeat_at='' THEN locked_at ELSE heartbeat_at END;
DELETE FROM active_task_locks WHERE rowid NOT IN (SELECT MAX(rowid) FROM active_task_locks GROUP BY folder_id);
UPDATE sync_folders SET normalized_local_path=CASE WHEN normalized_local_path='' THEN replace(local_path, '\\', '/') ELSE normalized_local_path END, normalized_maxkb_base_url=CASE WHEN normalized_maxkb_base_url='' THEN rtrim(COALESCE(maxkb_base_url_snapshot,''), '/') ELSE normalized_maxkb_base_url END;
UPDATE sync_files SET normalized_relative_path=CASE WHEN normalized_relative_path='' THEN replace(relative_path, '\\', '/') ELSE normalized_relative_path END;
INSERT OR IGNORE INTO job_queue(run_id,task_id,priority,queued_at,available_at)
SELECT id,task_id,10,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM sync_runs WHERE status='QUEUED';
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_task_locks_run ON active_task_locks(run_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_task_locks_folder ON active_task_locks(folder_id);
CREATE INDEX IF NOT EXISTS idx_sync_folders_enabled ON sync_folders(enabled) WHERE enabled = 1;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_normalized_local_path ON sync_folders(normalized_local_path);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_folders_remote_binding ON sync_folders(normalized_maxkb_base_url, workspace_id, kb_id);
DROP INDEX IF EXISTS uq_sync_files_normalized_path;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sync_files_normalized_path ON sync_files(folder_id, normalized_relative_path) WHERE normalized_relative_path <> '';
CREATE INDEX IF NOT EXISTS idx_job_queue_claimable ON job_queue(available_at, priority, queued_at, id);
CREATE INDEX IF NOT EXISTS idx_job_queue_task ON job_queue(task_id);
CREATE INDEX IF NOT EXISTS idx_active_task_locks_status ON active_task_locks(run_status, heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_file_attempts_remote_refs ON file_attempts(mineru_task_id, maxkb_batch_task_id, maxkb_document_id);
CREATE INDEX IF NOT EXISTS idx_sync_runs_checkpoint ON sync_runs(status, current_file_ordinal);
`
	if _, err := db.conn.Exec(backfill); err != nil {
		return fmt.Errorf("failed to backfill durable schema: %w", err)
	}
	return nil
}

func (db *DB) ensureColumn(table, column, definition string) error {
	rows, err := db.conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", table, err)
	}
	exists := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			exists = true
		}
	}
	rows.Close()
	if exists {
		return nil
	}
	if _, err := db.conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("failed to add %s.%s: %w", table, column, err)
	}
	return nil
}

// GetTableNames 获取所有表名
func (db *DB) GetTableNames() ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query table names: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan table name: %w", err)
		}
		tables = append(tables, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table names: %w", err)
	}

	return tables, nil
}

// TableExists 检查表是否存在
func (db *DB) TableExists(tableName string) (bool, error) {
	var count int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name=?
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check table existence: %w", err)
	}
	return count > 0, nil
}

// DropAllTables 删除所有表（用于测试清理）
func (db *DB) DropAllTables() error {
	tables, err := db.GetTableNames()
	if err != nil {
		return err
	}

	// 按依赖顺序倒序删除
	sort.Sort(sort.Reverse(sort.StringSlice(tables)))

	// 先禁用外键检查
	if _, err := db.conn.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("failed to disable foreign keys: %w", err)
	}

	for _, table := range tables {
		if _, err := db.conn.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
	}

	// 重新启用外键检查
	if _, err := db.conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	return nil
}

// CheckForeignKeys 检查外键完整性
func (db *DB) CheckForeignKeys() error {
	rows, err := db.conn.Query("PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("failed to check foreign keys: %w", err)
	}
	defer rows.Close()

	type FKError struct {
		Table  string
		RowID  int64
		Parent string
		FKIdx  int
	}

	var errors []FKError
	for rows.Next() {
		var fkErr FKError
		if err := rows.Scan(&fkErr.Table, &fkErr.RowID, &fkErr.Parent, &fkErr.FKIdx); err != nil {
			return fmt.Errorf("failed to scan foreign key error: %w", err)
		}
		errors = append(errors, fkErr)
	}

	if len(errors) > 0 {
		return fmt.Errorf("foreign key violations found: %+v", errors)
	}

	return nil
}
