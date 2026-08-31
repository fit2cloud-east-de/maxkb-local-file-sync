package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/infra/file"
	"maxkb-local-file-sync/internal/repository"
)

// FolderAPI 文件夹管理 API
type FolderAPI struct {
	app *app.Application
}

// NewFolderAPI 创建文件夹 API
func NewFolderAPI(app *app.Application) *FolderAPI {
	return &FolderAPI{app: app}
}

// CreateFolderRequest 创建文件夹请求
type CreateFolderRequest struct {
	Name                   string `json:"name"`
	LocalPath              string `json:"localPath"`
	KBId                   string `json:"kbId"`
	WorkspaceId            string `json:"workspaceId"`
	KnowledgeFolderId      string `json:"knowledgeFolderId"`
	WorkspaceName          string `json:"workspaceName"`
	KnowledgeName          string `json:"knowledgeName"`
	MaxKBBaseURLSnapshot   string `json:"maxkbBaseUrlSnapshot"`
	EnableMinerU           bool   `json:"enableMinerU"`
	CronExpression         string `json:"cronExpression"`
	CronEnabled            bool   `json:"cronEnabled"`
	SyncDeleteLocalRemoved bool   `json:"syncDeleteLocalRemoved"`
	MinerURetryCount       int    `json:"mineruRetryCount"`
	MinerURequestTimeout   int    `json:"mineruRequestTimeout"`
	MinerUTaskTimeout      int    `json:"mineruTaskTimeout"`
	MinerUPollInterval     int    `json:"mineruPollInterval"`
	MinerUSaveFullResult   bool   `json:"mineruSaveFullResult"`
	MinerUResultSaveDir    string `json:"mineruResultSaveDir"`
	IncludePatterns        string `json:"includePatterns"`
	ExcludePatterns        string `json:"excludePatterns"`
	MinerUFileExtensions   string `json:"mineruFileExtensions"`
}

// FolderDTO 文件夹 DTO
type FolderDTO struct {
	FolderId               string  `json:"folderId"`
	Name                   string  `json:"name"`
	LocalPath              string  `json:"localPath"`
	KBId                   string  `json:"kbId"`
	WorkspaceId            string  `json:"workspaceId"`
	KnowledgeFolderId      string  `json:"knowledgeFolderId"`
	WorkspaceName          string  `json:"workspaceName"`
	KnowledgeName          string  `json:"knowledgeName"`
	MaxKBBaseURLSnapshot   string  `json:"maxkbBaseUrlSnapshot"`
	EnableMinerU           bool    `json:"enableMinerU"`
	CronExpression         string  `json:"cronExpression"`
	CronEnabled            bool    `json:"cronEnabled"`
	Enabled                bool    `json:"enabled"`
	DisabledAt             *string `json:"disabledAt,omitempty"`
	SyncDeleteLocalRemoved bool    `json:"syncDeleteLocalRemoved"`
	MinerURetryCount       int     `json:"mineruRetryCount"`
	MinerURequestTimeout   int     `json:"mineruRequestTimeout"`
	MinerUTaskTimeout      int     `json:"mineruTaskTimeout"`
	MinerUPollInterval     int     `json:"mineruPollInterval"`
	MinerUSaveFullResult   bool    `json:"mineruSaveFullResult"`
	MinerUResultSaveDir    string  `json:"mineruResultSaveDir"`
	IncludePatterns        string  `json:"includePatterns"`
	ExcludePatterns        string  `json:"excludePatterns"`
	MinerUFileExtensions   string  `json:"mineruFileExtensions"`
	NextExecutionAt        *string `json:"nextExecutionAt,omitempty"`
	CreatedAt              string  `json:"createdAt"`
	UpdatedAt              string  `json:"updatedAt"`
}

// ScanResultDTO 扫描结果 DTO
type ScanResultDTO struct {
	NewFiles       []string          `json:"newFiles"`
	UpdatedFiles   []string          `json:"updatedFiles"`
	DeletedFiles   []string          `json:"deletedFiles"`
	RenamedFiles   map[string]string `json:"renamedFiles"`
	UnchangedFiles []string          `json:"unchangedFiles"`
}

// PreviewMatchRequest 预览匹配请求
type PreviewMatchRequest struct {
	LocalPath            string `json:"localPath"`
	IncludePatterns      string `json:"includePatterns"`
	ExcludePatterns      string `json:"excludePatterns"`
	EnableMinerU         bool   `json:"enableMinerU"`
	MinerUFileExtensions string `json:"mineruFileExtensions"`
}

// PreviewMatchResult 预览匹配结果
type PreviewMatchResult struct {
	TotalFiles       int               `json:"totalFiles"`
	MatchedFiles     []string          `json:"matchedFiles"`
	ExcludedFiles    []string          `json:"excludedFiles"`
	ExclusionReasons map[string]string `json:"exclusionReasons,omitempty"`
	MinerUFiles      []string          `json:"mineruFiles"`
	RegularFiles     []string          `json:"regularFiles"`
}

func validateFolderPathAvailable(ctx context.Context, repo repository.SyncFolderRepository, normalizedPath, currentFolderID string) error {
	existing, err := repo.GetByLocalPath(ctx, normalizedPath)
	if err == nil {
		if existing != nil && existing.FolderID != currentFolderID {
			name := strings.TrimSpace(existing.Name)
			if name == "" {
				name = "未命名任务"
			}
			return fmt.Errorf("本地文件夹已绑定同步任务“%s”，不能重复创建。请编辑已有任务或选择其他文件夹", name)
		}
		return nil
	}
	if !errors.Is(err, repository.ErrSyncFolderNotFound) {
		return err
	}
	return nil
}

func userFacingFolderPathError(err error) error {
	if errors.Is(err, repository.ErrSyncFolderPathConflict) {
		return fmt.Errorf("本地文件夹已绑定其他同步任务，不能重复创建。请编辑已有任务或选择其他文件夹")
	}
	return err
}

// validateFolderTarget 校验同步任务必填的本地目录、目标工作空间和知识库。
// 这项校验同时放在后端，避免绕过前端直接调用 Wails 接口创建不完整任务。
func validateFolderTarget(req CreateFolderRequest) error {
	if strings.TrimSpace(req.LocalPath) == "" {
		return errors.New("请选择本地文件夹")
	}
	if strings.TrimSpace(req.WorkspaceId) == "" {
		return errors.New("请选择目标工作区")
	}
	if strings.TrimSpace(req.KBId) == "" {
		return errors.New("请选择知识库")
	}
	return nil
}

// CreateFolder 创建文件夹
func (api *FolderAPI) CreateFolder(req CreateFolderRequest) (*FolderDTO, error) {
	ctx := context.Background()

	if err := validateFolderTarget(req); err != nil {
		return nil, err
	}

	// 规范化路径
	normalizedPath, err := file.NormalizePath(req.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize path: %w", err)
	}

	if err := validateFolderPathAvailable(ctx, api.app.FolderRepo(), normalizedPath, ""); err != nil {
		return nil, err
	}

	maxkbSettings, settingsErr := api.app.MaxKBSettings()
	if settingsErr != nil {
		return nil, settingsErr
	}
	maxkbSnapshot := req.MaxKBBaseURLSnapshot
	if strings.TrimSpace(maxkbSnapshot) == "" {
		maxkbSnapshot = maxkbSettings.BaseURL
	}
	folder := &repository.SyncFolder{
		FolderID:               generateFolderID(),
		Name:                   req.Name,
		LocalPath:              normalizedPath,
		KBId:                   req.KBId,
		WorkspaceID:            req.WorkspaceId,
		MaxKBBaseURLSnapshot:   maxkbSnapshot,
		WorkspaceName:          req.WorkspaceName,
		KnowledgeFolderID:      req.KnowledgeFolderId,
		KnowledgeName:          req.KnowledgeName,
		EnableMinerU:           req.EnableMinerU,
		CronExpression:         req.CronExpression,
		CronEnabled:            req.CronEnabled,
		Enabled:                true,
		SyncDeleteLocalRemoved: req.SyncDeleteLocalRemoved,
		MinerURetryCount:       req.MinerURetryCount,
		MinerURequestTimeout:   req.MinerURequestTimeout,
		MinerUTaskTimeout:      req.MinerUTaskTimeout,
		MinerUPollInterval:     req.MinerUPollInterval,
		MinerUSaveFullResult:   req.MinerUSaveFullResult,
		MinerUResultSaveDir:    req.MinerUResultSaveDir,
		IncludePatterns:        req.IncludePatterns,
		ExcludePatterns:        req.ExcludePatterns,
		MinerUFileExtensions:   req.MinerUFileExtensions,
	}

	if err := api.app.FolderRepo().Create(ctx, folder); err != nil {
		return nil, userFacingFolderPathError(err)
	}

	// 添加 Cron 调度
	if req.CronEnabled && req.CronExpression != "" {
		if err := api.app.CronService().AddSchedule(ctx, folder.FolderID); err != nil {
			api.app.GetLogger().ErrorWithErr("Failed to add cron schedule", err)
		}
	}

	return api.toDTO(folder), nil
}

// UpdateFolder 更新文件夹
func (api *FolderAPI) UpdateFolder(folderID string, req CreateFolderRequest) (*FolderDTO, error) {
	ctx := context.Background()

	if err := validateFolderTarget(req); err != nil {
		return nil, err
	}

	folder, err := api.app.FolderRepo().GetByID(ctx, folderID)
	if err != nil {
		return nil, err
	}

	// 规范化路径
	normalizedPath, err := file.NormalizePath(req.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize path: %w", err)
	}

	if err := validateFolderPathAvailable(ctx, api.app.FolderRepo(), normalizedPath, folderID); err != nil {
		return nil, err
	}

	// 更新字段
	folder.Name = req.Name
	folder.LocalPath = normalizedPath
	folder.KBId = req.KBId
	folder.WorkspaceID = req.WorkspaceId
	if strings.TrimSpace(req.MaxKBBaseURLSnapshot) != "" {
		folder.MaxKBBaseURLSnapshot = req.MaxKBBaseURLSnapshot
	}
	folder.WorkspaceName = req.WorkspaceName
	folder.KnowledgeFolderID = req.KnowledgeFolderId
	folder.KnowledgeName = req.KnowledgeName
	folder.EnableMinerU = req.EnableMinerU
	folder.CronExpression = req.CronExpression
	folder.CronEnabled = req.CronEnabled
	folder.SyncDeleteLocalRemoved = req.SyncDeleteLocalRemoved
	folder.MinerURetryCount = req.MinerURetryCount
	folder.MinerURequestTimeout = req.MinerURequestTimeout
	folder.MinerUTaskTimeout = req.MinerUTaskTimeout
	folder.MinerUPollInterval = req.MinerUPollInterval
	folder.MinerUSaveFullResult = req.MinerUSaveFullResult
	folder.MinerUResultSaveDir = req.MinerUResultSaveDir
	folder.IncludePatterns = req.IncludePatterns
	folder.ExcludePatterns = req.ExcludePatterns
	folder.MinerUFileExtensions = req.MinerUFileExtensions

	if err := api.app.FolderRepo().Update(ctx, folder); err != nil {
		return nil, userFacingFolderPathError(err)
	}

	// 更新 Cron 调度
	if req.CronEnabled && req.CronExpression != "" {
		if err := api.app.CronService().AddSchedule(ctx, folder.FolderID); err != nil {
			api.app.GetLogger().ErrorWithErr("Failed to update cron schedule", err)
		}
	} else {
		if err := api.app.CronService().RemoveSchedule(ctx, folder.FolderID); err != nil {
			api.app.GetLogger().ErrorWithErr("Failed to remove cron schedule", err)
		}
	}

	return api.toDTO(folder), nil
}

// DeleteFolder 删除文件夹
func (api *FolderAPI) DeleteFolder(folderID string) error {
	ctx := context.Background()

	// 移除 Cron 调度
	if err := api.app.CronService().RemoveSchedule(ctx, folderID); err != nil {
		api.app.GetLogger().ErrorWithErr("Failed to remove cron schedule", err)
	}

	return api.app.FolderRepo().Delete(ctx, folderID)
}

// GetFolder 获取文件夹详情
func (api *FolderAPI) GetFolder(folderID string) (*FolderDTO, error) {
	ctx := context.Background()
	folder, err := api.app.FolderRepo().GetByID(ctx, folderID)
	if err != nil {
		return nil, err
	}
	return api.toDTO(folder), nil
}

// ListFolders 列出所有文件夹
func (api *FolderAPI) ListFolders() ([]*FolderDTO, error) {
	ctx := context.Background()
	folders, err := api.app.FolderRepo().List(ctx)
	if err != nil {
		return nil, err
	}

	dtos := make([]*FolderDTO, len(folders))
	for i, f := range folders {
		dtos[i] = api.toDTO(f)
	}
	return dtos, nil
}

// ScanFolder 扫描文件夹
func (api *FolderAPI) ScanFolder(folderID string) (*ScanResultDTO, error) {
	ctx := context.Background()

	// 一次扫描同时返回预览差异并持久化状态。先 DetectChanges 再重新
	// ScanFolder 会把刚创建/更新的记录看成 unchanged，导致 UI 显示错误。
	result, err := api.app.FileScanner().DetectChangesWithResult(ctx, folderID)
	if err != nil {
		return nil, err
	}

	return &ScanResultDTO{
		NewFiles:       result.NewFiles,
		UpdatedFiles:   result.UpdatedFiles,
		DeletedFiles:   result.DeletedFiles,
		RenamedFiles:   result.RenamedFiles,
		UnchangedFiles: result.UnchangedFiles,
	}, nil
}

// DetectChanges 检测变更
func (api *FolderAPI) DetectChanges(folderID string) (string, error) {
	ctx := context.Background()
	changeType, err := api.app.FileScanner().DetectChanges(ctx, folderID)
	if err != nil {
		return "", err
	}

	return string(*changeType), nil
}

// PreviewMatch 预览文件匹配结果
func (api *FolderAPI) PreviewMatch(req PreviewMatchRequest) (*PreviewMatchResult, error) {
	ctx := context.Background()
	preview, err := api.app.FileScanner().PreviewMatch(ctx, req.LocalPath, req.IncludePatterns, req.ExcludePatterns, req.EnableMinerU, previewMinerUExtensions(req.EnableMinerU, req.MinerUFileExtensions))
	if err != nil {
		return nil, err
	}
	return &PreviewMatchResult{
		TotalFiles: preview.TotalFiles, MatchedFiles: preview.MatchedFiles,
		ExcludedFiles: preview.ExcludedFiles, ExclusionReasons: preview.ExclusionReasons,
		MinerUFiles: preview.MinerUFiles, RegularFiles: preview.RegularFiles,
	}, nil
}

func previewMinerUExtensions(enabled bool, input string) []string {
	if !enabled {
		return nil
	}
	return parsePreviewExtensions(input)
}

func parsePreviewExtensions(input string) []string {
	parts := strings.Split(input, ",")
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		ext := strings.ToLower(strings.TrimSpace(part))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		result = append(result, ext)
	}
	return result
}

// toDTO 转换为 DTO
func (api *FolderAPI) toDTO(folder *repository.SyncFolder) *FolderDTO {
	dto := &FolderDTO{
		FolderId:               folder.FolderID,
		Name:                   folder.Name,
		LocalPath:              folder.LocalPath,
		KBId:                   folder.KBId,
		WorkspaceId:            folder.WorkspaceID,
		KnowledgeFolderId:      folder.KnowledgeFolderID,
		WorkspaceName:          folder.WorkspaceName,
		KnowledgeName:          folder.KnowledgeName,
		MaxKBBaseURLSnapshot:   folder.MaxKBBaseURLSnapshot,
		EnableMinerU:           folder.EnableMinerU,
		CronExpression:         folder.CronExpression,
		CronEnabled:            folder.CronEnabled,
		Enabled:                folder.Enabled,
		SyncDeleteLocalRemoved: folder.SyncDeleteLocalRemoved,
		MinerURetryCount:       folder.MinerURetryCount,
		MinerURequestTimeout:   folder.MinerURequestTimeout,
		MinerUTaskTimeout:      folder.MinerUTaskTimeout,
		MinerUPollInterval:     folder.MinerUPollInterval,
		MinerUSaveFullResult:   folder.MinerUSaveFullResult,
		MinerUResultSaveDir:    folder.MinerUResultSaveDir,
		IncludePatterns:        folder.IncludePatterns,
		ExcludePatterns:        folder.ExcludePatterns,
		MinerUFileExtensions:   folder.MinerUFileExtensions,
		CreatedAt:              folder.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:              folder.UpdatedAt.Format("2006-01-02 15:04:05"),
	}

	if folder.DisabledAt != nil {
		t := folder.DisabledAt.Format("2006-01-02 15:04:05")
		dto.DisabledAt = &t
	}

	if folder.NextExecutionAt != nil {
		t := folder.NextExecutionAt.Format("2006-01-02 15:04:05")
		dto.NextExecutionAt = &t
	}

	return dto
}

// generateFolderID 生成文件夹 ID
func generateFolderID() string {
	return fmt.Sprintf("folder_%d", time.Now().UnixMilli())
}
