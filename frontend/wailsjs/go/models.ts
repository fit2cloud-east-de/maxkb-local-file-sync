export namespace api {
	
	export class CreateFolderRequest {
	    name: string;
	    localPath: string;
	    kbId: string;
	    workspaceId: string;
	    knowledgeFolderId: string;
	    workspaceName: string;
	    knowledgeName: string;
	    maxkbBaseUrlSnapshot: string;
	    enableMinerU: boolean;
	    cronExpression: string;
	    cronEnabled: boolean;
	    syncDeleteLocalRemoved: boolean;
	    mineruRetryCount: number;
	    mineruRequestTimeout: number;
	    mineruTaskTimeout: number;
	    mineruPollInterval: number;
	    mineruSaveFullResult: boolean;
	    mineruResultSaveDir: string;
	    includePatterns: string;
	    excludePatterns: string;
	    mineruFileExtensions: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateFolderRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.localPath = source["localPath"];
	        this.kbId = source["kbId"];
	        this.workspaceId = source["workspaceId"];
	        this.knowledgeFolderId = source["knowledgeFolderId"];
	        this.workspaceName = source["workspaceName"];
	        this.knowledgeName = source["knowledgeName"];
	        this.maxkbBaseUrlSnapshot = source["maxkbBaseUrlSnapshot"];
	        this.enableMinerU = source["enableMinerU"];
	        this.cronExpression = source["cronExpression"];
	        this.cronEnabled = source["cronEnabled"];
	        this.syncDeleteLocalRemoved = source["syncDeleteLocalRemoved"];
	        this.mineruRetryCount = source["mineruRetryCount"];
	        this.mineruRequestTimeout = source["mineruRequestTimeout"];
	        this.mineruTaskTimeout = source["mineruTaskTimeout"];
	        this.mineruPollInterval = source["mineruPollInterval"];
	        this.mineruSaveFullResult = source["mineruSaveFullResult"];
	        this.mineruResultSaveDir = source["mineruResultSaveDir"];
	        this.includePatterns = source["includePatterns"];
	        this.excludePatterns = source["excludePatterns"];
	        this.mineruFileExtensions = source["mineruFileExtensions"];
	    }
	}
	export class CreateKnowledgeBaseDTO {
	    workspaceId: string;
	    folderId: string;
	    name: string;
	    description: string;
	    embeddingModelId: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateKnowledgeBaseDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspaceId = source["workspaceId"];
	        this.folderId = source["folderId"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.embeddingModelId = source["embeddingModelId"];
	    }
	}
	export class EmbeddingModelDTO {
	    id: string;
	    name: string;
	    provider: string;
	
	    static createFrom(source: any = {}) {
	        return new EmbeddingModelDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	    }
	}
	export class FileDTO {
	    fileId: string;
	    folderId: string;
	    relativePath: string;
	    fileStatus: string;
	    observedMd5: string;
	    lastSuccessMd5: string;
	    remoteDocId: string;
	    lastSyncedAt?: string;
	    lastCheckedAt?: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new FileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileId = source["fileId"];
	        this.folderId = source["folderId"];
	        this.relativePath = source["relativePath"];
	        this.fileStatus = source["fileStatus"];
	        this.observedMd5 = source["observedMd5"];
	        this.lastSuccessMd5 = source["lastSuccessMd5"];
	        this.remoteDocId = source["remoteDocId"];
	        this.lastSyncedAt = source["lastSyncedAt"];
	        this.lastCheckedAt = source["lastCheckedAt"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class FileStatsDTO {
	    total: number;
	    synced: number;
	    pending: number;
	    stale: number;
	    needsDelete: number;
	
	    static createFrom(source: any = {}) {
	        return new FileStatsDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.synced = source["synced"];
	        this.pending = source["pending"];
	        this.stale = source["stale"];
	        this.needsDelete = source["needsDelete"];
	    }
	}
	export class FolderDTO {
	    folderId: string;
	    name: string;
	    localPath: string;
	    kbId: string;
	    workspaceId: string;
	    knowledgeFolderId: string;
	    workspaceName: string;
	    knowledgeName: string;
	    maxkbBaseUrlSnapshot: string;
	    enableMinerU: boolean;
	    cronExpression: string;
	    cronEnabled: boolean;
	    enabled: boolean;
	    disabledAt?: string;
	    syncDeleteLocalRemoved: boolean;
	    mineruRetryCount: number;
	    mineruRequestTimeout: number;
	    mineruTaskTimeout: number;
	    mineruPollInterval: number;
	    mineruSaveFullResult: boolean;
	    mineruResultSaveDir: string;
	    includePatterns: string;
	    excludePatterns: string;
	    mineruFileExtensions: string;
	    nextExecutionAt?: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new FolderDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.folderId = source["folderId"];
	        this.name = source["name"];
	        this.localPath = source["localPath"];
	        this.kbId = source["kbId"];
	        this.workspaceId = source["workspaceId"];
	        this.knowledgeFolderId = source["knowledgeFolderId"];
	        this.workspaceName = source["workspaceName"];
	        this.knowledgeName = source["knowledgeName"];
	        this.maxkbBaseUrlSnapshot = source["maxkbBaseUrlSnapshot"];
	        this.enableMinerU = source["enableMinerU"];
	        this.cronExpression = source["cronExpression"];
	        this.cronEnabled = source["cronEnabled"];
	        this.enabled = source["enabled"];
	        this.disabledAt = source["disabledAt"];
	        this.syncDeleteLocalRemoved = source["syncDeleteLocalRemoved"];
	        this.mineruRetryCount = source["mineruRetryCount"];
	        this.mineruRequestTimeout = source["mineruRequestTimeout"];
	        this.mineruTaskTimeout = source["mineruTaskTimeout"];
	        this.mineruPollInterval = source["mineruPollInterval"];
	        this.mineruSaveFullResult = source["mineruSaveFullResult"];
	        this.mineruResultSaveDir = source["mineruResultSaveDir"];
	        this.includePatterns = source["includePatterns"];
	        this.excludePatterns = source["excludePatterns"];
	        this.mineruFileExtensions = source["mineruFileExtensions"];
	        this.nextExecutionAt = source["nextExecutionAt"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class KnowledgeBaseDTO {
	    id: string;
	    name: string;
	    description: string;
	    workspaceId: string;
	    folderId: string;
	
	    static createFrom(source: any = {}) {
	        return new KnowledgeBaseDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.workspaceId = source["workspaceId"];
	        this.folderId = source["folderId"];
	    }
	}
	export class KnowledgeFolderDTO {
	    id: string;
	    name: string;
	    children?: KnowledgeFolderDTO[];
	
	    static createFrom(source: any = {}) {
	        return new KnowledgeFolderDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.children = this.convertValues(source["children"], KnowledgeFolderDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MaxKBConfigDTO {
	    baseUrl: string;
	    apiKey: string;
	
	    static createFrom(source: any = {}) {
	        return new MaxKBConfigDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	    }
	}
	export class MinerUArtifactCleanupResultDTO {
	    status: string;
	    deletedCount: number;
	    skippedCount: number;
	    error?: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new MinerUArtifactCleanupResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.deletedCount = source["deletedCount"];
	        this.skippedCount = source["skippedCount"];
	        this.error = source["error"];
	        this.at = source["at"];
	    }
	}
	export class MinerUArtifactSettingsDTO {
	    saveFullResult?: boolean;
	    resultSaveDir: string;
	    cleanupTemporaryResults?: boolean;
	    cleanupPolicy: string;
	    cleanupAfterValue: number;
	    cleanupAfterUnit: string;
	    cleanupAfterDays?: number;
	    cleanupKeepBatches: number;
	    cleanupCron: string;
	    lastCleanupAt: string;
	    lastCleanupStatus: string;
	    lastCleanupDeletedCount: number;
	    lastCleanupError: string;
	
	    static createFrom(source: any = {}) {
	        return new MinerUArtifactSettingsDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.saveFullResult = source["saveFullResult"];
	        this.resultSaveDir = source["resultSaveDir"];
	        this.cleanupTemporaryResults = source["cleanupTemporaryResults"];
	        this.cleanupPolicy = source["cleanupPolicy"];
	        this.cleanupAfterValue = source["cleanupAfterValue"];
	        this.cleanupAfterUnit = source["cleanupAfterUnit"];
	        this.cleanupAfterDays = source["cleanupAfterDays"];
	        this.cleanupKeepBatches = source["cleanupKeepBatches"];
	        this.cleanupCron = source["cleanupCron"];
	        this.lastCleanupAt = source["lastCleanupAt"];
	        this.lastCleanupStatus = source["lastCleanupStatus"];
	        this.lastCleanupDeletedCount = source["lastCleanupDeletedCount"];
	        this.lastCleanupError = source["lastCleanupError"];
	    }
	}
	export class MinerUConfigDTO {
	    baseUrl: string;
	    apiKey: string;
	    mode: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MinerUConfigDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.mode = source["mode"];
	        this.enabled = source["enabled"];
	    }
	}
	export class PreviewMatchRequest {
	    localPath: string;
	    includePatterns: string;
	    excludePatterns: string;
	    enableMinerU: boolean;
	    mineruFileExtensions: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewMatchRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.localPath = source["localPath"];
	        this.includePatterns = source["includePatterns"];
	        this.excludePatterns = source["excludePatterns"];
	        this.enableMinerU = source["enableMinerU"];
	        this.mineruFileExtensions = source["mineruFileExtensions"];
	    }
	}
	export class PreviewMatchResult {
	    totalFiles: number;
	    matchedFiles: string[];
	    excludedFiles: string[];
	    exclusionReasons?: Record<string, string>;
	    mineruFiles: string[];
	    regularFiles: string[];
	
	    static createFrom(source: any = {}) {
	        return new PreviewMatchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalFiles = source["totalFiles"];
	        this.matchedFiles = source["matchedFiles"];
	        this.excludedFiles = source["excludedFiles"];
	        this.exclusionReasons = source["exclusionReasons"];
	        this.mineruFiles = source["mineruFiles"];
	        this.regularFiles = source["regularFiles"];
	    }
	}
	export class QueueStatsDTO {
	    queued: number;
	    running: number;
	    paused: number;
	    reconcileRequired: number;
	
	    static createFrom(source: any = {}) {
	        return new QueueStatsDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.queued = source["queued"];
	        this.running = source["running"];
	        this.paused = source["paused"];
	        this.reconcileRequired = source["reconcileRequired"];
	    }
	}
	export class ReconcileDTO {
	    runFileId: string;
	    taskId: string;
	    fileId: string;
	    folderId: string;
	    folderName: string;
	    relativePath: string;
	    processingStage: string;
	    reason: string;
	    snapshotPath: string;
	    snapshotMD5: string;
	    snapshotSize: number;
	    maxKBSourceFileID: string;
	    maxKBBatchTaskID: string;
	    maxKBDocumentID: string;
	    deletingDocumentID: string;
	    minerUTaskID: string;
	    minerUStatus: string;
	    createdAt: string;
	    completedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ReconcileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runFileId = source["runFileId"];
	        this.taskId = source["taskId"];
	        this.fileId = source["fileId"];
	        this.folderId = source["folderId"];
	        this.folderName = source["folderName"];
	        this.relativePath = source["relativePath"];
	        this.processingStage = source["processingStage"];
	        this.reason = source["reason"];
	        this.snapshotPath = source["snapshotPath"];
	        this.snapshotMD5 = source["snapshotMD5"];
	        this.snapshotSize = source["snapshotSize"];
	        this.maxKBSourceFileID = source["maxKBSourceFileID"];
	        this.maxKBBatchTaskID = source["maxKBBatchTaskID"];
	        this.maxKBDocumentID = source["maxKBDocumentID"];
	        this.deletingDocumentID = source["deletingDocumentID"];
	        this.minerUTaskID = source["minerUTaskID"];
	        this.minerUStatus = source["minerUStatus"];
	        this.createdAt = source["createdAt"];
	        this.completedAt = source["completedAt"];
	    }
	}
	export class RunFileDTO {
	    runFileId: string;
	    taskId: string;
	    fileId: string;
	    relativePath: string;
	    processingStage: string;
	    controlState: string;
	    finalStatus: string;
	    errorMessage?: string;
	    createdAt: string;
	    startedAt?: string;
	    completedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunFileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runFileId = source["runFileId"];
	        this.taskId = source["taskId"];
	        this.fileId = source["fileId"];
	        this.relativePath = source["relativePath"];
	        this.processingStage = source["processingStage"];
	        this.controlState = source["controlState"];
	        this.finalStatus = source["finalStatus"];
	        this.errorMessage = source["errorMessage"];
	        this.createdAt = source["createdAt"];
	        this.startedAt = source["startedAt"];
	        this.completedAt = source["completedAt"];
	    }
	}
	export class ScanResultDTO {
	    newFiles: string[];
	    updatedFiles: string[];
	    deletedFiles: string[];
	    renamedFiles: Record<string, string>;
	    unchangedFiles: string[];
	
	    static createFrom(source: any = {}) {
	        return new ScanResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.newFiles = source["newFiles"];
	        this.updatedFiles = source["updatedFiles"];
	        this.deletedFiles = source["deletedFiles"];
	        this.renamedFiles = source["renamedFiles"];
	        this.unchangedFiles = source["unchangedFiles"];
	    }
	}
	export class TaskDTO {
	    taskId: string;
	    folderId: string;
	    folderName: string;
	    kbId: string;
	    workspaceId: string;
	    triggerType: string;
	    runStatus: string;
	    processingStage: string;
	    controlState: string;
	    createdAt: string;
	    startedAt?: string;
	    completedAt?: string;
	    errorMessage?: string;
	    totalFiles: number;
	    successCount: number;
	    failedCount: number;
	    skippedCount: number;
	    processedFiles: number;
	    successFiles: number;
	    failedFiles: number;
	    reconcileCount: number;
	    recoveryCount: number;
	    controlReason?: string;
	    errorSummary?: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.folderId = source["folderId"];
	        this.folderName = source["folderName"];
	        this.kbId = source["kbId"];
	        this.workspaceId = source["workspaceId"];
	        this.triggerType = source["triggerType"];
	        this.runStatus = source["runStatus"];
	        this.processingStage = source["processingStage"];
	        this.controlState = source["controlState"];
	        this.createdAt = source["createdAt"];
	        this.startedAt = source["startedAt"];
	        this.completedAt = source["completedAt"];
	        this.errorMessage = source["errorMessage"];
	        this.totalFiles = source["totalFiles"];
	        this.successCount = source["successCount"];
	        this.failedCount = source["failedCount"];
	        this.skippedCount = source["skippedCount"];
	        this.processedFiles = source["processedFiles"];
	        this.successFiles = source["successFiles"];
	        this.failedFiles = source["failedFiles"];
	        this.reconcileCount = source["reconcileCount"];
	        this.recoveryCount = source["recoveryCount"];
	        this.controlReason = source["controlReason"];
	        this.errorSummary = source["errorSummary"];
	    }
	}
	export class WorkspaceDTO {
	    id: string;
	    name: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}

}

