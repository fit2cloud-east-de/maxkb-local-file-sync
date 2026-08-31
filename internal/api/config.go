package api

import (
	"context"
	"fmt"
	"strings"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/infra/credential"
	fileutil "maxkb-local-file-sync/internal/infra/file"
	"maxkb-local-file-sync/internal/repository"
)

// ConfigAPI 配置管理 API（Wails 绑定）
type ConfigAPI struct {
	app *app.Application
}

// NewConfigAPI 创建配置 API
func NewConfigAPI(app *app.Application) *ConfigAPI {
	return &ConfigAPI{app: app}
}

// MaxKBConfig MaxKB 配置
type MaxKBConfigDTO struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

// MinerUConfig MinerU 配置
type MinerUConfigDTO struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Mode    string `json:"mode"` // "online" 或 "internal"
	Enabled bool   `json:"enabled"`
}

// MinerUArtifactSettingsDTO contains only non-secret system-wide result
// retention settings. It is separate from the MinerU connection DTO so the
// frontend cannot accidentally mix credentials with local filesystem policy.
type MinerUArtifactSettingsDTO struct {
	// Deprecated compatibility fields. ZIP retention is now enabled by
	// default and is controlled by cleanupPolicy, not by SaveFullResult.
	SaveFullResult          bool   `json:"saveFullResult,omitempty"`
	ResultSaveDir           string `json:"resultSaveDir"`
	CleanupTemporaryResults bool   `json:"cleanupTemporaryResults,omitempty"`

	CleanupPolicy     string `json:"cleanupPolicy"`
	CleanupAfterValue int    `json:"cleanupAfterValue"`
	CleanupAfterUnit  string `json:"cleanupAfterUnit"`
	// Deprecated compatibility field for older frontends.
	CleanupAfterDays   int    `json:"cleanupAfterDays,omitempty"`
	CleanupKeepBatches int    `json:"cleanupKeepBatches"`
	CleanupCron        string `json:"cleanupCron"`

	// Recent cleanup fields are read-only summaries. They are not accepted as
	// input by ConfigureMinerUArtifactSettings.
	LastCleanupAt           string `json:"lastCleanupAt"`
	LastCleanupStatus       string `json:"lastCleanupStatus"`
	LastCleanupDeletedCount int    `json:"lastCleanupDeletedCount"`
	LastCleanupError        string `json:"lastCleanupError"`
}

// ConfigureMaxKB 配置 MaxKB
func (api *ConfigAPI) ConfigureMaxKB(config MaxKBConfigDTO) error {
	key, restore, err := prepareCredential(api.app.GetCredStore(), credential.MaxKBAPIKey, config.APIKey, true)
	if err != nil {
		return err
	}
	if err := api.app.ConfigureMaxKB(config.BaseURL, key); err != nil {
		_ = restore()
		return err
	}
	if err := cleanupLegacyCredentials(api.app.GetCredStore(), "maxkb_base_url"); err != nil {
		return fmt.Errorf("remove legacy MaxKB URL credential: %w", err)
	}
	return nil
}

// ConfigureMinerU 配置 MinerU
func (api *ConfigAPI) ConfigureMinerU(config MinerUConfigDTO) error {
	if !config.Enabled {
		// Disabling the service is not credential deletion. Keep the token in
		// the system credential store so a later re-enable can reuse it without
		// requiring the user to enter the token again. Credential removal is a
		// separate lifecycle operation and must never be implied by a toggle.
		if err := cleanupLegacyCredentials(api.app.GetCredStore(), "mineru_base_url", "mineru_mode"); err != nil {
			return fmt.Errorf("remove legacy MinerU credentials: %w", err)
		}
		return api.app.DisableMinerU()
	}
	required := config.Mode == "online"
	key, restore, err := prepareCredential(api.app.GetCredStore(), credential.MinerUAPIKey, config.APIKey, required)
	if err != nil {
		return err
	}
	if err := api.app.ConfigureMinerU(config.BaseURL, key, config.Mode); err != nil {
		_ = restore()
		return err
	}
	if err := cleanupLegacyCredentials(api.app.GetCredStore(), "mineru_base_url", "mineru_mode"); err != nil {
		return fmt.Errorf("remove legacy MinerU credentials: %w", err)
	}
	return nil
}

// GetMaxKBConfig 获取 MaxKB 配置
func (api *ConfigAPI) GetMaxKBConfig() (*MaxKBConfigDTO, error) {
	settings, err := api.app.MaxKBSettings()
	if err != nil {
		return nil, err
	}
	apiKey, err := api.app.GetCredStore().Get(credential.MaxKBAPIKey)
	if err != nil {
		return nil, fmt.Errorf("read MaxKB credential: %w", err)
	}
	return &MaxKBConfigDTO{
		BaseURL: settings.BaseURL,
		APIKey:  maskedIfConfigured(apiKey),
	}, nil
}

// GetMinerUConfig 获取 MinerU 配置
func (api *ConfigAPI) GetMinerUConfig() (*MinerUConfigDTO, error) {
	settings, err := api.app.MinerUSettings()
	if err != nil {
		return nil, err
	}
	apiKey, err := api.app.GetCredStore().Get(credential.MinerUAPIKey)
	if err != nil {
		return nil, fmt.Errorf("read MinerU credential: %w", err)
	}
	return &MinerUConfigDTO{
		BaseURL: settings.BaseURL,
		APIKey:  maskedIfConfigured(apiKey),
		Mode:    settings.Mode,
		Enabled: settings.Enabled,
	}, nil
}

// GetMinerUArtifactSettings returns non-secret system-wide result retention settings.
func (api *ConfigAPI) GetMinerUArtifactSettings() (*MinerUArtifactSettingsDTO, error) {
	settings, err := api.app.MinerUArtifactSettings()
	if err != nil {
		return nil, err
	}
	return minerUArtifactSettingsDTO(settings), nil
}

// ConfigureMinerUArtifactSettings validates and persists non-secret local
// artifact settings. The directory is normalized but not created here.
// MinerUArtifactCleanupResultDTO is the user-safe result of a local cleanup.
type MinerUArtifactCleanupResultDTO struct {
	Status       string `json:"status"`
	DeletedCount int    `json:"deletedCount"`
	SkippedCount int    `json:"skippedCount"`
	Error        string `json:"error,omitempty"`
	At           string `json:"at"`
}

func (api *ConfigAPI) ConfigureMinerUArtifactSettings(config MinerUArtifactSettingsDTO) error {
	minerUSettings, err := api.app.MinerUSettings()
	if err != nil {
		return fmt.Errorf("read MinerU settings: %w", err)
	}
	resultSaveDir := strings.TrimSpace(config.ResultSaveDir)
	if minerUSettings.Enabled && resultSaveDir == "" {
		return fmt.Errorf("MinerU enabled requires an artifact result save directory")
	}
	if resultSaveDir != "" {
		normalized, err := fileutil.NormalizePath(resultSaveDir)
		if err != nil {
			return fmt.Errorf("invalid MinerU result save directory: %w", err)
		}
		resultSaveDir = normalized
	}

	policy := strings.TrimSpace(config.CleanupPolicy)
	if policy == "" {
		policy = repository.MinerUCleanupPolicyNever
	}
	if policy == repository.MinerUCleanupPolicyAfterDays {
		policy = repository.MinerUCleanupPolicyAfterDuration
	}
	afterValue := config.CleanupAfterValue
	if afterValue <= 0 && config.CleanupAfterDays > 0 {
		afterValue = config.CleanupAfterDays
	}
	afterUnit := strings.ToLower(strings.TrimSpace(config.CleanupAfterUnit))
	if afterUnit == "" {
		afterUnit = "day"
	}
	if afterUnit != "hour" && afterUnit != "day" {
		return fmt.Errorf("MinerU cleanup time unit must be hour or day")
	}
	switch policy {
	case repository.MinerUCleanupPolicyImmediate, repository.MinerUCleanupPolicyNever:
		if afterValue < 0 || config.CleanupKeepBatches < 0 {
			return fmt.Errorf("MinerU cleanup parameters cannot be negative")
		}
	case repository.MinerUCleanupPolicyAfterDuration:
		if afterValue <= 0 {
			return fmt.Errorf("MinerU time cleanup requires a retention value greater than zero")
		}
		if strings.TrimSpace(config.CleanupCron) == "" {
			return fmt.Errorf("MinerU time cleanup requires a Cron expression")
		}
	case repository.MinerUCleanupPolicyKeepBatches:
		if config.CleanupKeepBatches <= 0 {
			return fmt.Errorf("MinerU batch cleanup requires keepBatches greater than zero")
		}
		if strings.TrimSpace(config.CleanupCron) == "" {
			return fmt.Errorf("MinerU batch cleanup requires a Cron expression")
		}
	default:
		return fmt.Errorf("unsupported MinerU cleanup policy: %s", policy)
	}
	cleanupCron := strings.TrimSpace(config.CleanupCron)
	if cleanupCron != "" {
		if err := api.app.CronService().ValidateCronExpression(cleanupCron); err != nil {
			return err
		}
	}

	return api.app.ConfigureMinerUArtifactSettings(repository.MinerUArtifactSettings{
		SaveFullResult:          true,
		ResultSaveDir:           resultSaveDir,
		CleanupTemporaryResults: true,
		CleanupPolicy:           policy,
		CleanupAfterValue:       afterValue,
		CleanupAfterUnit:        afterUnit,
		CleanupAfterDays:        afterValue,
		CleanupKeepBatches:      config.CleanupKeepBatches,
		CleanupCron:             cleanupCron,
	})
}

func minerUArtifactSettingsDTO(settings repository.MinerUArtifactSettings) *MinerUArtifactSettingsDTO {
	lastCleanupAt := ""
	if settings.LastCleanupAt != nil {
		lastCleanupAt = settings.LastCleanupAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	policy := settings.CleanupPolicy
	if policy == repository.MinerUCleanupPolicyAfterDays {
		policy = repository.MinerUCleanupPolicyAfterDuration
	}
	return &MinerUArtifactSettingsDTO{
		SaveFullResult:          true,
		ResultSaveDir:           settings.ResultSaveDir,
		CleanupTemporaryResults: true,
		CleanupPolicy:           policy,
		CleanupAfterValue:       settings.CleanupAfterValue,
		CleanupAfterUnit:        settings.CleanupAfterUnit,
		CleanupAfterDays:        settings.CleanupAfterDays,
		CleanupKeepBatches:      settings.CleanupKeepBatches,
		CleanupCron:             settings.CleanupCron,
		LastCleanupAt:           lastCleanupAt,
		LastCleanupStatus:       settings.LastCleanupStatus,
		LastCleanupDeletedCount: settings.LastCleanupDeletedCount,
		LastCleanupError:        settings.LastCleanupError,
	}
}

// CleanupMinerUArtifacts removes eligible local MinerU result batches only.
func (api *ConfigAPI) CleanupMinerUArtifacts() (*MinerUArtifactCleanupResultDTO, error) {
	result, err := api.app.CleanupMinerUArtifacts(context.Background())
	dto := &MinerUArtifactCleanupResultDTO{Status: result.Status, DeletedCount: result.DeletedCount, SkippedCount: result.SkippedCount, Error: result.Error, At: result.At}
	return dto, err
}

// TestMaxKBConnection 测试 MaxKB 连接
func (api *ConfigAPI) TestMaxKBConnection(config MaxKBConfigDTO) (string, error) {
	key, _, err := resolveCredential(api.app.GetCredStore(), credential.MaxKBAPIKey, config.APIKey, true)
	if err != nil {
		return "", err
	}
	return api.app.TestMaxKBConnection(config.BaseURL, key)
}

// TestMinerUConnection 测试 MinerU 连接
func (api *ConfigAPI) TestMinerUConnection(config MinerUConfigDTO) error {
	key, _, err := resolveCredential(api.app.GetCredStore(), credential.MinerUAPIKey, config.APIKey, config.Mode == "online")
	if err != nil {
		return err
	}
	return api.app.TestMinerUConnection(config.BaseURL, key, config.Mode)
}

func maskedIfConfigured(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return credential.MaskedValue
}

func resolveCredential(store credential.Store, key, input string, required bool) (string, bool, error) {
	if credential.IsMasked(input) || strings.TrimSpace(input) == "" {
		value, err := store.Get(key)
		if err != nil {
			return "", false, fmt.Errorf("read credential: %w", err)
		}
		if required && strings.TrimSpace(value) == "" {
			return "", false, fmt.Errorf("credential is required")
		}
		return value, false, nil
	}
	return input, true, nil
}

func prepareCredential(store credential.Store, key, input string, required bool) (string, func() error, error) {
	old, err := store.Get(key)
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("read existing credential: %w", err)
	}
	value, changed, err := resolveCredential(store, key, input, required)
	if err != nil {
		return "", func() error { return nil }, err
	}
	if !changed {
		return value, func() error { return nil }, nil
	}
	if strings.TrimSpace(value) == "" {
		if err := store.Delete(key); err != nil {
			return "", func() error { return nil }, fmt.Errorf("delete credential: %w", err)
		}
	} else if err := store.Set(key, value); err != nil {
		return "", func() error { return nil }, fmt.Errorf("save credential: %w", err)
	}
	return value, func() error {
		if old == "" {
			return store.Delete(key)
		}
		return store.Set(key, old)
	}, nil
}

func cleanupLegacyCredentials(store credential.Store, keys ...string) error {
	for _, key := range keys {
		if err := store.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCronExpression 验证 Cron 表达式
func (api *ConfigAPI) ValidateCronExpression(expression string) error {
	return api.app.CronService().ValidateCronExpression(expression)
}

// WorkspaceDTO 工作空间 DTO
type WorkspaceDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// KnowledgeBaseDTO 知识库 DTO
type KnowledgeFolderDTO struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Children []*KnowledgeFolderDTO `json:"children,omitempty"`
}

// KnowledgeBaseDTO 知识库 DTO
type KnowledgeBaseDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	WorkspaceID string `json:"workspaceId"`
	FolderID    string `json:"folderId"`
}

// EmbeddingModelDTO 向量模型 DTO
type EmbeddingModelDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// CreateKnowledgeBaseDTO 创建知识库请求 DTO
type CreateKnowledgeBaseDTO struct {
	WorkspaceID      string `json:"workspaceId"`
	FolderID         string `json:"folderId"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	EmbeddingModelID string `json:"embeddingModelId"`
}

// ListWorkspaces 获取工作空间列表
func (api *ConfigAPI) ListWorkspaces() ([]*WorkspaceDTO, error) {
	ctx := context.Background()
	workspaces, err := api.app.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*WorkspaceDTO, 0, len(workspaces))
	for _, ws := range workspaces {
		result = append(result, &WorkspaceDTO{
			ID:          ws.ID,
			Name:        ws.Name,
			Description: ws.Description,
		})
	}
	return result, nil
}

// ListKnowledgeFolders 获取指定工作空间的知识库目录树
func (api *ConfigAPI) ListKnowledgeFolders(workspaceID string) ([]*KnowledgeFolderDTO, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspaceID 不能为空")
	}
	folders, err := api.app.ListKnowledgeFolders(context.Background(), workspaceID)
	if err != nil {
		return nil, err
	}
	return knowledgeFoldersToDTO(folders), nil
}

func knowledgeFoldersToDTO(folders []*adapter.KnowledgeFolder) []*KnowledgeFolderDTO {
	result := make([]*KnowledgeFolderDTO, 0, len(folders))
	for _, folder := range folders {
		if folder == nil {
			continue
		}
		result = append(result, &KnowledgeFolderDTO{ID: folder.ID, Name: folder.Name, Children: knowledgeFoldersToDTO(folder.Children)})
	}
	return result
}

// ListKnowledgeBases 获取指定工作空间的知识库列表
func (api *ConfigAPI) ListKnowledgeBases(workspaceID string) ([]*KnowledgeBaseDTO, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspaceID 不能为空")
	}
	ctx := context.Background()
	kbs, err := api.app.ListKnowledgeBases(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]*KnowledgeBaseDTO, 0, len(kbs))
	for _, kb := range kbs {
		result = append(result, &KnowledgeBaseDTO{
			ID:          kb.ID,
			Name:        kb.Name,
			Description: kb.Description,
			WorkspaceID: kb.WorkspaceID,
			FolderID:    kb.FolderID,
		})
	}
	return result, nil
}

// ListEmbeddingModels 获取指定工作空间的向量模型列表
func (api *ConfigAPI) ListEmbeddingModels(workspaceID string) ([]*EmbeddingModelDTO, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspaceID 不能为空")
	}
	ctx := context.Background()
	models, err := api.app.ListEmbeddingModels(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]*EmbeddingModelDTO, 0, len(models))
	for _, model := range models {
		result = append(result, &EmbeddingModelDTO{
			ID:       model.ID,
			Name:     model.Name,
			Provider: model.Provider,
		})
	}
	return result, nil
}

// CreateKnowledgeBase 创建知识库
func (api *ConfigAPI) CreateKnowledgeBase(req CreateKnowledgeBaseDTO) (*KnowledgeBaseDTO, error) {
	if req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceID 不能为空")
	}
	if req.FolderID == "" {
		return nil, fmt.Errorf("知识库目录不能为空")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("知识库名称不能为空")
	}
	if req.EmbeddingModelID == "" {
		return nil, fmt.Errorf("向量模型不能为空")
	}

	ctx := context.Background()
	kb, err := api.app.CreateKnowledgeBase(ctx, req.WorkspaceID, req.FolderID, req.Name, req.Description, req.EmbeddingModelID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeBaseDTO{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		WorkspaceID: kb.WorkspaceID,
		FolderID:    kb.FolderID,
	}, nil
}
