package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// ErrNoPendingChanges 表示扫描后没有任何待同步的本地变更。
//
// 这是一个可被 API 层稳定识别的业务结果，不应被当作系统异常处理。
var ErrNoPendingChanges = errors.New("no pending changes to sync")

// ErrRetryRequiresReconciliation indicates that the failed file has a durable
// remote reference (or an explicit reconcile marker). Retrying it as a fresh
// upload could duplicate or delete the wrong remote document, so an operator
// must first make a decision in the exception-handling screen.
var ErrRetryRequiresReconciliation = errors.New("retry requires reconciliation")

// ErrNoRetryableFailedFiles indicates that the selected failed batch has no
// ordinary failed files that can safely be submitted again.
var ErrNoRetryableFailedFiles = errors.New("no retryable failed files")

// TaskService 任务服务
type TaskService struct {
	taskRepo    repository.SyncTaskRepository
	folderRepo  repository.SyncFolderRepository
	fileRepo    repository.SyncFileRepository
	runFileRepo repository.RunFileRepository
	reliability *repository.ReliabilityStore
	logger      *logger.Logger
}

// NewTaskService 创建任务服务
func NewTaskService(
	taskRepo repository.SyncTaskRepository,
	folderRepo repository.SyncFolderRepository,
	fileRepo repository.SyncFileRepository,
	runFileRepo repository.RunFileRepository,
	logger *logger.Logger,
) *TaskService {
	return &TaskService{
		taskRepo:    taskRepo,
		folderRepo:  folderRepo,
		fileRepo:    fileRepo,
		runFileRepo: runFileRepo,
		logger:      logger,
	}
}

// SetReliabilityStore attaches the durable run/queue store. It is kept as a
// setter so existing embedders that construct TaskService continue to compile.
func (s *TaskService) SetReliabilityStore(store *repository.ReliabilityStore) { s.reliability = store }

// CreateTask 创建同步任务
func (s *TaskService) CreateTask(ctx context.Context, folderID string, triggerType types.TriggerType) (*repository.SyncTask, error) {
	// 获取文件夹配置
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	// 获取待同步的文件
	pendingFiles, err := s.fileRepo.ListPendingChanges(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending changes: %w", err)
	}

	if len(pendingFiles) == 0 {
		return nil, ErrNoPendingChanges
	}

	// 创建任务
	now := time.Now()
	task := &repository.SyncTask{
		TaskID:          uuid.New().String(),
		FolderID:        folderID,
		KBId:            folder.KBId,
		WorkspaceID:     folder.WorkspaceID,
		TriggerType:     triggerType,
		RunStatus:       types.RunStatusQueued,
		ProcessingStage: types.ProcessingStageInit,
		ControlState:    types.ControlStateActive,
		CreatedAt:       now,
		TotalFiles:      len(pendingFiles),
	}

	// 创建运行文件记录（执行计划）。所有公共状态、批次、队列和活动锁
	// 必须在同一个 BEGIN IMMEDIATE 事务中落库。
	runFiles := make([]*repository.RunFile, 0, len(pendingFiles))
	for _, syncFile := range pendingFiles {
		runFiles = append(runFiles, &repository.RunFile{
			RunFileID: uuid.New().String(), TaskID: task.TaskID, FileID: syncFile.FileID,
			ProcessingStage: types.ProcessingStageInit, ControlState: types.ControlStateActive,
			FinalStatus: types.FileFinalStatusPending, CreatedAt: now,
		})
	}
	if s.reliability != nil {
		if err := s.reliability.CreateRunPlan(ctx, task, runFiles); err != nil {
			return nil, fmt.Errorf("failed to create durable run plan: %w", err)
		}
	} else {
		if err := s.taskRepo.Create(ctx, task); err != nil {
			return nil, fmt.Errorf("failed to create task: %w", err)
		}
		if err := s.runFileRepo.BatchCreate(ctx, runFiles); err != nil {
			return nil, fmt.Errorf("failed to create run files: %w", err)
		}
	}

	s.logger.Info("Created sync task: task_id=%s, folder=%s, files=%d", task.TaskID, folderID, len(pendingFiles))

	return task, nil
}

// RetryFailedTask creates a new durable batch containing only ordinary failed
// files from a previous terminal batch. It deliberately does not consult the
// current directory change set: a failed upload must be retryable even when
// the source bytes have not changed since the previous scan.
//
// Files with RECONCILE_REQUIRED or any persisted remote operation reference
// are excluded from automatic retry. Those files must be resolved by a human
// in the exception-handling flow first.
func (s *TaskService) RetryFailedTask(ctx context.Context, sourceTaskID string) (*repository.SyncTask, error) {
	source, err := s.taskRepo.GetByID(ctx, sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source task: %w", err)
	}
	if source.RunStatus != types.RunStatusFailed && source.RunStatus != types.RunStatusPartialSuccess {
		return nil, fmt.Errorf("%w: source batch status is %s", ErrNoRetryableFailedFiles, source.RunStatus)
	}

	runFiles, err := s.runFileRepo.ListByTask(ctx, sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to list source run files: %w", err)
	}
	failedFiles := make([]*repository.RunFile, 0, len(runFiles))
	blockedFiles := make([]string, 0)
	reconcileRequired := false
	for _, runFile := range runFiles {
		if runFile.FinalStatus == types.FileFinalStatusReconcileRequired {
			reconcileRequired = true
		}
		if runFile.FinalStatus != types.FileFinalStatusFailed {
			continue
		}
		if s.reliability != nil {
			attempt, attemptErr := s.reliability.LatestAttempt(ctx, runFile.RunFileID)
			if attemptErr != nil && !errors.Is(attemptErr, sql.ErrNoRows) {
				return nil, fmt.Errorf("failed to inspect failed file %s: %w", runFile.RunFileID, attemptErr)
			}
			if attemptErr == nil && attemptHasRemoteReference(attempt) {
				blockedFiles = append(blockedFiles, runFile.RunFileID)
				continue
			}
		}
		failedFiles = append(failedFiles, runFile)
	}
	if len(blockedFiles) > 0 {
		if s.reliability != nil {
			const reason = "上次失败文件存在未确认的远端操作，请先在异常处理中确认远端状态"
			for _, runFileID := range blockedFiles {
				if err := s.reliability.PromoteFailedToReconcile(ctx, runFileID, reason); err != nil {
					return nil, fmt.Errorf("failed to create reconciliation item for %s: %w", runFileID, err)
				}
			}
		}
		return nil, ErrRetryRequiresReconciliation
	}
	if reconcileRequired {
		return nil, ErrRetryRequiresReconciliation
	}
	if len(failedFiles) == 0 {
		return nil, ErrNoRetryableFailedFiles
	}

	now := time.Now().UTC()
	retryTask := &repository.SyncTask{
		TaskID:          uuid.New().String(),
		FolderID:        source.FolderID,
		KBId:            source.KBId,
		WorkspaceID:     source.WorkspaceID,
		TriggerType:     types.TriggerTypeSingleFileRetry,
		RunStatus:       types.RunStatusQueued,
		ProcessingStage: types.ProcessingStageInit,
		ControlState:    types.ControlStateActive,
		CreatedAt:       now,
		TotalFiles:      len(failedFiles),
	}
	runPlan := make([]*repository.RunFile, 0, len(failedFiles))
	for _, sourceFile := range failedFiles {
		runPlan = append(runPlan, &repository.RunFile{
			RunFileID: uuid.New().String(), TaskID: retryTask.TaskID, FileID: sourceFile.FileID,
			ProcessingStage: types.ProcessingStageInit, ControlState: types.ControlStateActive,
			FinalStatus: types.FileFinalStatusPending, CreatedAt: now,
		})
	}
	if s.reliability != nil {
		if err := s.reliability.CreateRunPlan(ctx, retryTask, runPlan); err != nil {
			return nil, fmt.Errorf("failed to create retry run plan: %w", err)
		}
	} else {
		if err := s.taskRepo.Create(ctx, retryTask); err != nil {
			return nil, fmt.Errorf("failed to create retry task: %w", err)
		}
		if err := s.runFileRepo.BatchCreate(ctx, runPlan); err != nil {
			return nil, fmt.Errorf("failed to create retry run files: %w", err)
		}
	}
	s.logger.Info("Created retry sync task: task_id=%s, source_task_id=%s, files=%d", retryTask.TaskID, sourceTaskID, len(runPlan))
	return retryTask, nil
}

func attemptHasRemoteReference(attempt *repository.FileAttempt) bool {
	if attempt == nil {
		return false
	}
	return attempt.ReconcileReason != "" || attempt.MinerURemoteRef != "" || attempt.MinerUTaskID != "" ||
		attempt.MaxKBSourceFileID != "" || attempt.MaxKBBatchTaskID != "" || attempt.MaxKBDocumentID != "" ||
		attempt.DeletingDocumentID != ""
}

// GetTask 获取任务
func (s *TaskService) GetTask(ctx context.Context, taskID string) (*repository.SyncTask, error) {
	return s.taskRepo.GetByID(ctx, taskID)
}

// UpdateTaskStatus 更新任务状态
func (s *TaskService) UpdateTaskStatus(ctx context.Context, taskID string, status types.RunStatus) error {
	if err := s.taskRepo.UpdateStatus(ctx, taskID, status); err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	s.logger.Info("Updated task status: task_id=%s, status=%s", taskID, status)
	return nil
}

// UpdateTaskStage 更新任务阶段
func (s *TaskService) UpdateTaskStage(ctx context.Context, taskID string, stage types.ProcessingStage) error {
	if err := s.taskRepo.UpdateStage(ctx, taskID, stage); err != nil {
		return fmt.Errorf("failed to update task stage: %w", err)
	}

	s.logger.Info("Updated task stage: task_id=%s, stage=%s", taskID, stage)
	return nil
}

// UpdateTaskProgress 更新任务进度
func (s *TaskService) UpdateTaskProgress(ctx context.Context, taskID string, success, failed, skipped int) error {
	if s.reliability != nil {
		if err := s.reliability.UpdateProgress(ctx, taskID, success, failed, skipped); err != nil {
			return fmt.Errorf("failed to update durable progress: %w", err)
		}
		return nil
	}
	if err := s.taskRepo.UpdateProgress(ctx, taskID, success, failed, skipped); err != nil {
		return fmt.Errorf("failed to update task progress: %w", err)
	}
	return nil
}

// PauseTask 暂停任务
func (s *TaskService) PauseTask(ctx context.Context, taskID string) error {
	if s.reliability != nil {
		return s.reliability.Pause(ctx, taskID, "user_requested_pause")
	}
	return fmt.Errorf("durable reliability store is not configured")
}

// ResumeTask 恢复任务
func (s *TaskService) ResumeTask(ctx context.Context, taskID string) error {
	if s.reliability != nil {
		return s.reliability.Resume(ctx, taskID)
	}
	return fmt.Errorf("durable reliability store is not configured")
}

// StopTask 停止任务
func (s *TaskService) StopTask(ctx context.Context, taskID string) error {
	if s.reliability != nil {
		return s.reliability.Stop(ctx, taskID, "user_requested_stop")
	}
	return fmt.Errorf("durable reliability store is not configured")
}

// CompleteTask 完成任务
func (s *TaskService) CompleteTask(ctx context.Context, taskID string, success bool, errMsg string) error {
	if s.reliability != nil {
		_, err := s.reliability.Complete(ctx, taskID, errMsg)
		return err
	}
	now := time.Now()

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	task.CompletedAt = &now
	task.ProcessingStage = types.ProcessingStageDone

	if success {
		task.RunStatus = types.RunStatusCompleted
	} else {
		task.RunStatus = types.RunStatusFailed
		task.ErrorMessage = errMsg
	}

	if err := s.taskRepo.Update(ctx, task); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	s.logger.Info("Completed task: task_id=%s, status=%s", taskID, task.RunStatus)
	return nil
}

// ListRunningTasks 列出运行中的任务
func (s *TaskService) ListRunningTasks(ctx context.Context) ([]*repository.SyncTask, error) {
	return s.taskRepo.ListRunning(ctx)
}

// ListTaskHistory 列出任务历史
func (s *TaskService) ListTaskHistory(ctx context.Context, folderID string, limit int) ([]*repository.SyncTask, error) {
	if limit == 0 {
		limit = 50
	}
	return s.taskRepo.ListByFolder(ctx, folderID, limit)
}

// GetRunFiles 获取任务的运行文件列表
func (s *TaskService) GetRunFiles(ctx context.Context, taskID string) ([]*repository.RunFile, error) {
	return s.runFileRepo.ListByTask(ctx, taskID)
}

// UpdateRunFileStage 更新运行文件阶段
func (s *TaskService) UpdateRunFileStage(ctx context.Context, runFileID string, stage types.ProcessingStage) error {
	return s.runFileRepo.UpdateStage(ctx, runFileID, stage)
}

// CompleteRunFile 完成运行文件
func (s *TaskService) CompleteRunFile(ctx context.Context, runFileID string, status types.FileFinalStatus, errMsg string) error {
	return s.runFileRepo.UpdateFinalStatus(ctx, runFileID, status, errMsg)
}
