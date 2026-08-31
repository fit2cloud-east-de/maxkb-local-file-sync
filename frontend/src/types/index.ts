export interface CreateKnowledgeBaseRequest {
  workspaceId: string
  folderId: string
  name: string
  description: string
  embeddingModelId: string
}

export interface CreateFolderRequest {
  name: string
  localPath: string
  kbId: string
  workspaceId: string
  knowledgeFolderId: string
  workspaceName: string
  knowledgeName: string
  maxkbBaseUrlSnapshot: string
  enableMinerU: boolean
  cronExpression: string
  cronEnabled: boolean
  syncDeleteLocalRemoved: boolean

  // MinerU 高级配置
  mineruRetryCount: number
  mineruRequestTimeout: number
  mineruTaskTimeout: number
  mineruPollInterval: number
  mineruSaveFullResult: boolean
  mineruResultSaveDir: string

  // 文件筛选
  includePatterns: string
  excludePatterns: string
  mineruFileExtensions: string
}

export interface FolderDTO {
  folderId: string
  name: string
  localPath: string
  kbId: string
  workspaceId: string
  knowledgeFolderId: string
  workspaceName: string
  knowledgeName: string
  maxkbBaseUrlSnapshot: string
  enableMinerU: boolean
  cronExpression: string
  cronEnabled: boolean
  enabled: boolean
  disabledAt?: string
  syncDeleteLocalRemoved: boolean

  // MinerU 高级配置
  mineruRetryCount: number
  mineruRequestTimeout: number
  mineruTaskTimeout: number
  mineruPollInterval: number
  mineruSaveFullResult: boolean
  mineruResultSaveDir: string

  // 文件筛选
  includePatterns: string
  excludePatterns: string
  mineruFileExtensions: string

  // 下次执行时间
  nextExecutionAt?: string

  createdAt: string
  updatedAt: string
}

export interface FileDTO {
  fileId: string
  folderId: string
  relativePath: string
  fileStatus: string
  observedMd5: string
  lastSuccessMd5: string
  remoteDocId: string
  lastSyncedAt?: string
  lastCheckedAt?: string
  createdAt: string
  updatedAt: string
}

export interface FileStatsDTO {
  total: number
  synced: number
  pending: number
  stale: number
  failed: number
  needsDelete: number
}

export interface TaskDTO {
  taskId: string
  folderId: string
  folderName: string
  kbId: string
  workspaceId: string
  runStatus: string
  processingStage: string
  controlState: string
  triggerType: string
  totalFiles: number
  successCount: number
  failedCount: number
  skippedCount: number
  processedFiles: number
  createdAt: string
  startedAt?: string
  completedAt?: string
  errorMessage?: string
  reconcileCount?: number
  recoveryCount?: number
  controlReason?: string
  errorSummary?: string
}

export interface SyncTaskGroupDTO {
  groupKey: string
  folderId: string
  folderName: string
  runs: TaskDTO[]
  latest: TaskDTO
  successRuns: number
  failedRuns: number
  activeRuns: number
}

export interface RunFileDTO {
  runFileId: string
  taskId: string
  fileId: string
  relativePath: string
  processingStage: string
  controlState: string
  finalStatus: string
  errorMessage?: string
  createdAt: string
  startedAt?: string
  completedAt?: string
}

export interface ScanResultDTO {
  newFiles: string[]
  updatedFiles: string[]
  deletedFiles: string[]
  renamedFiles: Record<string, string>
  unchangedFiles: string[]
}

export interface PreviewMatchRequest {
  localPath: string
  includePatterns: string
  excludePatterns: string
  enableMinerU: boolean
  mineruFileExtensions: string
}

export interface PreviewMatchResult {
  totalFiles: number
  matchedFiles: string[]
  excludedFiles: string[]
  exclusionReasons?: Record<string, string>
  mineruFiles: string[]
  regularFiles: string[]
}

export interface MaxKBConfigDTO { baseUrl: string; apiKey: string }
export interface MinerUConfigDTO { enabled: boolean; baseUrl: string; apiKey: string; mode: string }

export type MinerUCleanupPolicy = 'immediate' | 'never' | 'after_duration' | 'after_days' | 'keep_batches'

/** System-wide, non-secret MinerU artifact settings exposed by app.go. */
export interface MinerUArtifactConfigDTO {
  /** @deprecated ZIP retention is always enabled; use cleanupPolicy for lifecycle. */
  saveFullResult?: boolean
  resultSaveDir: string
  /** @deprecated Temporary ZIPs are always cleaned after the safe checkpoint. */
  cleanupTemporaryResults?: boolean
  // Optional on the form type for compatibility with older settings views;
  // the config store normalizes all four fields before reading or writing.
  cleanupPolicy?: MinerUCleanupPolicy
  cleanupAfterValue?: number
  cleanupAfterUnit?: 'hour' | 'day' | string
  /** @deprecated Use cleanupAfterValue + cleanupAfterUnit. */
  cleanupAfterDays?: number
  cleanupKeepBatches?: number
  cleanupCron?: string
  /** Read-only result of the most recent cleanup attempt, when available. */
  cleanupResult?: MinerUArtifactCleanupResultDTO
}

/** Canonical, user-safe summary of one MinerU artifact cleanup attempt. */
export interface MinerUArtifactCleanupResultDTO {
  status: string
  deletedCount: number
  skippedCount: number
  error: string
  at: string

  /** @deprecated Use deletedCount. Kept for the current settings view binding. */
  deletedFiles?: number
  /** @deprecated The backend no longer reports byte totals as the canonical field. */
  deletedBytes?: number
}

/**
 * Current and planned Wails methods for system-level artifact settings.
 * Cleanup remains optional so an older app.go binding does not block settings.
 */
export interface MinerUArtifactBindings {
  GetMinerUArtifactSettings?: () => Promise<MinerUArtifactConfigDTO>
  ConfigureMinerUArtifactSettings?: (config: MinerUArtifactConfigDTO) => Promise<void>
  CleanupMinerUArtifacts?: () => Promise<MinerUArtifactCleanupResultDTO | void>
}
export interface WorkspaceDTO { id: string; name: string; description: string }
export interface KnowledgeFolderDTO { id: string; name: string; children?: KnowledgeFolderDTO[] }
export interface KnowledgeBaseDTO { id: string; name: string; description: string; workspaceId: string; folderId: string }
export interface EmbeddingModelDTO { id: string; name: string; provider: string }
export interface CreateKnowledgeBaseDTO { workspaceId: string; folderId: string; name: string; description: string; embeddingModelId: string }

export interface QueueStatsDTO { queued: number; running: number; paused: number; reconcileRequired: number }
export interface ReconcileDTO {
  runFileID?: string; runFileId?: string
  taskID?: string; taskId?: string
  fileID?: string; fileId?: string
  folderID?: string; folderId?: string
  folderName: string; relativePath: string
  processingStage: string; reason: string
  snapshotPath: string; snapshotMD5: string; snapshotSize: number
  maxKBSourceFileID: string; maxKBBatchTaskID: string; maxKBDocumentID: string
  deletingDocumentID: string; minerUTaskID: string; minerUStatus: string
  createdAt: string; completedAt: string
}
