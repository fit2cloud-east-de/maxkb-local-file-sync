package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
)

// Sentinel errors used by the API layer to turn database details into
// actionable user-facing messages without exposing SQLite constraint text.
var (
	ErrSyncFolderNotFound     = errors.New("sync folder not found")
	ErrSyncFolderPathConflict = errors.New("sync folder local path already exists")
)

// SyncFolder 同步文件夹实体
//
// The MaxKB snapshot fields are deliberately kept with the task binding. They
// make a task auditable even when the global MaxKB configuration is changed.
type SyncFolder struct {
	FolderID               string
	Name                   string
	LocalPath              string
	KBId                   string
	WorkspaceID            string
	MaxKBBaseURLSnapshot   string
	NormalizedMaxKBBaseURL string
	WorkspaceName          string
	KnowledgeFolderID      string
	KnowledgeName          string
	EnableMinerU           bool
	CronExpression         string
	CronEnabled            bool
	Enabled                bool
	DisabledAt             *time.Time
	SyncDeleteLocalRemoved bool

	// MinerU 高级配置
	MinerURetryCount     int
	MinerURequestTimeout int
	MinerUTaskTimeout    int
	MinerUPollInterval   int
	MinerUSaveFullResult bool
	MinerUResultSaveDir  string

	// 文件筛选
	IncludePatterns      string
	ExcludePatterns      string
	MinerUFileExtensions string

	// 下次执行时间
	NextExecutionAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

type rowScanner interface {
	Scan(dest ...any) error
}

// Keep one column order for every read path. This prevents a new snapshot
// field from silently being omitted by one of the repository queries. The
// legacy sync_folders.mineru_mode and mineru_endpoint columns intentionally
// remain in the database schema for upgrade compatibility, but are not part of
// the current task contract and are never read or written by this repository.
const syncFolderSelectColumns = `
	folder_id, name, local_path, kb_id, workspace_id,
	COALESCE(maxkb_base_url_snapshot, '') AS maxkb_base_url_snapshot,
	COALESCE(normalized_maxkb_base_url, '') AS normalized_maxkb_base_url,
	COALESCE(workspace_name, '') AS workspace_name,
	COALESCE(knowledge_folder_id, '') AS knowledge_folder_id,
	COALESCE(knowledge_name, '') AS knowledge_name,
	enable_mineru,
	cron_expression, cron_enabled,
	COALESCE(enabled, 1) AS enabled, disabled_at,
	COALESCE(sync_delete_local_removed, 0) AS sync_delete_local_removed,
	COALESCE(mineru_retry_count, 3) AS mineru_retry_count,
	COALESCE(mineru_request_timeout_ms, 60000) AS mineru_request_timeout_ms,
	COALESCE(mineru_task_timeout_ms, 300000) AS mineru_task_timeout_ms,
	COALESCE(mineru_poll_interval_ms, 2000) AS mineru_poll_interval_ms,
	COALESCE(mineru_save_full_result, 0) AS mineru_save_full_result,
	COALESCE(mineru_result_save_dir, '') AS mineru_result_save_dir,
	COALESCE(include_patterns, '') AS include_patterns,
	COALESCE(exclude_patterns, '') AS exclude_patterns,
	COALESCE(mineru_file_extensions, '') AS mineru_file_extensions,
	next_execution_at, created_at, updated_at`

// SyncFolderRepository 同步文件夹仓储接口
type SyncFolderRepository interface {
	Create(ctx context.Context, folder *SyncFolder) error
	Update(ctx context.Context, folder *SyncFolder) error
	Delete(ctx context.Context, folderID string) error
	GetByID(ctx context.Context, folderID string) (*SyncFolder, error)
	GetByLocalPath(ctx context.Context, localPath string) (*SyncFolder, error)
	List(ctx context.Context) ([]*SyncFolder, error)
	ListByKBId(ctx context.Context, kbID string) ([]*SyncFolder, error)
	ListCronEnabled(ctx context.Context) ([]*SyncFolder, error)
	SetEnabled(ctx context.Context, folderID string, enabled bool) error
}

type syncFolderRepo struct{ db *db.DB }

func NewSyncFolderRepository(database *db.DB) SyncFolderRepository {
	return &syncFolderRepo{db: database}
}

func (r *syncFolderRepo) Create(ctx context.Context, folder *SyncFolder) error {
	if folder == nil {
		return fmt.Errorf("sync folder is nil")
	}
	normalizeFolderIdentity(folder)
	query := `
		INSERT INTO sync_folders (
			folder_id, name, local_path, normalized_local_path, kb_id, workspace_id,
			maxkb_base_url_snapshot, normalized_maxkb_base_url, workspace_name,
			knowledge_folder_id, knowledge_name,
			enable_mineru,
			cron_expression, cron_enabled, enabled, sync_delete_local_removed,
			mineru_retry_count, mineru_request_timeout_ms, mineru_task_timeout_ms,
			mineru_poll_interval_ms, mineru_save_full_result, mineru_result_save_dir,
			include_patterns, exclude_patterns, mineru_file_extensions,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.Exec(query,
		folder.FolderID, folder.Name, folder.LocalPath, normalizeLocalPath(folder.LocalPath),
		folder.KBId, folder.WorkspaceID, folder.MaxKBBaseURLSnapshot, folder.NormalizedMaxKBBaseURL,
		folder.WorkspaceName, folder.KnowledgeFolderID, folder.KnowledgeName,
		boolToInt(folder.EnableMinerU),
		folder.CronExpression, boolToInt(folder.CronEnabled), boolToInt(folder.Enabled), boolToInt(folder.SyncDeleteLocalRemoved),
		folder.MinerURetryCount, folder.MinerURequestTimeout, folder.MinerUTaskTimeout, folder.MinerUPollInterval,
		boolToInt(folder.MinerUSaveFullResult), folder.MinerUResultSaveDir,
		folder.IncludePatterns, folder.ExcludePatterns, folder.MinerUFileExtensions,
		folder.CreatedAt.Format(time.RFC3339Nano), folder.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if isLocalPathUniqueConstraint(err) {
			return fmt.Errorf("%w: %s", ErrSyncFolderPathConflict, folder.LocalPath)
		}
		return fmt.Errorf("failed to create sync folder: %w", err)
	}
	return nil
}

func (r *syncFolderRepo) Update(ctx context.Context, folder *SyncFolder) error {
	if folder == nil {
		return fmt.Errorf("sync folder is nil")
	}
	normalizeFolderIdentity(folder)
	query := `
		UPDATE sync_folders SET
			name = ?, local_path = ?, normalized_local_path = ?, kb_id = ?, workspace_id = ?,
			maxkb_base_url_snapshot = ?, normalized_maxkb_base_url = ?, workspace_name = ?,
			knowledge_folder_id = ?, knowledge_name = ?,
			enable_mineru = ?,
			cron_expression = ?, cron_enabled = ?, sync_delete_local_removed = ?,
			mineru_retry_count = ?, mineru_request_timeout_ms = ?, mineru_task_timeout_ms = ?,
			mineru_poll_interval_ms = ?, mineru_save_full_result = ?, mineru_result_save_dir = ?,
			include_patterns = ?, exclude_patterns = ?, mineru_file_extensions = ?,
			updated_at = ?
		WHERE folder_id = ?`
	result, err := r.db.Exec(query,
		folder.Name, folder.LocalPath, normalizeLocalPath(folder.LocalPath), folder.KBId, folder.WorkspaceID,
		folder.MaxKBBaseURLSnapshot, folder.NormalizedMaxKBBaseURL, folder.WorkspaceName,
		folder.KnowledgeFolderID, folder.KnowledgeName,
		boolToInt(folder.EnableMinerU),
		folder.CronExpression, boolToInt(folder.CronEnabled), boolToInt(folder.SyncDeleteLocalRemoved),
		folder.MinerURetryCount, folder.MinerURequestTimeout, folder.MinerUTaskTimeout, folder.MinerUPollInterval,
		boolToInt(folder.MinerUSaveFullResult), folder.MinerUResultSaveDir,
		folder.IncludePatterns, folder.ExcludePatterns, folder.MinerUFileExtensions,
		time.Now().UTC().Format(time.RFC3339Nano), folder.FolderID)
	if err != nil {
		if isLocalPathUniqueConstraint(err) {
			return fmt.Errorf("%w: %s", ErrSyncFolderPathConflict, folder.LocalPath)
		}
		return fmt.Errorf("failed to update sync folder: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("sync folder not found: %s", folder.FolderID)
	}
	return nil
}

func (r *syncFolderRepo) Delete(ctx context.Context, folderID string) error {
	result, err := r.db.Exec(`DELETE FROM sync_folders WHERE folder_id = ?`, folderID)
	if err != nil {
		return fmt.Errorf("failed to delete sync folder: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("sync folder not found: %s", folderID)
	}
	return nil
}

func (r *syncFolderRepo) GetByID(ctx context.Context, folderID string) (*SyncFolder, error) {
	folder := &SyncFolder{}
	err := scanFolderRow(r.db.QueryRow(`SELECT `+syncFolderSelectColumns+` FROM sync_folders WHERE folder_id = ?`, folderID), folder)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", ErrSyncFolderNotFound, folderID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query sync folder: %w", err)
	}
	return folder, nil
}

func (r *syncFolderRepo) GetByLocalPath(ctx context.Context, localPath string) (*SyncFolder, error) {
	folder := &SyncFolder{}
	normalized := normalizeLocalPath(localPath)
	err := scanFolderRow(r.db.QueryRow(`SELECT `+syncFolderSelectColumns+` FROM sync_folders WHERE normalized_local_path = ? OR local_path = ?`, normalized, localPath), folder)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w for path: %s", ErrSyncFolderNotFound, localPath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query sync folder: %w", err)
	}
	return folder, nil
}

func isLocalPathUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed: sync_folders.normalized_local_path") ||
		strings.Contains(message, "UNIQUE constraint failed: sync_folders.local_path")
}

func (r *syncFolderRepo) List(ctx context.Context) ([]*SyncFolder, error) {
	rows, err := r.db.Query(`SELECT ` + syncFolderSelectColumns + ` FROM sync_folders ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list sync folders: %w", err)
	}
	defer rows.Close()
	return r.scanFolders(rows)
}

func (r *syncFolderRepo) ListByKBId(ctx context.Context, kbID string) ([]*SyncFolder, error) {
	rows, err := r.db.Query(`SELECT `+syncFolderSelectColumns+` FROM sync_folders WHERE kb_id = ? ORDER BY created_at DESC`, kbID)
	if err != nil {
		return nil, fmt.Errorf("failed to list sync folders by kb_id: %w", err)
	}
	defer rows.Close()
	return r.scanFolders(rows)
}

func (r *syncFolderRepo) ListCronEnabled(ctx context.Context) ([]*SyncFolder, error) {
	rows, err := r.db.Query(`SELECT ` + syncFolderSelectColumns + ` FROM sync_folders WHERE cron_enabled = 1 AND COALESCE(enabled, 1) = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list cron enabled folders: %w", err)
	}
	defer rows.Close()
	return r.scanFolders(rows)
}

func (r *syncFolderRepo) scanFolders(rows *sql.Rows) ([]*SyncFolder, error) {
	folders := make([]*SyncFolder, 0)
	for rows.Next() {
		folder := &SyncFolder{}
		if err := scanFolderRow(rows, folder); err != nil {
			return nil, fmt.Errorf("failed to scan folder: %w", err)
		}
		folders = append(folders, folder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate folders: %w", err)
	}
	return folders, nil
}

func scanFolderRow(scanner rowScanner, folder *SyncFolder) error {
	var enableMinerU, cronEnabled, enabled, syncDeleteLocalRemoved, minerUSaveFullResult int
	var disabledAt, nextExecutionAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&folder.FolderID, &folder.Name, &folder.LocalPath, &folder.KBId, &folder.WorkspaceID,
		&folder.MaxKBBaseURLSnapshot, &folder.NormalizedMaxKBBaseURL, &folder.WorkspaceName,
		&folder.KnowledgeFolderID, &folder.KnowledgeName,
		&enableMinerU, &folder.CronExpression, &cronEnabled,
		&enabled, &disabledAt, &syncDeleteLocalRemoved, &folder.MinerURetryCount,
		&folder.MinerURequestTimeout, &folder.MinerUTaskTimeout, &folder.MinerUPollInterval,
		&minerUSaveFullResult, &folder.MinerUResultSaveDir, &folder.IncludePatterns, &folder.ExcludePatterns,
		&folder.MinerUFileExtensions, &nextExecutionAt, &createdAt, &updatedAt,
	); err != nil {
		return err
	}
	folder.EnableMinerU = intToBool(enableMinerU)
	folder.CronEnabled = intToBool(cronEnabled)
	folder.Enabled = intToBool(enabled)
	folder.SyncDeleteLocalRemoved = intToBool(syncDeleteLocalRemoved)
	folder.MinerUSaveFullResult = intToBool(minerUSaveFullResult)
	folder.DisabledAt = parseNullableTime(disabledAt)
	folder.NextExecutionAt = parseNullableTime(nextExecutionAt)
	folder.CreatedAt = parseTime(createdAt)
	folder.UpdatedAt = parseTime(updatedAt)
	return nil
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	t := parseTime(value.String)
	return &t
}

func parseTime(value string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func normalizeFolderIdentity(folder *SyncFolder) {
	folder.NormalizedMaxKBBaseURL = strings.TrimRight(strings.TrimSpace(folder.MaxKBBaseURLSnapshot), "/")
}

func normalizeLocalPath(path string) string {
	return strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func intToBool(i int) bool { return i != 0 }

func (r *syncFolderRepo) SetEnabled(ctx context.Context, folderID string, enabled bool) error {
	var query string
	var args []any
	if enabled {
		query = `UPDATE sync_folders SET enabled = 1, disabled_at = NULL, updated_at = ? WHERE folder_id = ?`
		args = []any{time.Now().UTC().Format(time.RFC3339Nano), folderID}
	} else {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		query = `UPDATE sync_folders SET enabled = 0, disabled_at = ?, updated_at = ? WHERE folder_id = ?`
		args = []any{now, now, folderID}
	}
	result, err := r.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to set enabled status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("sync folder not found: %s", folderID)
	}
	return nil
}
