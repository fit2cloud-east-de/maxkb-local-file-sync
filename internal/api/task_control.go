package api

import (
	"context"
	"fmt"

	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/pkg/types"
)

// TaskControlAPI 任务控制 API（启用/关闭、暂停/继续/停止）
type TaskControlAPI struct {
	app *app.Application
}

// NewTaskControlAPI 创建任务控制 API
func NewTaskControlAPI(app *app.Application) *TaskControlAPI {
	return &TaskControlAPI{app: app}
}

// EnableTask 启用同步任务
func (api *TaskControlAPI) EnableTask(folderID string) error {
	ctx := context.Background()

	// 1. 更新 enabled = true
	if err := api.app.FolderRepo().SetEnabled(ctx, folderID, true); err != nil {
		return fmt.Errorf("failed to enable task: %w", err)
	}

	// 2. 恢复 Cron 调度
	folder, err := api.app.FolderRepo().GetByID(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	if folder.CronEnabled && folder.CronExpression != "" {
		if err := api.app.CronService().AddSchedule(ctx, folderID); err != nil {
			api.app.GetLogger().ErrorWithErr("Failed to add cron schedule", err)
			return fmt.Errorf("failed to restore cron schedule: %w", err)
		}
	}

	api.app.GetLogger().Info("Task enabled")
	return nil
}

// DisableTask 关闭同步任务
func (api *TaskControlAPI) DisableTask(folderID string) error {
	ctx := context.Background()

	// 1. 更新 enabled = false, disabled_at = now
	if err := api.app.FolderRepo().SetEnabled(ctx, folderID, false); err != nil {
		return fmt.Errorf("failed to disable task: %w", err)
	}

	// 2. 取消该任务所有 QUEUED 批次。取消必须在持久化队列事务中
	// 完成，确保被取消批次不会被 worker 领取后再扫描或访问远端。
	if _, err := api.app.ReliabilityStore().CancelQueuedByFolder(ctx, folderID, "同步任务已关闭"); err != nil {
		return fmt.Errorf("failed to cancel queued tasks: %w", err)
	}

	// 3. 移除 Cron 调度
	if err := api.app.CronService().RemoveSchedule(ctx, folderID); err != nil {
		api.app.GetLogger().ErrorWithErr("Failed to remove cron schedule", err)
	}

	api.app.GetLogger().Info("Task disabled")
	return nil
}

// PauseTask 暂停正在运行的任务
func (api *TaskControlAPI) PauseTask(taskID string) error {
	ctx := context.Background()

	// 1. 获取任务信息
	task, err := api.app.TaskRepo().GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	// 2. 检查状态是否为 RUNNING
	if task.RunStatus != types.RunStatusRunning {
		return fmt.Errorf("task is not running (current status: %s)", task.RunStatus)
	}

	// 3. 使用 reliability store 的 Pause 方法
	if err := api.app.ReliabilityStore().Pause(ctx, taskID, "paused_by_user"); err != nil {
		return fmt.Errorf("failed to pause task: %w", err)
	}

	api.app.GetLogger().Info("Task pause requested")
	return nil
}

// ResumeTask 继续已暂停的任务
func (api *TaskControlAPI) ResumeTask(taskID string) error {
	ctx := context.Background()

	// 1. 获取任务信息
	task, err := api.app.TaskRepo().GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	// 2. 检查状态是否为 PAUSED
	if task.RunStatus != types.RunStatusPaused {
		return fmt.Errorf("task is not paused (current status: %s)", task.RunStatus)
	}

	// 3. 使用 reliability store 的 Resume 方法
	if err := api.app.ReliabilityStore().Resume(ctx, taskID); err != nil {
		return fmt.Errorf("failed to resume task: %w", err)
	}

	api.app.GetLogger().Info("Task resume requested")
	return nil
}

// StopTask 停止任务（支持 QUEUED、RUNNING、PAUSED 状态）
func (api *TaskControlAPI) StopTask(taskID string) error {
	ctx := context.Background()

	// 1. 获取任务信息
	task, err := api.app.TaskRepo().GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	// 2. 根据当前状态决定停止策略
	switch task.RunStatus {
	case types.RunStatusQueued:
		// 取消排队与运行中停止是两个不同的显式状态迁移。
		if err := api.app.ReliabilityStore().CancelQueued(ctx, taskID, "stopped_by_user_from_queued"); err != nil {
			return fmt.Errorf("failed to cancel queued task: %w", err)
		}
		api.app.GetLogger().Info("Queued task cancelled")

	case types.RunStatusRunning:
		// 停止运行：标记为 STOP_REQUESTED，执行器在下一个检查点会停止
		if err := api.app.ReliabilityStore().Stop(ctx, taskID, "stopped_by_user_from_running"); err != nil {
			return fmt.Errorf("failed to request stop for running task: %w", err)
		}
		api.app.GetLogger().Info("Running task stop requested")

	case types.RunStatusPaused:
		// 停止暂停：直接标记为 STOPPED
		if err := api.app.ReliabilityStore().Stop(ctx, taskID, "stopped_by_user_from_paused"); err != nil {
			return fmt.Errorf("failed to stop paused task: %w", err)
		}
		api.app.GetLogger().Info("Paused task stopped")

	default:
		return fmt.Errorf("cannot stop task in %s status", task.RunStatus)
	}

	return nil
}
