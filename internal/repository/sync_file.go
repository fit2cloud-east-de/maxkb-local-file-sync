package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

// SyncFile 同步文件实体
type SyncFile struct {
	FileID         string
	FolderID       string
	RelativePath   string
	FileStatus     types.FileStatus
	ObservedMD5    string
	LastSuccessMD5 string
	RemoteDocID    string
	LastSyncedAt   *time.Time
	LastCheckedAt  *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SyncFileRepository 同步文件仓储接口
type SyncFileRepository interface {
	// 创建
	Create(ctx context.Context, file *SyncFile) error
	BatchCreate(ctx context.Context, files []*SyncFile) error

	// 更新
	Update(ctx context.Context, file *SyncFile) error
	UpdateStatus(ctx context.Context, fileID string, status types.FileStatus) error
	UpdateMD5(ctx context.Context, fileID, observedMD5, lastSuccessMD5 string) error
	UpdateRemoteDocID(ctx context.Context, fileID, remoteDocID string) error

	// 删除
	Delete(ctx context.Context, fileID string) error
	DeleteByFolder(ctx context.Context, folderID string) error

	// 查询
	GetByID(ctx context.Context, fileID string) (*SyncFile, error)
	GetByPath(ctx context.Context, folderID, relativePath string) (*SyncFile, error)
	ListByFolder(ctx context.Context, folderID string) ([]*SyncFile, error)
	ListByStatus(ctx context.Context, folderID string, status types.FileStatus) ([]*SyncFile, error)
	ListPendingChanges(ctx context.Context, folderID string) ([]*SyncFile, error)

	// 统计
	CountByFolder(ctx context.Context, folderID string) (int, error)
	CountByStatus(ctx context.Context, folderID string, status types.FileStatus) (int, error)
}

// syncFileRepo 同步文件仓储实现
type syncFileRepo struct {
	db *db.DB
}

// NewSyncFileRepository 创建同步文件仓储
func NewSyncFileRepository(database *db.DB) SyncFileRepository {
	return &syncFileRepo{db: database}
}

// Create 创建同步文件
func (r *syncFileRepo) Create(ctx context.Context, file *SyncFile) error {
	query := `
		INSERT INTO sync_files (
			file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.Exec(query,
		file.FileID,
		file.FolderID,
		file.RelativePath,
		string(file.FileStatus),
		file.ObservedMD5,
		file.LastSuccessMD5,
		file.RemoteDocID,
		timeToString(file.LastSyncedAt),
		timeToString(file.LastCheckedAt),
		file.CreatedAt.Format(time.RFC3339),
		file.UpdatedAt.Format(time.RFC3339),
	)

	if err != nil {
		return fmt.Errorf("failed to create sync file: %w", err)
	}

	return nil
}

// BatchCreate 批量创建同步文件
func (r *syncFileRepo) BatchCreate(ctx context.Context, files []*SyncFile) error {
	if len(files) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO sync_files (
			file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, file := range files {
		_, err := stmt.Exec(
			file.FileID,
			file.FolderID,
			file.RelativePath,
			string(file.FileStatus),
			file.ObservedMD5,
			file.LastSuccessMD5,
			file.RemoteDocID,
			timeToString(file.LastSyncedAt),
			timeToString(file.LastCheckedAt),
			file.CreatedAt.Format(time.RFC3339),
			file.UpdatedAt.Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("failed to insert file %s: %w", file.FileID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Update 更新同步文件
func (r *syncFileRepo) Update(ctx context.Context, file *SyncFile) error {
	query := `
		UPDATE sync_files SET
			relative_path = ?, file_status = ?,
			observed_md5 = ?, last_success_md5 = ?, remote_doc_id = ?,
			last_synced_at = ?, last_checked_at = ?, updated_at = ?
		WHERE file_id = ?
	`

	result, err := r.db.Exec(query,
		file.RelativePath,
		string(file.FileStatus),
		file.ObservedMD5,
		file.LastSuccessMD5,
		file.RemoteDocID,
		timeToString(file.LastSyncedAt),
		timeToString(file.LastCheckedAt),
		time.Now().Format(time.RFC3339),
		file.FileID,
	)

	if err != nil {
		return fmt.Errorf("failed to update sync file: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync file not found: %s", file.FileID)
	}

	return nil
}

// UpdateStatus 更新文件状态
func (r *syncFileRepo) UpdateStatus(ctx context.Context, fileID string, status types.FileStatus) error {
	query := `UPDATE sync_files SET file_status = ?, updated_at = ? WHERE file_id = ?`

	result, err := r.db.Exec(query, string(status), time.Now().Format(time.RFC3339), fileID)
	if err != nil {
		return fmt.Errorf("failed to update file status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync file not found: %s", fileID)
	}

	return nil
}

// UpdateMD5 更新文件 MD5
func (r *syncFileRepo) UpdateMD5(ctx context.Context, fileID, observedMD5, lastSuccessMD5 string) error {
	query := `
		UPDATE sync_files SET
			observed_md5 = ?, last_success_md5 = ?, updated_at = ?
		WHERE file_id = ?
	`

	result, err := r.db.Exec(query, observedMD5, lastSuccessMD5, time.Now().Format(time.RFC3339), fileID)
	if err != nil {
		return fmt.Errorf("failed to update file MD5: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync file not found: %s", fileID)
	}

	return nil
}

// UpdateRemoteDocID 更新远程文档 ID
func (r *syncFileRepo) UpdateRemoteDocID(ctx context.Context, fileID, remoteDocID string) error {
	query := `UPDATE sync_files SET remote_doc_id = ?, updated_at = ? WHERE file_id = ?`

	result, err := r.db.Exec(query, remoteDocID, time.Now().Format(time.RFC3339), fileID)
	if err != nil {
		return fmt.Errorf("failed to update remote doc id: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync file not found: %s", fileID)
	}

	return nil
}

// Delete 删除同步文件
func (r *syncFileRepo) Delete(ctx context.Context, fileID string) error {
	query := `DELETE FROM sync_files WHERE file_id = ?`

	result, err := r.db.Exec(query, fileID)
	if err != nil {
		return fmt.Errorf("failed to delete sync file: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync file not found: %s", fileID)
	}

	return nil
}

// DeleteByFolder 删除文件夹下所有文件
func (r *syncFileRepo) DeleteByFolder(ctx context.Context, folderID string) error {
	query := `DELETE FROM sync_files WHERE folder_id = ?`

	_, err := r.db.Exec(query, folderID)
	if err != nil {
		return fmt.Errorf("failed to delete files by folder: %w", err)
	}

	return nil
}

// GetByID 根据 ID 查询
func (r *syncFileRepo) GetByID(ctx context.Context, fileID string) (*SyncFile, error) {
	query := `
		SELECT file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		FROM sync_files
		WHERE file_id = ?
	`

	return r.scanFile(r.db.QueryRow(query, fileID))
}

// GetByPath 根据路径查询
func (r *syncFileRepo) GetByPath(ctx context.Context, folderID, relativePath string) (*SyncFile, error) {
	query := `
		SELECT file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		FROM sync_files
		WHERE folder_id = ? AND relative_path = ?
	`

	return r.scanFile(r.db.QueryRow(query, folderID, relativePath))
}

// ListByFolder 列出文件夹下所有文件
func (r *syncFileRepo) ListByFolder(ctx context.Context, folderID string) ([]*SyncFile, error) {
	query := `
		SELECT file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		FROM sync_files
		WHERE folder_id = ?
		ORDER BY relative_path
	`

	rows, err := r.db.Query(query, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list files by folder: %w", err)
	}
	defer rows.Close()

	return r.scanFiles(rows)
}

// ListByStatus 根据状态列出文件
func (r *syncFileRepo) ListByStatus(ctx context.Context, folderID string, status types.FileStatus) ([]*SyncFile, error) {
	query := `
		SELECT file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		FROM sync_files
		WHERE folder_id = ? AND file_status = ?
		ORDER BY relative_path
	`

	rows, err := r.db.Query(query, folderID, string(status))
	if err != nil {
		return nil, fmt.Errorf("failed to list files by status: %w", err)
	}
	defer rows.Close()

	return r.scanFiles(rows)
}

// ListPendingChanges 列出待同步的文件
func (r *syncFileRepo) ListPendingChanges(ctx context.Context, folderID string) ([]*SyncFile, error) {
	query := `
		SELECT file_id, folder_id, relative_path, file_status,
			observed_md5, last_success_md5, remote_doc_id,
			last_synced_at, last_checked_at, created_at, updated_at
		FROM sync_files
		WHERE folder_id = ? AND file_status IN (?, ?, ?)
		ORDER BY relative_path
	`

	rows, err := r.db.Query(query, folderID,
		string(types.FileStatusPending),
		string(types.FileStatusStaleRemoteExists),
		string(types.FileStatusNeedsDelete),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending changes: %w", err)
	}
	defer rows.Close()

	return r.scanFiles(rows)
}

// CountByFolder 统计文件夹下文件数量
func (r *syncFileRepo) CountByFolder(ctx context.Context, folderID string) (int, error) {
	query := `SELECT COUNT(*) FROM sync_files WHERE folder_id = ?`

	var count int
	err := r.db.QueryRow(query, folderID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count files by folder: %w", err)
	}

	return count, nil
}

// CountByStatus 统计指定状态的文件数量
func (r *syncFileRepo) CountByStatus(ctx context.Context, folderID string, status types.FileStatus) (int, error) {
	query := `SELECT COUNT(*) FROM sync_files WHERE folder_id = ? AND file_status = ?`

	var count int
	err := r.db.QueryRow(query, folderID, string(status)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count files by status: %w", err)
	}

	return count, nil
}

// scanFile 扫描单个文件
func (r *syncFileRepo) scanFile(row *sql.Row) (*SyncFile, error) {
	file := &SyncFile{}
	var fileStatus string
	var lastSyncedAt, lastCheckedAt, createdAt, updatedAt sql.NullString

	err := row.Scan(
		&file.FileID,
		&file.FolderID,
		&file.RelativePath,
		&fileStatus,
		&file.ObservedMD5,
		&file.LastSuccessMD5,
		&file.RemoteDocID,
		&lastSyncedAt,
		&lastCheckedAt,
		&createdAt,
		&updatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("sync file not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan sync file: %w", err)
	}

	file.FileStatus = types.FileStatus(fileStatus)
	file.LastSyncedAt = stringToTime(lastSyncedAt)
	file.LastCheckedAt = stringToTime(lastCheckedAt)
	file.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	file.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)

	return file, nil
}

// scanFiles 扫描多个文件
func (r *syncFileRepo) scanFiles(rows *sql.Rows) ([]*SyncFile, error) {
	files := make([]*SyncFile, 0)

	for rows.Next() {
		file := &SyncFile{}
		var fileStatus string
		var lastSyncedAt, lastCheckedAt, createdAt, updatedAt sql.NullString

		err := rows.Scan(
			&file.FileID,
			&file.FolderID,
			&file.RelativePath,
			&fileStatus,
			&file.ObservedMD5,
			&file.LastSuccessMD5,
			&file.RemoteDocID,
			&lastSyncedAt,
			&lastCheckedAt,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan file: %w", err)
		}

		file.FileStatus = types.FileStatus(fileStatus)
		file.LastSyncedAt = stringToTime(lastSyncedAt)
		file.LastCheckedAt = stringToTime(lastCheckedAt)
		file.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
		file.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)

		files = append(files, file)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate files: %w", err)
	}

	return files, nil
}

// 辅助函数
func timeToString(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{
		String: t.Format(time.RFC3339),
		Valid:  true,
	}
}

func stringToTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return nil
	}
	return &t
}
