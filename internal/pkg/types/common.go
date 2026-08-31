package types

import "time"

// TriggerType 定义批次触发类型
type TriggerType string

const (
	TriggerTypeManual              TriggerType = "manual"
	TriggerTypeCron                TriggerType = "cron"
	TriggerTypeRecovery            TriggerType = "recovery"
	TriggerTypeSingleFileRetry     TriggerType = "single_file_retry"
	TriggerTypeFolderChangeCleanup TriggerType = "folder_change_cleanup"
	TriggerTypeKBChangeArchive     TriggerType = "kb_change_archive"
)

// TaskStatus 定义任务状态
type TaskStatus string

const (
	TaskStatusEnabled  TaskStatus = "ENABLED"
	TaskStatusDisabled TaskStatus = "DISABLED"
)

// RunStatus 定义批次状态
type RunStatus string

const (
	RunStatusQueued         RunStatus = "QUEUED"
	RunStatusRunning        RunStatus = "RUNNING"
	RunStatusPauseRequested RunStatus = "PAUSE_REQUESTED"
	RunStatusPaused         RunStatus = "PAUSED"
	RunStatusStopRequested  RunStatus = "STOP_REQUESTED"
	RunStatusStopped        RunStatus = "STOPPED"
	RunStatusSuccess        RunStatus = "SUCCESS"
	RunStatusCompleted      RunStatus = "COMPLETED"
	RunStatusPartialSuccess RunStatus = "PARTIAL_SUCCESS"
	RunStatusFailed         RunStatus = "FAILED"
	RunStatusInterrupted    RunStatus = "INTERRUPTED"
	RunStatusCancelled      RunStatus = "CANCELLED"
)

// ProcessingStage 定义文件处理阶段
type ProcessingStage string

const (
	ProcessingStageInit                 ProcessingStage = "INIT"
	ProcessingStageHashing              ProcessingStage = "HASHING"
	ProcessingStageMinerUPending        ProcessingStage = "MINERU_PENDING"
	ProcessingStageMinerURunning        ProcessingStage = "MINERU_RUNNING"
	ProcessingStageMaxKBDeleting        ProcessingStage = "MAXKB_DELETING"
	ProcessingStageMaxKBDeleteCompleted ProcessingStage = "MAXKB_DELETE_COMPLETED"
	ProcessingStageMaxKBSplitting       ProcessingStage = "MAXKB_SPLITTING"
	ProcessingStageMaxKBCreating        ProcessingStage = "MAXKB_CREATING"
	ProcessingStageMaxKBProcessing      ProcessingStage = "MAXKB_PROCESSING"
	ProcessingStageCompleted            ProcessingStage = "COMPLETED"
	// Shorthand aliases used by service layer
	ProcessingStageUploading ProcessingStage = "UPLOADING"
	ProcessingStageDeleting  ProcessingStage = "DELETING"
	ProcessingStageDone      ProcessingStage = "DONE"
)

// ControlState 定义文件控制状态
type ControlState string

const (
	ControlStateActive  ControlState = "ACTIVE"
	ControlStatePaused  ControlState = "PAUSED"
	ControlStateStopped ControlState = "STOPPED"
)

// FileFinalStatus 定义文件最终状态
type FileFinalStatus string

const (
	FileFinalStatusPending           FileFinalStatus = "PENDING"
	FileFinalStatusSuccess           FileFinalStatus = "SUCCESS"
	FileFinalStatusFailed            FileFinalStatus = "FAILED"
	FileFinalStatusSkipped           FileFinalStatus = "SKIPPED"
	FileFinalStatusStopped           FileFinalStatus = "STOPPED"
	FileFinalStatusReconcileRequired FileFinalStatus = "RECONCILE_REQUIRED"
)

// FileStatus 定义 sync_files 表的状态
type FileStatus string

const (
	FileStatusPending                FileStatus = "PENDING"
	FileStatusSynced                 FileStatus = "SYNCED"
	FileStatusStaleRemoteExists      FileStatus = "STALE_REMOTE_EXISTS"
	FileStatusNeedsDelete            FileStatus = "NEEDS_DELETE"
	FileStatusDeleted                FileStatus = "DELETED"
	FileStatusLocalMissingRemoteKept FileStatus = "LOCAL_MISSING_REMOTE_KEPT"
	FileStatusReconcileRequired      FileStatus = "RECONCILE_REQUIRED"
)

// ChangeType 定义文件变更类型
type ChangeType string

const (
	ChangeTypeNew       ChangeType = "new"
	ChangeTypeUpdated   ChangeType = "updated"
	ChangeTypeUpdate    ChangeType = "updated" // alias
	ChangeTypeDeleted   ChangeType = "deleted"
	ChangeTypeRenamed   ChangeType = "renamed"
	ChangeTypeUnchanged ChangeType = "unchanged"
	ChangeTypeNoChange  ChangeType = "unchanged" // alias
)

// CheckpointVersion 定义检查点版本
type CheckpointVersion int

const (
	CheckpointV1 CheckpointVersion = 1
)

// CheckpointData 定义批次检查点数据
type CheckpointData struct {
	Version            CheckpointVersion `json:"version"`
	LastProcessedIndex int               `json:"last_processed_index"`
	CurrentFileOrdinal int               `json:"current_file_ordinal"`
}

// FileCheckpoint 定义文件级检查点
type FileCheckpoint struct {
	RelativePath      string `json:"relative_path"`
	Status            string `json:"status"`
	MinerUTaskID      string `json:"mineru_task_id,omitempty"`
	MinerUBatchID     string `json:"mineru_batch_id,omitempty"`
	MaxKBSourceFileID string `json:"maxkb_source_file_id,omitempty"`
	MaxKBDocumentID   string `json:"maxkb_document_id,omitempty"`
	RetryCount        int    `json:"retry_count"`
}

// TimeRange 定义时间范围（用于查询）
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Pagination 定义分页参数
type Pagination struct {
	Page  int
	Size  int
	Total int
}
