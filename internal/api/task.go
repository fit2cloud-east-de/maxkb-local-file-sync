package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
	"maxkb-local-file-sync/internal/service"
)

// TaskAPI 任务管理 API（Wails 绑定）
type TaskAPI struct {
	app *app.Application
}

// NewTaskAPI 创建任务 API
func NewTaskAPI(app *app.Application) *TaskAPI {
	return &TaskAPI{app: app}
}

// TaskDTO 任务 DTO
type TaskDTO struct {
	TaskID          string `json:"taskId"`
	FolderID        string `json:"folderId"`
	FolderName      string `json:"folderName"`
	KBId            string `json:"kbId"`
	WorkspaceID     string `json:"workspaceId"`
	TriggerType     string `json:"triggerType"`
	RunStatus       string `json:"runStatus"`
	ProcessingStage string `json:"processingStage"`
	ControlState    string `json:"controlState"`
	CreatedAt       string `json:"createdAt"`
	StartedAt       string `json:"startedAt,omitempty"`
	CompletedAt     string `json:"completedAt,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	TotalFiles      int    `json:"totalFiles"`
	SuccessCount    int    `json:"successCount"`
	FailedCount     int    `json:"failedCount"`
	SkippedCount    int    `json:"skippedCount"`
	ProcessedFiles  int    `json:"processedFiles"`
	SuccessFiles    int    `json:"successFiles"`
	FailedFiles     int    `json:"failedFiles"`
	ReconcileCount  int    `json:"reconcileCount"`
	RecoveryCount   int    `json:"recoveryCount"`
	ControlReason   string `json:"controlReason,omitempty"`
	ErrorSummary    string `json:"errorSummary,omitempty"`
}

type QueueStatsDTO struct {
	Queued            int `json:"queued"`
	Running           int `json:"running"`
	Paused            int `json:"paused"`
	ReconcileRequired int `json:"reconcileRequired"`
}

type ReconcileDTO struct {
	RunFileID          string `json:"runFileId"`
	TaskID             string `json:"taskId"`
	FileID             string `json:"fileId"`
	FolderID           string `json:"folderId"`
	FolderName         string `json:"folderName"`
	RelativePath       string `json:"relativePath"`
	ProcessingStage    string `json:"processingStage"`
	Reason             string `json:"reason"`
	SnapshotPath       string `json:"snapshotPath"`
	SnapshotMD5        string `json:"snapshotMD5"`
	SnapshotSize       int64  `json:"snapshotSize"`
	MaxKBSourceFileID  string `json:"maxKBSourceFileID"`
	MaxKBBatchTaskID   string `json:"maxKBBatchTaskID"`
	MaxKBDocumentID    string `json:"maxKBDocumentID"`
	DeletingDocumentID string `json:"deletingDocumentID"`
	MinerUTaskID       string `json:"minerUTaskID"`
	MinerUStatus       string `json:"minerUStatus"`
	CreatedAt          string `json:"createdAt"`
	CompletedAt        string `json:"completedAt"`
}

// RunFileDTO 运行文件 DTO
type RunFileDTO struct {
	RunFileID       string `json:"runFileId"`
	TaskID          string `json:"taskId"`
	FileID          string `json:"fileId"`
	RelativePath    string `json:"relativePath"`
	ProcessingStage string `json:"processingStage"`
	ControlState    string `json:"controlState"`
	FinalStatus     string `json:"finalStatus"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	CreatedAt       string `json:"createdAt"`
	StartedAt       string `json:"startedAt,omitempty"`
	CompletedAt     string `json:"completedAt,omitempty"`
}

// wrapCreateTaskError keeps expected business outcomes distinguishable across
// the Wails error boundary, where only error text is transported to the web UI.
// The marker is intentionally stable so the frontend does not have to match a
// localized or wrapped implementation message.
func wrapCreateTaskError(err error) error {
	if errors.Is(err, service.ErrNoPendingChanges) {
		return errors.New("NO_PENDING_CHANGES: no pending changes to sync")
	}
	return fmt.Errorf("failed to create task: %w", err)
}

// wrapRetryFailedTaskError keeps retry outcomes stable across the Wails boundary.
func wrapRetryFailedTaskError(err error) error {
	switch {
	case errors.Is(err, service.ErrRetryRequiresReconciliation):
		return errors.New("RETRY_REQUIRES_RECONCILIATION: 该失败文件存在不确定的远端操作，请先前往\"异常处理\"确认远端状态。")
	case errors.Is(err, service.ErrNoRetryableFailedFiles):
		return errors.New("NO_RETRYABLE_FAILED_FILES: 当前批次没有可重新同步的失败文件。")
	default:
		return fmt.Errorf("failed to retry failed task: %w", err)
	}
}

// CreateTask 创建同步任务
func (api *TaskAPI) CreateTask(folderID string, triggerType string) (*TaskDTO, error) {
	ctx := context.Background()

	trigger := types.TriggerType(triggerType)
	task, err := api.app.TaskService().CreateTask(ctx, folderID, trigger)
	if err != nil {
		return nil, wrapCreateTaskError(err)
	}

	// 自动加入执行队列
	if err := api.app.Orchestrator().EnqueueTask(ctx, task.TaskID); err != nil {
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	return api.toTaskDTO(ctx, task)
}

// RetryFailedTask creates a new batch for failed files in a previous batch.
// It intentionally does not rescan the local directory, so a failed file can
// be retried even when its bytes have not changed.
func (api *TaskAPI) RetryFailedTask(sourceTaskID string) (*TaskDTO, error) {
	ctx := context.Background()
	task, err := api.app.TaskService().RetryFailedTask(ctx, sourceTaskID)
	if err != nil {
		return nil, wrapRetryFailedTaskError(err)
	}
	if err := api.app.Orchestrator().EnqueueTask(ctx, task.TaskID); err != nil {
		return nil, fmt.Errorf("failed to enqueue retry task: %w", err)
	}
	return api.toTaskDTO(ctx, task)
}

// GetTask 获取任务详情
func (api *TaskAPI) GetTask(taskID string) (*TaskDTO, error) {
	ctx := context.Background()

	task, err := api.app.TaskService().GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	return api.toTaskDTO(ctx, task)
}

// ListTasks 列出任务历史
func (api *TaskAPI) ListTasks(folderID string, limit int) ([]*TaskDTO, error) {
	ctx := context.Background()

	if limit == 0 {
		limit = 50
	}

	tasks, err := api.app.TaskService().ListTaskHistory(ctx, folderID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	result := make([]*TaskDTO, 0, len(tasks))
	for _, task := range tasks {
		dto, err := api.toTaskDTO(ctx, task)
		if err != nil {
			continue
		}
		result = append(result, dto)
	}

	return result, nil
}

// ListRunningTasks 列出运行中的任务
func (api *TaskAPI) ListRunningTasks() ([]*TaskDTO, error) {
	ctx := context.Background()

	tasks, err := api.app.TaskService().ListRunningTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list running tasks: %w", err)
	}

	result := make([]*TaskDTO, 0, len(tasks))
	for _, task := range tasks {
		dto, err := api.toTaskDTO(ctx, task)
		if err != nil {
			continue
		}
		result = append(result, dto)
	}

	return result, nil
}

// PauseTask 暂停任务
func (api *TaskAPI) PauseTask(taskID string) error {
	ctx := context.Background()

	if err := api.app.TaskService().PauseTask(ctx, taskID); err != nil {
		return fmt.Errorf("failed to pause task: %w", err)
	}

	return nil
}

// ResumeTask 恢复任务
func (api *TaskAPI) ResumeTask(taskID string) error {
	ctx := context.Background()

	if err := api.app.TaskService().ResumeTask(ctx, taskID); err != nil {
		return fmt.Errorf("failed to resume task: %w", err)
	}

	return nil
}

// StopTask 停止任务
func (api *TaskAPI) StopTask(taskID string) error {
	ctx := context.Background()

	if err := api.app.TaskService().StopTask(ctx, taskID); err != nil {
		return fmt.Errorf("failed to stop task: %w", err)
	}

	return nil
}

// GetRunFiles 获取任务的运行文件列表
func (api *TaskAPI) GetRunFiles(taskID string) ([]*RunFileDTO, error) {
	ctx := context.Background()

	runFiles, err := api.app.TaskService().GetRunFiles(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get run files: %w", err)
	}

	result := make([]*RunFileDTO, 0, len(runFiles))
	for _, rf := range runFiles {
		dto, err := api.toRunFileDTO(ctx, rf)
		if err != nil {
			continue
		}
		result = append(result, dto)
	}

	return result, nil
}

// GetQueueStats 获取持久化队列和异常处理统计。
func (api *TaskAPI) GetQueueStats() (*QueueStatsDTO, error) {
	stats, err := api.app.ReliabilityStore().QueueStats(context.Background())
	if err != nil {
		return nil, err
	}
	return &QueueStatsDTO{Queued: stats.Queued, Running: stats.Running, Paused: stats.Paused, ReconcileRequired: stats.ReconcileRequired}, nil
}

// ListReconcileRequired 列出所有不能安全自动重试的外部操作。
func (api *TaskAPI) ListReconcileRequired() ([]*ReconcileDTO, error) {
	items, err := api.app.ReliabilityStore().ListReconcileItems(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]*ReconcileDTO, 0, len(items))
	for _, x := range items {
		out = append(out, &ReconcileDTO{
			RunFileID: x.RunFileID, TaskID: x.TaskID, FileID: x.FileID, FolderID: x.FolderID,
			FolderName: x.FolderName, RelativePath: x.RelativePath, ProcessingStage: x.ProcessingStage,
			Reason: x.Reason, SnapshotPath: x.SnapshotPath, SnapshotMD5: x.SnapshotMD5,
			SnapshotSize: x.SnapshotSize, MaxKBSourceFileID: x.MaxKBSourceFileID,
			MaxKBBatchTaskID: x.MaxKBBatchTaskID, MaxKBDocumentID: x.MaxKBDocumentID,
			DeletingDocumentID: x.DeletingDocumentID, MinerUTaskID: x.MinerUTaskID,
			MinerUStatus: x.MinerUStatus, CreatedAt: x.CreatedAt, CompletedAt: x.CompletedAt,
		})
	}
	return out, nil
}

// ResolveReconcile 只接受明确的人工决策，禁止后台盲目重试非幂等操作。
func (api *TaskAPI) ResolveReconcile(runFileID, resolution, remoteDocumentID string) error {
	_, err := api.app.ReliabilityStore().ResolveReconcile(context.Background(), runFileID, resolution, remoteDocumentID)
	if err != nil {
		return fmt.Errorf("failed to resolve reconciliation: %w", err)
	}
	if resolution == "REMOTE_ABSENT_RETRY" {
		api.app.Orchestrator().Wake()
	}
	return nil
}

// toTaskDTO 转换为任务 DTO
func (api *TaskAPI) toTaskDTO(ctx context.Context, task *repository.SyncTask) (*TaskDTO, error) {
	dto := &TaskDTO{
		TaskID:          task.TaskID,
		FolderID:        task.FolderID,
		KBId:            task.KBId,
		WorkspaceID:     task.WorkspaceID,
		TriggerType:     string(task.TriggerType),
		RunStatus:       string(task.RunStatus),
		ProcessingStage: string(task.ProcessingStage),
		ControlState:    string(task.ControlState),
		CreatedAt:       task.CreatedAt.Format(time.RFC3339),
		ErrorMessage:    task.ErrorMessage,
		TotalFiles:      task.TotalFiles,
		SuccessCount:    task.SuccessCount,
		FailedCount:     task.FailedCount,
		SkippedCount:    task.SkippedCount,
		ProcessedFiles:  task.SuccessCount + task.FailedCount + task.SkippedCount,
		SuccessFiles:    task.SuccessCount,
		FailedFiles:     task.FailedCount,
	}
	if metadata, err := api.app.ReliabilityStore().GetRunMetadata(ctx, task.TaskID); err == nil {
		dto.ReconcileCount = metadata.ReconcileCount
		dto.RecoveryCount = metadata.RecoveryCount
		dto.ControlReason = metadata.ControlReason
		dto.ErrorSummary = metadata.ErrorSummary
	}

	if task.StartedAt != nil {
		dto.StartedAt = task.StartedAt.Format(time.RFC3339)
	}

	if task.CompletedAt != nil {
		dto.CompletedAt = task.CompletedAt.Format(time.RFC3339)
	}

	// 获取文件夹名称
	folder, err := api.app.FolderRepo().GetByID(ctx, task.FolderID)
	if err == nil {
		dto.FolderName = folder.Name
	}

	return dto, nil
}

// toRunFileDTO 转换为运行文件 DTO
func (api *TaskAPI) toRunFileDTO(ctx context.Context, rf *repository.RunFile) (*RunFileDTO, error) {
	dto := &RunFileDTO{
		RunFileID:       rf.RunFileID,
		TaskID:          rf.TaskID,
		FileID:          rf.FileID,
		ProcessingStage: string(rf.ProcessingStage),
		ControlState:    string(rf.ControlState),
		FinalStatus:     string(rf.FinalStatus),
		ErrorMessage:    rf.ErrorMessage,
		CreatedAt:       rf.CreatedAt.Format(time.RFC3339),
	}

	if rf.StartedAt != nil {
		dto.StartedAt = rf.StartedAt.Format(time.RFC3339)
	}

	if rf.CompletedAt != nil {
		dto.CompletedAt = rf.CompletedAt.Format(time.RFC3339)
	}

	// 获取文件相对路径
	syncFile, err := api.app.FileRepo().GetByID(ctx, rf.FileID)
	if err == nil {
		dto.RelativePath = syncFile.RelativePath
	}

	return dto, nil
}
