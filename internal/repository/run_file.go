package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

// RunFile 运行文件实体
type RunFile struct {
	RunFileID          string
	TaskID             string
	FileID             string
	ProcessingStage    types.ProcessingStage
	ControlState       types.ControlState
	FinalStatus        types.FileFinalStatus
	SnapshotPath       string
	SnapshotSize       int64
	SnapshotModifiedAt int64
	SnapshotMD5        string
	ErrorMessage       string
	CreatedAt          time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
}

// RunFileRepository 运行文件仓储接口
type RunFileRepository interface {
	// 创建
	Create(ctx context.Context, runFile *RunFile) error
	BatchCreate(ctx context.Context, runFiles []*RunFile) error

	// 更新
	Update(ctx context.Context, runFile *RunFile) error
	UpdateStage(ctx context.Context, runFileID string, stage types.ProcessingStage) error
	UpdateControlState(ctx context.Context, runFileID string, state types.ControlState) error
	UpdateFinalStatus(ctx context.Context, runFileID string, status types.FileFinalStatus, errMsg string) error

	// 查询
	GetByID(ctx context.Context, runFileID string) (*RunFile, error)
	ListByTask(ctx context.Context, taskID string) ([]*RunFile, error)
	ListByStage(ctx context.Context, taskID string, stage types.ProcessingStage) ([]*RunFile, error)
	ListPending(ctx context.Context, taskID string) ([]*RunFile, error)

	// 删除
	Delete(ctx context.Context, runFileID string) error
	DeleteByTask(ctx context.Context, taskID string) error
}

// runFileRepo 运行文件仓储实现
type runFileRepo struct {
	db *db.DB
}

// NewRunFileRepository 创建运行文件仓储
func NewRunFileRepository(database *db.DB) RunFileRepository {
	return &runFileRepo{db: database}
}

// Create 创建运行文件
func (r *runFileRepo) Create(ctx context.Context, runFile *RunFile) error {
	query := `
		INSERT INTO run_files (
			run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.Exec(query,
		runFile.RunFileID,
		runFile.TaskID,
		runFile.FileID,
		string(runFile.ProcessingStage),
		string(runFile.ControlState),
		string(runFile.FinalStatus),
		runFile.SnapshotPath,
		runFile.SnapshotSize,
		runFile.SnapshotModifiedAt,
		runFile.SnapshotMD5,
		runFile.ErrorMessage,
		runFile.CreatedAt.Format(time.RFC3339),
		timeToString(runFile.StartedAt),
		timeToString(runFile.CompletedAt),
	)

	if err != nil {
		return fmt.Errorf("failed to create run file: %w", err)
	}

	return nil
}

// BatchCreate 批量创建运行文件
func (r *runFileRepo) BatchCreate(ctx context.Context, runFiles []*RunFile) error {
	if len(runFiles) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO run_files (
			run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, rf := range runFiles {
		_, err := stmt.Exec(
			rf.RunFileID,
			rf.TaskID,
			rf.FileID,
			string(rf.ProcessingStage),
			string(rf.ControlState),
			string(rf.FinalStatus),
			rf.SnapshotPath,
			rf.SnapshotSize,
			rf.SnapshotModifiedAt,
			rf.SnapshotMD5,
			rf.ErrorMessage,
			rf.CreatedAt.Format(time.RFC3339),
			timeToString(rf.StartedAt),
			timeToString(rf.CompletedAt),
		)
		if err != nil {
			return fmt.Errorf("failed to insert run file %s: %w", rf.RunFileID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Update 更新运行文件
func (r *runFileRepo) Update(ctx context.Context, runFile *RunFile) error {
	query := `
		UPDATE run_files SET
			processing_stage = ?, control_state = ?, final_status = ?,
			snapshot_path = ?, snapshot_size = ?, snapshot_modified_at = ?,
			snapshot_md5 = ?, error_message = ?,
			started_at = ?, completed_at = ?
		WHERE run_file_id = ?
	`

	result, err := r.db.Exec(query,
		string(runFile.ProcessingStage),
		string(runFile.ControlState),
		string(runFile.FinalStatus),
		runFile.SnapshotPath,
		runFile.SnapshotSize,
		runFile.SnapshotModifiedAt,
		runFile.SnapshotMD5,
		runFile.ErrorMessage,
		timeToString(runFile.StartedAt),
		timeToString(runFile.CompletedAt),
		runFile.RunFileID,
	)

	if err != nil {
		return fmt.Errorf("failed to update run file: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("run file not found: %s", runFile.RunFileID)
	}

	return nil
}

// UpdateStage 更新处理阶段
func (r *runFileRepo) UpdateStage(ctx context.Context, runFileID string, stage types.ProcessingStage) error {
	query := `UPDATE run_files SET processing_stage = ? WHERE run_file_id = ?`

	result, err := r.db.Exec(query, string(stage), runFileID)
	if err != nil {
		return fmt.Errorf("failed to update stage: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("run file not found: %s", runFileID)
	}

	return nil
}

// UpdateControlState 更新控制状态
func (r *runFileRepo) UpdateControlState(ctx context.Context, runFileID string, state types.ControlState) error {
	query := `UPDATE run_files SET control_state = ? WHERE run_file_id = ?`

	result, err := r.db.Exec(query, string(state), runFileID)
	if err != nil {
		return fmt.Errorf("failed to update control state: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("run file not found: %s", runFileID)
	}

	return nil
}

// UpdateFinalStatus 更新最终状态
func (r *runFileRepo) UpdateFinalStatus(ctx context.Context, runFileID string, status types.FileFinalStatus, errMsg string) error {
	query := `
		UPDATE run_files SET
			final_status = ?, error_message = ?, completed_at = ?
		WHERE run_file_id = ?
	`

	now := time.Now()
	result, err := r.db.Exec(query, string(status), errMsg, now.Format(time.RFC3339), runFileID)
	if err != nil {
		return fmt.Errorf("failed to update final status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("run file not found: %s", runFileID)
	}

	return nil
}

// GetByID 根据 ID 查询
func (r *runFileRepo) GetByID(ctx context.Context, runFileID string) (*RunFile, error) {
	query := `
		SELECT run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		FROM run_files
		WHERE run_file_id = ?
	`

	return r.scanRunFile(r.db.QueryRow(query, runFileID))
}

// ListByTask 列出任务的所有运行文件
func (r *runFileRepo) ListByTask(ctx context.Context, taskID string) ([]*RunFile, error) {
	query := `
		SELECT run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		FROM run_files
		WHERE task_id = ?
		ORDER BY created_at
	`

	rows, err := r.db.Query(query, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to list run files by task: %w", err)
	}
	defer rows.Close()

	return r.scanRunFiles(rows)
}

// ListByStage 根据阶段列出运行文件
func (r *runFileRepo) ListByStage(ctx context.Context, taskID string, stage types.ProcessingStage) ([]*RunFile, error) {
	query := `
		SELECT run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		FROM run_files
		WHERE task_id = ? AND processing_stage = ?
		ORDER BY created_at
	`

	rows, err := r.db.Query(query, taskID, string(stage))
	if err != nil {
		return nil, fmt.Errorf("failed to list run files by stage: %w", err)
	}
	defer rows.Close()

	return r.scanRunFiles(rows)
}

// ListPending 列出待处理的运行文件
func (r *runFileRepo) ListPending(ctx context.Context, taskID string) ([]*RunFile, error) {
	query := `
		SELECT run_file_id, task_id, file_id, processing_stage, control_state,
			final_status, snapshot_path, snapshot_size, snapshot_modified_at,
			snapshot_md5, error_message, created_at, started_at, completed_at
		FROM run_files
		WHERE task_id = ? AND final_status = ?
		ORDER BY created_at
	`

	rows, err := r.db.Query(query, taskID, string(types.FileFinalStatusPending))
	if err != nil {
		return nil, fmt.Errorf("failed to list pending run files: %w", err)
	}
	defer rows.Close()

	return r.scanRunFiles(rows)
}

// Delete 删除运行文件
func (r *runFileRepo) Delete(ctx context.Context, runFileID string) error {
	query := `DELETE FROM run_files WHERE run_file_id = ?`

	result, err := r.db.Exec(query, runFileID)
	if err != nil {
		return fmt.Errorf("failed to delete run file: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("run file not found: %s", runFileID)
	}

	return nil
}

// DeleteByTask 删除任务的所有运行文件
func (r *runFileRepo) DeleteByTask(ctx context.Context, taskID string) error {
	query := `DELETE FROM run_files WHERE task_id = ?`

	_, err := r.db.Exec(query, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete run files by task: %w", err)
	}

	return nil
}

// scanRunFile 扫描单个运行文件
func (r *runFileRepo) scanRunFile(row *sql.Row) (*RunFile, error) {
	rf := &RunFile{}
	var processingStage, controlState, finalStatus string
	var createdAt string
	var snapshotModifiedAt sql.NullInt64
	var startedAt, completedAt sql.NullString

	err := row.Scan(
		&rf.RunFileID,
		&rf.TaskID,
		&rf.FileID,
		&processingStage,
		&controlState,
		&finalStatus,
		&rf.SnapshotPath,
		&rf.SnapshotSize,
		&snapshotModifiedAt,
		&rf.SnapshotMD5,
		&rf.ErrorMessage,
		&createdAt,
		&startedAt,
		&completedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run file not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan run file: %w", err)
	}

	if snapshotModifiedAt.Valid {
		rf.SnapshotModifiedAt = snapshotModifiedAt.Int64
	}
	rf.ProcessingStage = types.ProcessingStage(processingStage)
	rf.ControlState = types.ControlState(controlState)
	rf.FinalStatus = types.FileFinalStatus(finalStatus)
	rf.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	rf.StartedAt = stringToTime(startedAt)
	rf.CompletedAt = stringToTime(completedAt)

	return rf, nil
}

// scanRunFiles 扫描多个运行文件
func (r *runFileRepo) scanRunFiles(rows *sql.Rows) ([]*RunFile, error) {
	runFiles := make([]*RunFile, 0)

	for rows.Next() {
		rf := &RunFile{}
		var processingStage, controlState, finalStatus string
		var createdAt string
		var snapshotModifiedAt sql.NullInt64
		var startedAt, completedAt sql.NullString

		err := rows.Scan(
			&rf.RunFileID,
			&rf.TaskID,
			&rf.FileID,
			&processingStage,
			&controlState,
			&finalStatus,
			&rf.SnapshotPath,
			&rf.SnapshotSize,
			&snapshotModifiedAt,
			&rf.SnapshotMD5,
			&rf.ErrorMessage,
			&createdAt,
			&startedAt,
			&completedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan run file: %w", err)
		}

		if snapshotModifiedAt.Valid {
			rf.SnapshotModifiedAt = snapshotModifiedAt.Int64
		}
		rf.ProcessingStage = types.ProcessingStage(processingStage)
		rf.ControlState = types.ControlState(controlState)
		rf.FinalStatus = types.FileFinalStatus(finalStatus)
		rf.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		rf.StartedAt = stringToTime(startedAt)
		rf.CompletedAt = stringToTime(completedAt)

		runFiles = append(runFiles, rf)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate run files: %w", err)
	}

	return runFiles, nil
}
