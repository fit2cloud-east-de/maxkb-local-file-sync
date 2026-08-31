package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/robfig/cron/v3"

	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// CronService Cron 调度服务
type CronService struct {
	cron          *cron.Cron
	folderRepo    repository.SyncFolderRepository
	taskSvc       *TaskService
	orchestrator  *TaskOrchestrator
	fileScanner   *FileScanner
	logger        *logger.Logger
	scheduledJobs map[string]cron.EntryID // folderID → entryID
	mu            sync.RWMutex
}

// NewCronService 创建 Cron 调度服务
func NewCronService(
	folderRepo repository.SyncFolderRepository,
	taskSvc *TaskService,
	orchestrator *TaskOrchestrator,
	fileScanner *FileScanner,
	logger *logger.Logger,
) *CronService {
	return &CronService{
		cron:          cron.New(),
		folderRepo:    folderRepo,
		taskSvc:       taskSvc,
		orchestrator:  orchestrator,
		fileScanner:   fileScanner,
		logger:        logger,
		scheduledJobs: make(map[string]cron.EntryID),
	}
}

// Start 启动 Cron 调度器
func (s *CronService) Start(ctx context.Context) error {
	s.cron.Start()
	s.logger.Info("Cron service started")

	// 加载所有启用 Cron 的文件夹
	if err := s.ReloadAllSchedules(ctx); err != nil {
		return fmt.Errorf("failed to reload schedules: %w", err)
	}

	return nil
}

// Stop 停止 Cron 调度器
func (s *CronService) Stop(ctx context.Context) error {
	cronCtx := s.cron.Stop()
	<-cronCtx.Done()
	s.logger.Info("Cron service stopped")
	return nil
}

// AddSchedule 添加定时任务
func (s *CronService) AddSchedule(ctx context.Context, folderID string) error {
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	if !folder.CronEnabled {
		return fmt.Errorf("cron not enabled for folder: %s", folderID)
	}

	if folder.CronExpression == "" {
		return fmt.Errorf("cron expression is empty for folder: %s", folderID)
	}

	// 移除旧的调度（如果存在）
	s.RemoveSchedule(ctx, folderID)

	// 添加新的调度
	entryID, err := s.cron.AddFunc(folder.CronExpression, func() {
		s.executeCronTask(ctx, folderID)
	})

	if err != nil {
		return fmt.Errorf("failed to add cron schedule: %w", err)
	}

	s.mu.Lock()
	s.scheduledJobs[folderID] = entryID
	s.mu.Unlock()

	s.logger.Info("Added cron schedule: folder=%s, expression=%s", folderID, folder.CronExpression)
	return nil
}

// RemoveSchedule 移除定时任务
func (s *CronService) RemoveSchedule(ctx context.Context, folderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entryID, exists := s.scheduledJobs[folderID]
	if !exists {
		return nil
	}

	s.cron.Remove(entryID)
	delete(s.scheduledJobs, folderID)

	s.logger.Info("Removed cron schedule: folder=%s", folderID)
	return nil
}

// ReloadAllSchedules 重新加载所有调度
func (s *CronService) ReloadAllSchedules(ctx context.Context) error {
	folders, err := s.folderRepo.ListCronEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to list cron enabled folders: %w", err)
	}

	// 清空现有调度
	s.mu.Lock()
	for folderID := range s.scheduledJobs {
		s.cron.Remove(s.scheduledJobs[folderID])
	}
	s.scheduledJobs = make(map[string]cron.EntryID)
	s.mu.Unlock()

	// 添加所有启用的调度
	for _, folder := range folders {
		if err := s.AddSchedule(ctx, folder.FolderID); err != nil {
			s.logger.Error("Failed to add schedule for folder %s: %v", folder.FolderID, err)
			continue
		}
	}

	s.logger.Info("Reloaded %d cron schedules", len(folders))
	return nil
}

// executeCronTask 执行定时任务
func (s *CronService) executeCronTask(ctx context.Context, folderID string) {
	s.logger.Info("Executing cron task: folder=%s", folderID)

	// Cron 是后台触发入口，不能依赖 UI 先行扫描。扫描失败时只记录本次
	// 触发失败，不创建空批次或发起任何远端请求。
	if s.fileScanner == nil {
		s.logger.Error("Cron task skipped: file scanner is not configured")
		return
	}
	if _, err := s.fileScanner.DetectChanges(ctx, folderID); err != nil {
		s.logger.Error("Cron scan failed: folder=%s, error=%v", folderID, err)
		return
	}

	// 创建任务
	task, err := s.taskSvc.CreateTask(ctx, folderID, types.TriggerTypeCron)
	if err != nil {
		if errors.Is(err, ErrNoPendingChanges) {
			s.logger.Info("Cron task skipped: folder=%s, no pending changes", folderID)
			return
		}
		s.logger.Error("Failed to create cron task: folder=%s, error=%v", folderID, err)
		return
	}

	// 加入执行队列
	if err := s.orchestrator.EnqueueTask(ctx, task.TaskID); err != nil {
		s.logger.Error("Failed to enqueue cron task: task_id=%s, error=%v", task.TaskID, err)
		return
	}

	s.logger.Info("Cron task enqueued: task_id=%s, folder=%s", task.TaskID, folderID)
}

// GetScheduledFolders 获取所有已调度的文件夹
func (s *CronService) GetScheduledFolders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	folders := make([]string, 0, len(s.scheduledJobs))
	for folderID := range s.scheduledJobs {
		folders = append(folders, folderID)
	}
	return folders
}

// ValidateCronExpression 验证 Cron 表达式
func (s *CronService) ValidateCronExpression(expression string) error {
	_, err := cron.ParseStandard(expression)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}
