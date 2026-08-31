package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"maxkb-local-file-sync/internal/api"
	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/infra/platform"
)

// App struct
// appVersion is overridden by release build scripts through -ldflags. Keeping
// a development fallback makes the value available during `wails dev` too.
var appVersion = "v1.0.0"

type App struct {
	ctx         context.Context
	application *app.Application

	// API 层（供前端调用）
	folderAPI      *api.FolderAPI
	fileAPI        *api.FileAPI
	taskAPI        *api.TaskAPI
	configAPI      *api.ConfigAPI
	taskControlAPI *api.TaskControlAPI
	startupErr     error
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 获取用户主目录
	homeDir, err := os.UserHomeDir()
	if err != nil {
		a.recordStartupError("failed to get home directory", err)
		return
	}

	// 初始化平台标准的用户数据目录。安装目录与业务数据分离，避免升级或
	// 卸载时误删 SQLite、日志和恢复快照；旧版本 ~/.maxkb-sync 会在首次
	// 启动时迁移到新目录。
	storage, err := platform.ResolveStoragePaths(homeDir)
	if err != nil {
		a.recordStartupError("failed to resolve application storage", err)
		return
	}

	// 确保目录存在；失败时只输出脱敏后的诊断信息，避免把本地路径
	// 或底层错误中的其他敏感字段直接写入控制台。
	for _, dir := range []string{storage.Data, storage.Snapshots, storage.Logs, storage.Temp, storage.Backups} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			a.recordStartupError("failed to prepare application storage", err)
			return
		}
	}

	// 创建应用实例
	application, err := app.NewApplication(app.Config{
		DataDir:     storage.Data,
		SnapshotDir: storage.Snapshots,
		LogDir:      storage.Logs,
		LogLevel:    logger.LevelInfo,
	})

	if err != nil {
		a.recordStartupError("failed to create application", err)
		return
	}

	a.application = application

	// 初始化 API 层并先加载适配器配置，再启动 durable worker，避免启动窗口
	// 内已有队列任务在未配置远端客户端时被误判为失败。
	a.folderAPI = api.NewFolderAPI(application)
	a.fileAPI = api.NewFileAPI(application)
	a.taskAPI = api.NewTaskAPI(application)
	a.configAPI = api.NewConfigAPI(application)
	a.taskControlAPI = api.NewTaskControlAPI(application)
	// NewApplication restores only previously validated adapters directly from
	// the OS credential store. Do not call Configure* here: the public config
	// DTOs intentionally contain masked credentials, and Configure* correctly
	// treats any change as an unvalidated draft.

	if err := application.Start(); err != nil {
		application.GetLogger().ErrorWithErr("Failed to start application", err)
		_ = application.Stop()
		a.startupErr = errors.New("application failed to start: " + logger.SanitizeError(err))
		return
	}
}

// recordStartupError keeps Wails bindings callable when startup fails. Without
// this guard, the frontend receives a nil-pointer panic from every API call and
// can remain on its initial loading skeleton instead of showing the real cause.
func (a *App) recordStartupError(prefix string, err error) {
	a.startupErr = fmt.Errorf("%s: %s", prefix, logger.SanitizeError(err))
	fmt.Fprintf(os.Stderr, "Failed to start application: %s\n", logger.SanitizeError(a.startupErr))
}

func (a *App) requireReady() error {
	if a.startupErr != nil {
		return a.startupErr
	}
	if a.application == nil || a.folderAPI == nil || a.fileAPI == nil || a.taskAPI == nil || a.configAPI == nil || a.taskControlAPI == nil {
		return errors.New("application is not ready")
	}
	return nil
}

// shutdown is called when the app is closed
func (a *App) shutdown(ctx context.Context) {
	if a.application != nil {
		if err := a.application.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stop application: %s\n", logger.SanitizeError(err))
		}
	}
}

// loadStoredConfig 加载存储的配置
func (a *App) loadStoredConfig() {
	// 加载 MaxKB 配置
	maxkbConfig, err := a.configAPI.GetMaxKBConfig()
	if err == nil && maxkbConfig.BaseURL != "" {
		if err := a.application.ConfigureMaxKB(maxkbConfig.BaseURL, maxkbConfig.APIKey); err != nil {
			a.application.GetLogger().ErrorWithErr("Failed to configure MaxKB from stored config", err)
		}
	}

	// 加载 MinerU 配置
	mineruConfig, err := a.configAPI.GetMinerUConfig()
	if err == nil && mineruConfig.Enabled {
		if err := a.application.ConfigureMinerU(mineruConfig.BaseURL, mineruConfig.APIKey, mineruConfig.Mode); err != nil {
			a.application.GetLogger().ErrorWithErr("Failed to configure MinerU from stored config", err)
		}
	}
}

// ==================== 文件夹管理 API ====================

func (a *App) CreateFolder(req api.CreateFolderRequest) (*api.FolderDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.CreateFolder(req)
}

func (a *App) UpdateFolder(folderID string, req api.CreateFolderRequest) (*api.FolderDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.UpdateFolder(folderID, req)
}

func (a *App) DeleteFolder(folderID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.folderAPI.DeleteFolder(folderID)
}

func (a *App) GetFolder(folderID string) (*api.FolderDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.GetFolder(folderID)
}

func (a *App) ListFolders() ([]*api.FolderDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.ListFolders()
}

func (a *App) ScanFolder(folderID string) (*api.ScanResultDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.ScanFolder(folderID)
}

func (a *App) DetectChanges(folderID string) (string, error) {
	if err := a.requireReady(); err != nil {
		return "", err
	}
	return a.folderAPI.DetectChanges(folderID)
}

func (a *App) PreviewMatch(req api.PreviewMatchRequest) (*api.PreviewMatchResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.folderAPI.PreviewMatch(req)
}

// ==================== 文件管理 API ====================

func (a *App) ListFiles(folderID string) ([]*api.FileDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.fileAPI.ListFiles(folderID)
}

func (a *App) ListPendingFiles(folderID string) ([]*api.FileDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.fileAPI.ListPendingFiles(folderID)
}

func (a *App) ListFilesByStatus(folderID string, status string) ([]*api.FileDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.fileAPI.ListFilesByStatus(folderID, status)
}

func (a *App) GetFileStats(folderID string) (*api.FileStatsDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.fileAPI.GetFileStats(folderID)
}

func (a *App) DeleteFile(fileID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.fileAPI.DeleteFile(fileID)
}

// ==================== 任务管理 API ====================

func (a *App) CreateTask(folderID string, triggerType string) (*api.TaskDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.CreateTask(folderID, triggerType)
}

func (a *App) RetryFailedTask(taskID string) (*api.TaskDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.RetryFailedTask(taskID)
}

func (a *App) GetTask(taskID string) (*api.TaskDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.GetTask(taskID)
}

func (a *App) ListTasks(folderID string, limit int) ([]*api.TaskDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.ListTasks(folderID, limit)
}

func (a *App) ListRunningTasks() ([]*api.TaskDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.ListRunningTasks()
}

func (a *App) PauseTask(taskID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskControlAPI.PauseTask(taskID)
}

func (a *App) ResumeTask(taskID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskControlAPI.ResumeTask(taskID)
}

func (a *App) StopTask(taskID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskControlAPI.StopTask(taskID)
}

func (a *App) GetRunFiles(taskID string) ([]*api.RunFileDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.GetRunFiles(taskID)
}

func (a *App) GetQueueStats() (*api.QueueStatsDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.GetQueueStats()
}

func (a *App) ListReconcileRequired() ([]*api.ReconcileDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.taskAPI.ListReconcileRequired()
}

func (a *App) ResolveReconcile(runFileID, resolution, remoteDocumentID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskAPI.ResolveReconcile(runFileID, resolution, remoteDocumentID)
}

// ==================== 配置管理 API ====================

func (a *App) ConfigureMaxKB(config api.MaxKBConfigDTO) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.configAPI.ConfigureMaxKB(config)
}

func (a *App) ConfigureMinerU(config api.MinerUConfigDTO) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.configAPI.ConfigureMinerU(config)
}

func (a *App) GetMaxKBConfig() (*api.MaxKBConfigDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.GetMaxKBConfig()
}

func (a *App) GetMinerUConfig() (*api.MinerUConfigDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.GetMinerUConfig()
}

func (a *App) GetMinerUArtifactSettings() (*api.MinerUArtifactSettingsDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.GetMinerUArtifactSettings()
}

func (a *App) ConfigureMinerUArtifactSettings(config api.MinerUArtifactSettingsDTO) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.configAPI.ConfigureMinerUArtifactSettings(config)
}

func (a *App) CleanupMinerUArtifacts() (*api.MinerUArtifactCleanupResultDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.CleanupMinerUArtifacts()
}

func (a *App) TestMaxKBConnection(config api.MaxKBConfigDTO) (string, error) {
	if err := a.requireReady(); err != nil {
		return "", err
	}
	return a.configAPI.TestMaxKBConnection(config)
}

func (a *App) TestMinerUConnection(config api.MinerUConfigDTO) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.configAPI.TestMinerUConnection(config)
}

func (a *App) ValidateCronExpression(expression string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.configAPI.ValidateCronExpression(expression)
}

// ==================== 工具方法 ====================

// SelectDirectory 打开系统原生文件夹选择器
func (a *App) SelectDirectory() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                      "选择本地同步目录",
		CanCreateDirectories:       true,
		ResolvesAliases:            false,
		TreatPackagesAsDirectories: true,
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// ListWorkspaces 获取 MaxKB 工作空间列表
func (a *App) ListWorkspaces() ([]*api.WorkspaceDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.ListWorkspaces()
}

// ListKnowledgeFolders 获取指定工作空间下的知识库目录树
func (a *App) ListKnowledgeFolders(workspaceID string) ([]*api.KnowledgeFolderDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.ListKnowledgeFolders(workspaceID)
}

// ListKnowledgeBases 获取指定工作空间下的知识库列表
func (a *App) ListKnowledgeBases(workspaceID string) ([]*api.KnowledgeBaseDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.ListKnowledgeBases(workspaceID)
}

// ListEmbeddingModels 获取指定工作空间下的向量模型列表
func (a *App) ListEmbeddingModels(workspaceID string) ([]*api.EmbeddingModelDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.ListEmbeddingModels(workspaceID)
}

// CreateKnowledgeBase 创建知识库
func (a *App) CreateKnowledgeBase(req api.CreateKnowledgeBaseDTO) (*api.KnowledgeBaseDTO, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	return a.configAPI.CreateKnowledgeBase(req)
}

// Greet 示例方法（可保留用于测试）
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// GetAppVersion 获取应用版本
func (a *App) GetAppVersion() string {
	return appVersion
}

// GetDataDirectory 获取数据目录
func (a *App) GetDataDirectory() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	paths, err := platform.ResolveStoragePaths(homeDir)
	if err != nil {
		return ""
	}
	return paths.Root
}

// ==================== 任务控制 API ====================

// EnableTask 启用同步任务
func (a *App) EnableTask(folderID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskControlAPI.EnableTask(folderID)
}

// DisableTask 关闭同步任务
func (a *App) DisableTask(folderID string) error {
	if err := a.requireReady(); err != nil {
		return err
	}
	return a.taskControlAPI.DisableTask(folderID)
}
