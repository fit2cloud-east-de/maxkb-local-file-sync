package api

import (
	"context"
	"fmt"
	"time"

	"maxkb-local-file-sync/internal/app"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// FileAPI 文件管理 API（Wails 绑定）
type FileAPI struct {
	app *app.Application
}

// NewFileAPI 创建文件 API
func NewFileAPI(app *app.Application) *FileAPI {
	return &FileAPI{app: app}
}

// FileDTO 文件 DTO
type FileDTO struct {
	FileID         string `json:"fileId"`
	FolderID       string `json:"folderId"`
	RelativePath   string `json:"relativePath"`
	FileStatus     string `json:"fileStatus"`
	ObservedMD5    string `json:"observedMd5"`
	LastSuccessMD5 string `json:"lastSuccessMd5"`
	RemoteDocID    string `json:"remoteDocId"`
	LastSyncedAt   string `json:"lastSyncedAt,omitempty"`
	LastCheckedAt  string `json:"lastCheckedAt,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// ListFiles 列出文件夹下的所有文件
func (api *FileAPI) ListFiles(folderID string) ([]*FileDTO, error) {
	ctx := context.Background()

	files, err := api.app.FileRepo().ListByFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	result := make([]*FileDTO, 0, len(files))
	for _, file := range files {
		result = append(result, toFileDTO(file))
	}

	return result, nil
}

// ListPendingFiles 列出待同步的文件
func (api *FileAPI) ListPendingFiles(folderID string) ([]*FileDTO, error) {
	ctx := context.Background()

	files, err := api.app.FileRepo().ListPendingChanges(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending files: %w", err)
	}

	result := make([]*FileDTO, 0, len(files))
	for _, file := range files {
		result = append(result, toFileDTO(file))
	}

	return result, nil
}

// ListFilesByStatus 根据状态列出文件
func (api *FileAPI) ListFilesByStatus(folderID string, status string) ([]*FileDTO, error) {
	ctx := context.Background()

	fileStatus := types.FileStatus(status)
	files, err := api.app.FileRepo().ListByStatus(ctx, folderID, fileStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to list files by status: %w", err)
	}

	result := make([]*FileDTO, 0, len(files))
	for _, file := range files {
		result = append(result, toFileDTO(file))
	}

	return result, nil
}

// GetFileStats 获取文件统计
func (api *FileAPI) GetFileStats(folderID string) (*FileStatsDTO, error) {
	ctx := context.Background()

	total, err := api.app.FileRepo().CountByFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to count total files: %w", err)
	}

	synced, err := api.app.FileRepo().CountByStatus(ctx, folderID, types.FileStatusSynced)
	if err != nil {
		synced = 0
	}

	pending, err := api.app.FileRepo().CountByStatus(ctx, folderID, types.FileStatusPending)
	if err != nil {
		pending = 0
	}

	stale, err := api.app.FileRepo().CountByStatus(ctx, folderID, types.FileStatusStaleRemoteExists)
	if err != nil {
		stale = 0
	}

	needsDelete, err := api.app.FileRepo().CountByStatus(ctx, folderID, types.FileStatusNeedsDelete)
	if err != nil {
		needsDelete = 0
	}

	return &FileStatsDTO{
		Total:       total,
		Synced:      synced,
		Pending:     pending,
		Stale:       stale,
		NeedsDelete: needsDelete,
	}, nil
}

// DeleteFile 删除文件记录
func (api *FileAPI) DeleteFile(fileID string) error {
	ctx := context.Background()

	if err := api.app.FileRepo().Delete(ctx, fileID); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// FileStatsDTO 文件统计 DTO
type FileStatsDTO struct {
	Total       int `json:"total"`
	Synced      int `json:"synced"`
	Pending     int `json:"pending"`
	Stale       int `json:"stale"`
	NeedsDelete int `json:"needsDelete"`
}

// toFileDTO 转换为文件 DTO
func toFileDTO(file *repository.SyncFile) *FileDTO {
	dto := &FileDTO{
		FileID:         file.FileID,
		FolderID:       file.FolderID,
		RelativePath:   file.RelativePath,
		FileStatus:     string(file.FileStatus),
		ObservedMD5:    file.ObservedMD5,
		LastSuccessMD5: file.LastSuccessMD5,
		RemoteDocID:    file.RemoteDocID,
		CreatedAt:      file.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      file.UpdatedAt.Format(time.RFC3339),
	}

	if file.LastSyncedAt != nil {
		dto.LastSyncedAt = file.LastSyncedAt.Format(time.RFC3339)
	}

	if file.LastCheckedAt != nil {
		dto.LastCheckedAt = file.LastCheckedAt.Format(time.RFC3339)
	}

	return dto
}
