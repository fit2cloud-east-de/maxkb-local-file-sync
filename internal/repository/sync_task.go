package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

// SyncTask 同步任务实体
type SyncTask struct {
	TaskID          string
	FolderID        string
	KBId            string
	WorkspaceID     string
	TriggerType     types.TriggerType
	RunStatus       types.RunStatus
	ProcessingStage types.ProcessingStage
	ControlState    types.ControlState
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	ErrorMessage    string
	TotalFiles      int
	SuccessCount    int
	FailedCount     int
	SkippedCount    int
}

// SyncTaskRepository 同步任务仓储接口
type SyncTaskRepository interface {
	// 创建
	Create(ctx context.Context, task *SyncTask) error

	// 更新
	Update(ctx context.Context, task *SyncTask) error
	UpdateStatus(ctx context.Context, taskID string, status types.RunStatus) error
	UpdateStage(ctx context.Context, taskID string, stage types.ProcessingStage) error
	UpdateControlState(ctx context.Context, taskID string, state types.ControlState) error
	UpdateProgress(ctx context.Context, taskID string, success, failed, skipped int) error

	// 查询
	GetByID(ctx context.Context, taskID string) (*SyncTask, error)
	ListByFolder(ctx context.Context, folderID string, limit int) ([]*SyncTask, error)
	ListByStatus(ctx context.Context, status types.RunStatus) ([]*SyncTask, error)
	ListRunning(ctx context.Context) ([]*SyncTask, error)

	// 删除
	Delete(ctx context.Context, taskID string) error
}

// syncTaskRepo 同步任务仓储实现
type syncTaskRepo struct {
	db *db.DB
}

// NewSyncTaskRepository 创建同步任务仓储
func NewSyncTaskRepository(database *db.DB) SyncTaskRepository {
	return &syncTaskRepo{db: database}
}

// Create 创建同步任务
func (r *syncTaskRepo) Create(ctx context.Context, task *SyncTask) error {
	query := `
		INSERT INTO sync_tasks (
			task_id, folder_id, kb_id, workspace_id, trigger_type,
			run_status, processing_stage, control_state,
			created_at, started_at, completed_at, error_message,
			total_files, success_count, failed_count, skipped_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.Exec(query,
		task.TaskID,
		task.FolderID,
		task.KBId,
		task.WorkspaceID,
		string(task.TriggerType),
		string(task.RunStatus),
		string(task.ProcessingStage),
		string(task.ControlState),
		task.CreatedAt.Format(time.RFC3339),
		timeToString(task.StartedAt),
		timeToString(task.CompletedAt),
		task.ErrorMessage,
		task.TotalFiles,
		task.SuccessCount,
		task.FailedCount,
		task.SkippedCount,
	)

	if err != nil {
		return fmt.Errorf("failed to create sync task: %w", err)
	}

	return nil
}

// Update 更新同步任务
func (r *syncTaskRepo) Update(ctx context.Context, task *SyncTask) error {
	query := `
		UPDATE sync_tasks SET
			run_status = ?, processing_stage = ?, control_state = ?,
			started_at = ?, completed_at = ?, error_message = ?,
			total_files = ?, success_count = ?, failed_count = ?, skipped_count = ?
		WHERE task_id = ?
	`

	result, err := r.db.Exec(query,
		string(task.RunStatus),
		string(task.ProcessingStage),
		string(task.ControlState),
		timeToString(task.StartedAt),
		timeToString(task.CompletedAt),
		task.ErrorMessage,
		task.TotalFiles,
		task.SuccessCount,
		task.FailedCount,
		task.SkippedCount,
		task.TaskID,
	)

	if err != nil {
		return fmt.Errorf("failed to update sync task: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", task.TaskID)
	}

	return nil
}

// UpdateStatus 更新任务状态
func (r *syncTaskRepo) UpdateStatus(ctx context.Context, taskID string, status types.RunStatus) error {
	query := `UPDATE sync_tasks SET run_status = ? WHERE task_id = ?`

	result, err := r.db.Exec(query, string(status), taskID)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", taskID)
	}

	return nil
}

// UpdateStage 更新处理阶段
func (r *syncTaskRepo) UpdateStage(ctx context.Context, taskID string, stage types.ProcessingStage) error {
	query := `UPDATE sync_tasks SET processing_stage = ? WHERE task_id = ?`

	result, err := r.db.Exec(query, string(stage), taskID)
	if err != nil {
		return fmt.Errorf("failed to update task stage: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", taskID)
	}

	return nil
}

// UpdateControlState 更新控制状态
func (r *syncTaskRepo) UpdateControlState(ctx context.Context, taskID string, state types.ControlState) error {
	query := `UPDATE sync_tasks SET control_state = ? WHERE task_id = ?`

	result, err := r.db.Exec(query, string(state), taskID)
	if err != nil {
		return fmt.Errorf("failed to update control state: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", taskID)
	}

	return nil
}

// UpdateProgress 更新进度计数
func (r *syncTaskRepo) UpdateProgress(ctx context.Context, taskID string, success, failed, skipped int) error {
	query := `
		UPDATE sync_tasks SET
			success_count = ?, failed_count = ?, skipped_count = ?
		WHERE task_id = ?
	`

	result, err := r.db.Exec(query, success, failed, skipped, taskID)
	if err != nil {
		return fmt.Errorf("failed to update task progress: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", taskID)
	}

	return nil
}

// GetByID 根据 ID 查询
func (r *syncTaskRepo) GetByID(ctx context.Context, taskID string) (*SyncTask, error) {
	query := `
		SELECT task_id, folder_id, kb_id, workspace_id, trigger_type,
			run_status, processing_stage, control_state,
			created_at, started_at, completed_at, error_message,
			total_files, success_count, failed_count, skipped_count
		FROM sync_tasks
		WHERE task_id = ?
	`

	return r.scanTask(r.db.QueryRow(query, taskID))
}

// ListByFolder 列出文件夹的任务。
// folderID 为空时表示查询全部任务，供执行队列页面使用；不能把空字符串
// 当作真实 folder_id 查询，否则前端会出现队列计数正常但批次列表为空。
func (r *syncTaskRepo) ListByFolder(ctx context.Context, folderID string, limit int) ([]*SyncTask, error) {
	baseQuery := `
		SELECT task_id, folder_id, kb_id, workspace_id, trigger_type,
			run_status, processing_stage, control_state,
			created_at, started_at, completed_at, error_message,
			total_files, success_count, failed_count, skipped_count
		FROM sync_tasks`

	var (
		rows *sql.Rows
		err  error
	)
	if folderID == "" {
		rows, err = r.db.Query(baseQuery+` ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = r.db.Query(baseQuery+` WHERE folder_id = ? ORDER BY created_at DESC LIMIT ?`, folderID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// ListByStatus 根据状态列出任务
func (r *syncTaskRepo) ListByStatus(ctx context.Context, status types.RunStatus) ([]*SyncTask, error) {
	query := `
		SELECT task_id, folder_id, kb_id, workspace_id, trigger_type,
			run_status, processing_stage, control_state,
			created_at, started_at, completed_at, error_message,
			total_files, success_count, failed_count, skipped_count
		FROM sync_tasks
		WHERE run_status = ?
		ORDER BY created_at DESC
	`

	rows, err := r.db.Query(query, string(status))
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks by status: %w", err)
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// ListRunning 列出正在运行的任务
func (r *syncTaskRepo) ListRunning(ctx context.Context) ([]*SyncTask, error) {
	query := `
		SELECT task_id, folder_id, kb_id, workspace_id, trigger_type,
			run_status, processing_stage, control_state,
			created_at, started_at, completed_at, error_message,
			total_files, success_count, failed_count, skipped_count
		FROM sync_tasks
		WHERE run_status IN (?, ?, ?, ?, ?, ?)
		ORDER BY created_at DESC
	`

	rows, err := r.db.Query(query,
		string(types.RunStatusQueued), string(types.RunStatusRunning),
		string(types.RunStatusPauseRequested), string(types.RunStatusPaused),
		string(types.RunStatusStopRequested), string(types.RunStatusInterrupted))
	if err != nil {
		return nil, fmt.Errorf("failed to list running tasks: %w", err)
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// Delete 删除任务
func (r *syncTaskRepo) Delete(ctx context.Context, taskID string) error {
	query := `DELETE FROM sync_tasks WHERE task_id = ?`

	result, err := r.db.Exec(query, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete sync task: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("sync task not found: %s", taskID)
	}

	return nil
}

// scanTask 扫描单个任务
func (r *syncTaskRepo) scanTask(row *sql.Row) (*SyncTask, error) {
	task := &SyncTask{}
	var triggerType, runStatus, processingStage, controlState string
	var createdAt string
	var startedAt, completedAt sql.NullString

	err := row.Scan(
		&task.TaskID,
		&task.FolderID,
		&task.KBId,
		&task.WorkspaceID,
		&triggerType,
		&runStatus,
		&processingStage,
		&controlState,
		&createdAt,
		&startedAt,
		&completedAt,
		&task.ErrorMessage,
		&task.TotalFiles,
		&task.SuccessCount,
		&task.FailedCount,
		&task.SkippedCount,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("sync task not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan sync task: %w", err)
	}

	task.TriggerType = types.TriggerType(triggerType)
	task.RunStatus = types.RunStatus(runStatus)
	task.ProcessingStage = types.ProcessingStage(processingStage)
	task.ControlState = types.ControlState(controlState)
	task.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	task.StartedAt = stringToTime(startedAt)
	task.CompletedAt = stringToTime(completedAt)

	return task, nil
}

// scanTasks 扫描多个任务
func (r *syncTaskRepo) scanTasks(rows *sql.Rows) ([]*SyncTask, error) {
	tasks := make([]*SyncTask, 0)

	for rows.Next() {
		task := &SyncTask{}
		var triggerType, runStatus, processingStage, controlState string
		var createdAt string
		var startedAt, completedAt sql.NullString

		err := rows.Scan(
			&task.TaskID,
			&task.FolderID,
			&task.KBId,
			&task.WorkspaceID,
			&triggerType,
			&runStatus,
			&processingStage,
			&controlState,
			&createdAt,
			&startedAt,
			&completedAt,
			&task.ErrorMessage,
			&task.TotalFiles,
			&task.SuccessCount,
			&task.FailedCount,
			&task.SkippedCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		task.TriggerType = types.TriggerType(triggerType)
		task.RunStatus = types.RunStatus(runStatus)
		task.ProcessingStage = types.ProcessingStage(processingStage)
		task.ControlState = types.ControlState(controlState)
		task.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		task.StartedAt = stringToTime(startedAt)
		task.CompletedAt = stringToTime(completedAt)

		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tasks: %w", err)
	}

	return tasks, nil
}
