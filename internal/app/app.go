package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/infra/credential"
	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/repository"
	"maxkb-local-file-sync/internal/service"
)

// Application 应用主结构
type Application struct {
	ctx context.Context

	// 基础设施
	db        *db.DB
	logger    *logger.Logger
	credStore credential.Store

	// 适配器
	maxkbAdapter  adapter.MaxKBAdapter
	mineruAdapter adapter.MinerUAdapter

	// 仓储
	folderRepo         repository.SyncFolderRepository
	fileRepo           repository.SyncFileRepository
	taskRepo           repository.SyncTaskRepository
	runFileRepo        repository.RunFileRepository
	systemSettingsRepo repository.SystemSettingsRepository
	reliability        *repository.ReliabilityStore

	// 服务
	fileScanner        *service.FileScanner
	snapshotSvc        *service.SnapshotService
	taskSvc            *service.TaskService
	syncExecutor       *service.SyncExecutor
	orchestrator       *service.TaskOrchestrator
	cronSvc            *service.CronService
	artifactCleanupSvc *service.MinerUArtifactCleanupService
	maxkbReconciler    *service.MaxKBReconciler
}

// Config 应用配置
type Config struct {
	DataDir     string
	SnapshotDir string
	LogDir      string
	LogLevel    logger.Level
}

// NewApplication 创建应用实例
func NewApplication(cfg Config) (*Application, error) {
	ctx := context.Background()

	// 初始化日志
	log, err := logger.New(logger.Config{
		Level:       cfg.LogLevel,
		LogDir:      cfg.LogDir,
		LogFileName: "maxkb_sync.log",
		Sanitize:    true,
		MaxFileSize: 100 * 1024 * 1024, // 100MB
		MaxBackups:  10,
		Console:     false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// 初始化数据库
	database, err := db.New(db.Config{
		DataDir: cfg.DataDir,
		DBName:  "maxkb_sync.db",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	// 生产启动必须执行版本化迁移；InitSchema 仅保留给测试快速建库。
	if err := database.MigrateUp(db.MigrationsFS); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	// 初始化凭据存储
	credStore, err := credential.NewStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create credential store: %w", err)
	}

	// 初始化仓储
	folderRepo := repository.NewSyncFolderRepository(database)
	fileRepo := repository.NewSyncFileRepository(database)
	taskRepo := repository.NewSyncTaskRepository(database)
	runFileRepo := repository.NewRunFileRepository(database)
	systemSettingsRepo := repository.NewSystemSettingsRepository(database)
	reliability := repository.NewReliabilityStore(database)

	// 在 durable worker 启动前从凭据存储恢复适配器。只有完成连接校验的
	// 配置才是可用配置；URL/凭据存在但 validation_success=0 的记录只是草稿。
	// 适配器不能等到
	// worker 已经领取批次后才异步加载，否则恢复批次会以配置缺失失败。
	var maxkbAdapter adapter.MaxKBAdapter
	var maxkbURL, mineruBaseURL, mineruMode string
	var maxkbValid, mineruValid, mineruEnabled int
	if err := database.QueryRow(`SELECT maxkb_normalized_base_url,maxkb_validation_success,mineru_base_url,mineru_mode,mineru_enabled,mineru_validation_success FROM system_settings WHERE id=1`).Scan(&maxkbURL, &maxkbValid, &mineruBaseURL, &mineruMode, &mineruEnabled, &mineruValid); err != nil {
		return nil, fmt.Errorf("failed to load persisted service settings: %w", err)
	}
	if maxkbValid == 1 && maxkbURL != "" {
		if apiKey, e2 := credStore.Get(credential.MaxKBAPIKey); e2 == nil && strings.TrimSpace(apiKey) != "" {
			maxkbAdapter = adapter.NewMaxKBAdapter(adapter.MaxKBConfig{BaseURL: maxkbURL, APIKey: apiKey, MaxRetries: 3, EnableDebug: false})
		}
	}
	var mineruAdapter adapter.MinerUAdapter
	if mineruValid == 1 && mineruEnabled == 1 && mineruMode != "" && mineruBaseURL != "" {
		apiKey, e2 := credStore.Get(credential.MinerUAPIKey)
		if e2 == nil && (mineruMode == adapter.MinerUModeInternal || strings.TrimSpace(apiKey) != "") {
			mineruAdapter = adapter.NewMinerUAdapter(adapter.MinerUConfig{BaseURL: mineruBaseURL, APIKey: apiKey, Mode: mineruMode, MaxRetries: 3, EnableDebug: false})
		}
	}

	// 初始化服务
	fileScanner := service.NewFileScanner(fileRepo, folderRepo)
	snapshotSvc := service.NewSnapshotService(cfg.SnapshotDir)
	taskSvc := service.NewTaskService(taskRepo, folderRepo, fileRepo, runFileRepo, log)
	taskSvc.SetReliabilityStore(reliability)
	syncExecutor := service.NewSyncExecutor(maxkbAdapter, mineruAdapter, fileRepo, folderRepo, runFileRepo, snapshotSvc, log)
	syncExecutor.SetSystemSettingsRepository(systemSettingsRepo)
	syncExecutor.SetReliabilityStore(reliability)
	orchestrator := service.NewTaskOrchestrator(taskSvc, syncExecutor, runFileRepo, log)
	orchestrator.SetReliabilityStore(reliability)
	cronSvc := service.NewCronService(folderRepo, taskSvc, orchestrator, fileScanner, log)
	artifactCleanupSvc := service.NewMinerUArtifactCleanupService(database, systemSettingsRepo, log)
	maxkbReconciler := service.NewMaxKBReconciler(maxkbAdapter, reliability, folderRepo, log)

	app := &Application{
		ctx:                ctx,
		db:                 database,
		logger:             log,
		credStore:          credStore,
		maxkbAdapter:       maxkbAdapter,
		mineruAdapter:      mineruAdapter,
		folderRepo:         folderRepo,
		fileRepo:           fileRepo,
		taskRepo:           taskRepo,
		runFileRepo:        runFileRepo,
		systemSettingsRepo: systemSettingsRepo,
		reliability:        reliability,
		fileScanner:        fileScanner,
		snapshotSvc:        snapshotSvc,
		taskSvc:            taskSvc,
		syncExecutor:       syncExecutor,
		orchestrator:       orchestrator,
		cronSvc:            cronSvc,
		artifactCleanupSvc: artifactCleanupSvc,
		maxkbReconciler:    maxkbReconciler,
	}

	return app, nil
}

// Start 启动应用
func (a *Application) Start() error {
	a.logger.Info("Starting MaxKB Local File Sync application")

	// 必须在启动 worker 前完成恢复，避免恢复事务把刚领取的 RUNNING 批次
	// 再次放回队列，造成同一批次并发执行。
	if err := a.orchestrator.RecoverAllTasks(a.ctx); err != nil {
		return fmt.Errorf("failed to recover durable tasks: %w", err)
	}

	if err := a.orchestrator.Start(a.ctx); err != nil {
		return fmt.Errorf("failed to start orchestrator: %w", err)
	}

	if err := a.cronSvc.Start(a.ctx); err != nil {
		_ = a.orchestrator.Stop(a.ctx)
		return fmt.Errorf("failed to start cron service: %w", err)
	}
	if err := a.artifactCleanupSvc.Start(a.ctx); err != nil {
		_ = a.cronSvc.Stop(a.ctx)
		_ = a.orchestrator.Stop(a.ctx)
		return fmt.Errorf("failed to start MinerU artifact cleanup service: %w", err)
	}
	if err := a.maxkbReconciler.Start(a.ctx); err != nil {
		_ = a.artifactCleanupSvc.Stop(a.ctx)
		_ = a.cronSvc.Stop(a.ctx)
		_ = a.orchestrator.Stop(a.ctx)
		return fmt.Errorf("failed to start MaxKB reconciliation service: %w", err)
	}

	a.logger.Info("Application started successfully")
	return nil
}

// Stop 停止应用
func (a *Application) Stop() error {
	a.logger.Info("Stopping application")

	// 停止 MaxKB 异步对账服务，避免数据库关闭后仍有后台查询。
	if err := a.maxkbReconciler.Stop(a.ctx); err != nil {
		a.logger.ErrorWithErr("Failed to stop MaxKB reconciliation service", err)
	}

	// 停止 MinerU 产物清理调度器
	if err := a.artifactCleanupSvc.Stop(a.ctx); err != nil {
		a.logger.ErrorWithErr("Failed to stop MinerU artifact cleanup service", err)
	}

	// 停止 Cron 调度器
	if err := a.cronSvc.Stop(a.ctx); err != nil {
		a.logger.ErrorWithErr("Failed to stop cron service", err)
	}

	// 停止任务编排器
	if err := a.orchestrator.Stop(a.ctx); err != nil {
		a.logger.ErrorWithErr("Failed to stop orchestrator", err)
	}

	// 关闭数据库
	if err := a.db.Close(); err != nil {
		a.logger.ErrorWithErr("Failed to close database", err)
	}

	// 关闭日志
	if err := a.logger.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to close application logger: %s\n", logger.SanitizeError(err))
	}

	return nil
}

// GetLogger 获取日志记录器
func (a *Application) GetLogger() *logger.Logger {
	return a.logger
}

// GetDB 获取数据库
func (a *Application) GetDB() *db.DB {
	return a.db
}

// GetCredStore 获取凭据存储
func (a *Application) GetCredStore() credential.Store {
	return a.credStore
}

// ServiceSettings is non-secret persisted configuration. Credential values
// are intentionally excluded; callers must retrieve them from the OS store.
type ServiceSettings struct {
	BaseURL           string
	Mode              string
	Enabled           bool
	ValidationSuccess bool
}

func (a *Application) MaxKBSettings() (ServiceSettings, error) {
	var s ServiceSettings
	var valid int
	if err := a.db.QueryRow(`SELECT maxkb_base_url,maxkb_validation_success FROM system_settings WHERE id=1`).Scan(&s.BaseURL, &valid); err != nil {
		return s, fmt.Errorf("load MaxKB settings: %w", err)
	}
	s.ValidationSuccess = valid == 1
	return s, nil
}

func (a *Application) MinerUSettings() (ServiceSettings, error) {
	var s ServiceSettings
	var enabled, valid int
	if err := a.db.QueryRow(`SELECT mineru_base_url,mineru_mode,mineru_enabled,mineru_validation_success FROM system_settings WHERE id=1`).Scan(&s.BaseURL, &s.Mode, &enabled, &valid); err != nil {
		return s, fmt.Errorf("load MinerU settings: %w", err)
	}
	s.Enabled = enabled == 1
	s.ValidationSuccess = valid == 1
	return s, nil
}

// MinerUArtifactSettings returns system-wide, non-secret result retention settings.
func (a *Application) MinerUArtifactSettings() (repository.MinerUArtifactSettings, error) {
	return a.systemSettingsRepo.GetMinerUArtifactSettings(a.ctx)
}

// ConfigureMinerUArtifactSettings persists system-wide, non-secret result
// retention settings. It intentionally does not alter the running pipeline;
// execution code can consume the repository contract in a later change.
func (a *Application) ConfigureMinerUArtifactSettings(settings repository.MinerUArtifactSettings) error {
	if err := a.systemSettingsRepo.UpdateMinerUArtifactSettings(a.ctx, settings); err != nil {
		return err
	}
	if a.artifactCleanupSvc != nil {
		if err := a.artifactCleanupSvc.Reload(a.ctx); err != nil {
			return fmt.Errorf("reload MinerU artifact cleanup schedule: %w", err)
		}
	}
	return nil
}

func (a *Application) CleanupMinerUArtifacts(ctx context.Context) (service.MinerUArtifactCleanupResult, error) {
	if a.artifactCleanupSvc == nil {
		return service.MinerUArtifactCleanupResult{}, fmt.Errorf("MinerU artifact cleanup service is not configured")
	}
	return a.artifactCleanupSvc.RunNow(ctx)
}

// 导出服务访问器
func (a *Application) FileScanner() *service.FileScanner         { return a.fileScanner }
func (a *Application) SnapshotService() *service.SnapshotService { return a.snapshotSvc }
func (a *Application) TaskService() *service.TaskService         { return a.taskSvc }
func (a *Application) SyncExecutor() *service.SyncExecutor       { return a.syncExecutor }
func (a *Application) Orchestrator() *service.TaskOrchestrator   { return a.orchestrator }
func (a *Application) CronService() *service.CronService         { return a.cronSvc }
func (a *Application) MinerUArtifactCleanupService() *service.MinerUArtifactCleanupService {
	return a.artifactCleanupSvc
}

// 导出仓储访问器
func (a *Application) FolderRepo() repository.SyncFolderRepository { return a.folderRepo }
func (a *Application) FileRepo() repository.SyncFileRepository     { return a.fileRepo }
func (a *Application) TaskRepo() repository.SyncTaskRepository     { return a.taskRepo }
func (a *Application) RunFileRepo() repository.RunFileRepository   { return a.runFileRepo }
func (a *Application) SystemSettingsRepo() repository.SystemSettingsRepository {
	return a.systemSettingsRepo
}
func (a *Application) ReliabilityStore() *repository.ReliabilityStore { return a.reliability }

// TestMaxKBConnection performs a real health request without changing the active adapter.
func (a *Application) TestMaxKBConnection(baseURL, apiKey string) (string, error) {
	normalized, err := credential.ValidateBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(apiKey) == "" || credential.IsMasked(apiKey) {
		return "", fmt.Errorf("MaxKB API key is required")
	}
	client := adapter.NewMaxKBAdapter(adapter.MaxKBConfig{BaseURL: normalized, APIKey: apiKey, MaxRetries: 1})
	profile, err := client.Ping(a.ctx)
	if err != nil {
		return "", err
	}
	// A connection test for unsaved form data is only a probe. It must not turn
	// an unpersisted draft into the active runtime configuration.
	var savedURL string
	if err := a.db.QueryRow(`SELECT maxkb_normalized_base_url FROM system_settings WHERE id=1`).Scan(&savedURL); err != nil {
		return "", fmt.Errorf("read MaxKB draft: %w", err)
	}
	savedKey, err := a.credStore.Get(credential.MaxKBAPIKey)
	if err != nil {
		return "", fmt.Errorf("read MaxKB credential: %w", err)
	}
	if savedURL != normalized || savedKey != apiKey {
		return profile.Version, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`UPDATE system_settings SET maxkb_validation_success=1,maxkb_version=?,maxkb_version_display=?,maxkb_last_validated_at=?,updated_at=? WHERE id=1`, profile.Version, profile.VersionDisplay, now, now); err != nil {
		return "", fmt.Errorf("persist MaxKB validation result: %w", err)
	}
	a.maxkbAdapter = adapter.NewMaxKBAdapter(adapter.MaxKBConfig{BaseURL: normalized, APIKey: apiKey, MaxRetries: 3, EnableDebug: false})
	a.syncExecutor.SetAdapters(a.maxkbAdapter, a.mineruAdapter)
	a.maxkbReconciler.SetAdapter(a.maxkbAdapter)
	return profile.Version, nil
}
func (a *Application) TestMinerUConnection(baseURL, apiKey, mode string) error {
	normalized, err := credential.ValidateBaseURL(baseURL)
	if err != nil {
		return err
	}
	if mode != adapter.MinerUModeOnline && mode != adapter.MinerUModeInternal {
		return fmt.Errorf("unsupported MinerU mode: %s", mode)
	}
	if mode == adapter.MinerUModeOnline && (strings.TrimSpace(apiKey) == "" || credential.IsMasked(apiKey)) {
		return fmt.Errorf("MinerU API key is required for online mode")
	}
	client := adapter.NewMinerUAdapter(adapter.MinerUConfig{BaseURL: normalized, APIKey: apiKey, Mode: mode, MaxRetries: 1})
	if err := client.Ping(a.ctx); err != nil {
		return err
	}
	var savedURL, savedMode string
	if err := a.db.QueryRow(`SELECT mineru_base_url,mineru_mode FROM system_settings WHERE id=1`).Scan(&savedURL, &savedMode); err != nil {
		return fmt.Errorf("read MinerU draft: %w", err)
	}
	savedKey, err := a.credStore.Get(credential.MinerUAPIKey)
	if err != nil {
		return fmt.Errorf("read MinerU credential: %w", err)
	}
	if savedURL != normalized || savedMode != mode || savedKey != apiKey {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`UPDATE system_settings SET mineru_validation_success=1,mineru_last_validated_at=?,updated_at=? WHERE id=1`, now, now); err != nil {
		return fmt.Errorf("persist MinerU validation result: %w", err)
	}
	a.mineruAdapter = adapter.NewMinerUAdapter(adapter.MinerUConfig{BaseURL: normalized, APIKey: apiKey, Mode: mode, MaxRetries: 3, EnableDebug: false})
	a.syncExecutor.SetAdapters(a.maxkbAdapter, a.mineruAdapter)
	return nil
}

// ListWorkspaces 获取 MaxKB 工作空间列表
func (a *Application) ListWorkspaces(ctx context.Context) ([]*adapter.Workspace, error) {
	if a.maxkbAdapter == nil {
		return nil, fmt.Errorf("MaxKB 未配置，请先在设置页面配置 MaxKB 连接")
	}
	return a.maxkbAdapter.ListWorkspaces(ctx)
}

// ListKnowledgeFolders 获取指定工作空间下的知识库目录树
func (a *Application) ListKnowledgeFolders(ctx context.Context, workspaceID string) ([]*adapter.KnowledgeFolder, error) {
	if a.maxkbAdapter == nil {
		return nil, fmt.Errorf("MaxKB 未配置，请先在设置页面配置 MaxKB 连接")
	}
	return a.maxkbAdapter.ListKnowledgeFolders(ctx, workspaceID)
}

// ListKnowledgeBases 获取指定工作空间下的知识库列表
func (a *Application) ListKnowledgeBases(ctx context.Context, workspaceID string) ([]*adapter.KnowledgeBase, error) {
	if a.maxkbAdapter == nil {
		return nil, fmt.Errorf("MaxKB 未配置，请先在设置页面配置 MaxKB 连接")
	}
	return a.maxkbAdapter.ListKnowledgeBases(ctx, workspaceID)
}

// ListEmbeddingModels 获取指定工作空间下的向量模型列表
func (a *Application) ListEmbeddingModels(ctx context.Context, workspaceID string) ([]*adapter.EmbeddingModel, error) {
	if a.maxkbAdapter == nil {
		return nil, fmt.Errorf("MaxKB 未配置，请先在设置页面配置 MaxKB 连接")
	}
	return a.maxkbAdapter.ListEmbeddingModels(ctx, workspaceID)
}

// CreateKnowledgeBase 创建知识库
func (a *Application) CreateKnowledgeBase(ctx context.Context, workspaceID, folderID, name, description, embeddingModelID string) (*adapter.KnowledgeBase, error) {
	if a.maxkbAdapter == nil {
		return nil, fmt.Errorf("MaxKB 未配置，请先在设置页面配置 MaxKB 连接")
	}
	return a.maxkbAdapter.CreateKnowledgeBase(ctx, &adapter.CreateKnowledgeBaseRequest{
		WorkspaceID:      workspaceID,
		FolderID:         folderID,
		Name:             name,
		Description:      description,
		EmbeddingModelID: embeddingModelID,
	})
}

// ConfigureMaxKB 配置 MaxKB 适配器
func (a *Application) ConfigureMaxKB(baseURL, apiKey string) error {
	normalized, err := credential.ValidateBaseURL(baseURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(apiKey) == "" || credential.IsMasked(apiKey) {
		return fmt.Errorf("MaxKB API key is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`UPDATE system_settings SET maxkb_base_url=?,maxkb_normalized_base_url=?,maxkb_validation_success=0,maxkb_version='',maxkb_version_display='',updated_at=? WHERE id=1`, normalized, normalized, now); err != nil {
		return fmt.Errorf("persist MaxKB configuration: %w", err)
	}
	// Saving a changed configuration creates a draft. It must not be usable
	// until TestMaxKBConnection succeeds.
	a.maxkbAdapter = nil
	a.syncExecutor.SetAdapters(nil, a.mineruAdapter)
	a.maxkbReconciler.SetAdapter(nil)
	a.logger.Info("MaxKB configuration saved as an unvalidated draft")
	return nil
}

// DisableMinerU atomically removes the runtime adapter and persists the
// disabled application state. Folder-level settings remain unchanged so a
// later re-enable does not silently rewrite folder configuration.
func (a *Application) DisableMinerU() error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`UPDATE system_settings SET mineru_enabled=0,mineru_validation_success=0,updated_at=? WHERE id=1`, now); err != nil {
		return fmt.Errorf("persist disabled MinerU configuration: %w", err)
	}
	a.mineruAdapter = nil
	a.syncExecutor.SetAdapters(a.maxkbAdapter, nil)
	a.logger.Info("MinerU adapter disabled")
	return nil
}

// ConfigureMinerU 配置 MinerU 适配器
func (a *Application) ConfigureMinerU(baseURL, apiKey, mode string) error {
	normalized, err := credential.ValidateBaseURL(baseURL)
	if err != nil {
		return err
	}
	if mode != adapter.MinerUModeOnline && mode != adapter.MinerUModeInternal {
		return fmt.Errorf("unsupported MinerU mode: %s", mode)
	}
	if mode == adapter.MinerUModeOnline && (strings.TrimSpace(apiKey) == "" || credential.IsMasked(apiKey)) {
		return fmt.Errorf("MinerU API key is required for online mode")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := a.db.Exec(`UPDATE system_settings SET mineru_base_url=?,mineru_mode=?,mineru_enabled=1,mineru_validation_success=0,updated_at=? WHERE id=1`, normalized, mode, now); err != nil {
		return fmt.Errorf("persist MinerU configuration: %w", err)
	}
	a.mineruAdapter = nil
	a.syncExecutor.SetAdapters(a.maxkbAdapter, nil)
	a.logger.Info("MinerU configuration saved as an unvalidated draft")
	return nil
}
