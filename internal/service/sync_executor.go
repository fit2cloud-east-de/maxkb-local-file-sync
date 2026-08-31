package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/infra/file"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

type terminalRemoteError struct{ err error }

func (e *terminalRemoteError) Error() string { return e.err.Error() }
func (e *terminalRemoteError) Unwrap() error { return e.err }

// mineruResultError marks failures after MinerU has completed but before its
// downloaded ZIP can be handed to MaxKB (for example an empty download or a
// failure while publishing the archive to the configured retention folder).
type mineruResultError struct {
	code string
	err  error
}

func (e *mineruResultError) Error() string { return e.err.Error() }
func (e *mineruResultError) Unwrap() error { return e.err }

// SyncExecutor executes a run file as a resumable state machine. Every remote
// reference is checkpointed in file_attempts before the next side effect.
type SyncExecutor struct {
	mu            sync.RWMutex
	maxkbAdapter  adapter.MaxKBAdapter
	mineruAdapter adapter.MinerUAdapter
	fileRepo      repository.SyncFileRepository
	folderRepo    repository.SyncFolderRepository
	runFileRepo   repository.RunFileRepository
	reliability   *repository.ReliabilityStore
	snapshotSvc   *SnapshotService
	artifactStore *MinerUArtifactStore
	logger        *logger.Logger
}

func NewSyncExecutor(maxkbAdapter adapter.MaxKBAdapter, mineruAdapter adapter.MinerUAdapter, fileRepo repository.SyncFileRepository, folderRepo repository.SyncFolderRepository, runFileRepo repository.RunFileRepository, snapshotSvc *SnapshotService, logger *logger.Logger) *SyncExecutor {
	return &SyncExecutor{maxkbAdapter: maxkbAdapter, mineruAdapter: mineruAdapter, fileRepo: fileRepo, folderRepo: folderRepo, runFileRepo: runFileRepo, snapshotSvc: snapshotSvc, artifactStore: NewMinerUArtifactStore(), logger: logger}
}
func (e *SyncExecutor) SetReliabilityStore(store *repository.ReliabilityStore) {
	e.mu.Lock()
	e.reliability = store
	e.mu.Unlock()
}

// SetSystemSettingsRepository wires the system-wide MinerU artifact policy
// without changing the existing executor constructor contract.
func (e *SyncExecutor) SetSystemSettingsRepository(repo repository.SystemSettingsRepository) {
	e.mu.Lock()
	if e.artifactStore == nil {
		e.artifactStore = NewMinerUArtifactStore()
	}
	e.artifactStore.SetSystemSettingsRepository(repo)
	e.mu.Unlock()
}
func (e *SyncExecutor) SetAdapters(maxkb adapter.MaxKBAdapter, mineru adapter.MinerUAdapter) {
	e.mu.Lock()
	e.maxkbAdapter = maxkb
	e.mineruAdapter = mineru
	e.mu.Unlock()
}
func (e *SyncExecutor) adapters() (adapter.MaxKBAdapter, adapter.MinerUAdapter, *repository.ReliabilityStore) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.maxkbAdapter, e.mineruAdapter, e.reliability
}

func (e *SyncExecutor) ExecuteRunFile(ctx context.Context, runFileID string) error {
	runFile, err := e.runFileRepo.GetByID(ctx, runFileID)
	if err != nil {
		return fmt.Errorf("failed to get run file: %w", err)
	}
	if runFile.FinalStatus != types.FileFinalStatusPending {
		return nil
	}
	_, _, store := e.adapters()
	if err := checkpointRun(ctx, store, runFile.TaskID); err != nil {
		return err
	}
	if runFile.ControlState == types.ControlStatePaused {
		return fmt.Errorf("run file is paused")
	}
	if runFile.ControlState == types.ControlStateStopped {
		return fmt.Errorf("run file is stopped")
	}
	syncFile, err := e.fileRepo.GetByID(ctx, runFile.FileID)
	if err != nil {
		return fmt.Errorf("failed to get sync file: %w", err)
	}
	folder, err := e.folderRepo.GetByID(ctx, syncFile.FolderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}
	filePath := filepath.Join(folder.LocalPath, syncFile.RelativePath)
	switch syncFile.FileStatus {
	case types.FileStatusPending:
		return e.executeAddOrUpdate(ctx, runFile, syncFile, folder, filePath, true)
	case types.FileStatusStaleRemoteExists:
		return e.executeAddOrUpdate(ctx, runFile, syncFile, folder, filePath, false)
	case types.FileStatusNeedsDelete:
		return e.executeDelete(ctx, runFile, syncFile, folder)
	case types.FileStatusDeleted, types.FileStatusSynced, types.FileStatusLocalMissingRemoteKept:
		if err := e.runFileRepo.UpdateFinalStatus(ctx, runFileID, types.FileFinalStatusSkipped, "already synchronized"); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unexpected file status: %s", syncFile.FileStatus)
	}
}

func (e *SyncExecutor) executeAddOrUpdate(ctx context.Context, rf *repository.RunFile, sf *repository.SyncFile, folder *repository.SyncFolder, filePath string, isNew bool) error {
	mineruExtensions := file.ParseExtensions(folder.MinerUFileExtensions)
	if !isSupportedForSync(sf.RelativePath, folder.EnableMinerU, mineruExtensions) {
		return e.fail(ctx, rf, "UNSUPPORTED_FILE_TYPE", "MaxKB 不支持直接上传该文件格式，且未启用对应的 MinerU 转换", false)
	}

	maxkb, mineru, store := e.adapters()
	if maxkb == nil {
		return e.fail(ctx, rf, "CONFIGURATION", "MaxKB adapter is not configured", false)
	}
	attempt, err := e.loadAttempt(ctx, rf.RunFileID, store)
	if err != nil {
		return err
	}

	// Reuse a durable snapshot after a crash. A new snapshot is created only
	// before the first irreversible remote operation.
	var snapshotPath string
	if attempt.SnapshotPath != "" {
		// A durable reference is only reusable when the file still exists and
		// its bytes match the recorded hash. If remote references already exist,
		// a missing/corrupt snapshot is an audit problem, not a reason to upload
		// different bytes automatically.
		stat, statErr := os.Stat(attempt.SnapshotPath)
		actualMD5 := ""
		if statErr == nil && !stat.IsDir() {
			actualMD5, statErr = file.CalculateMD5(attempt.SnapshotPath)
		}
		if statErr == nil && (attempt.SnapshotMD5 == "" || actualMD5 == attempt.SnapshotMD5) {
			snapshotPath = attempt.SnapshotPath
			if attempt.SnapshotMD5 == "" {
				attempt.SnapshotMD5 = actualMD5
				attempt.SourceMD5Before = actualMD5
			}
			rf.SnapshotPath = snapshotPath
			rf.SnapshotMD5 = attempt.SnapshotMD5
			rf.SnapshotSize = stat.Size()
			if err := e.saveAttempt(ctx, store, attempt); err != nil {
				return err
			}
		} else if attempt.MaxKBSourceFileID != "" || attempt.MaxKBBatchTaskID != "" || attempt.MaxKBDocumentID != "" || attempt.MinerUTaskID != "" {
			if store != nil {
				if err := store.MarkReconcile(ctx, rf.RunFileID, "durable snapshot is missing or corrupt while remote references exist"); err != nil {
					return err
				}
			}
			return fmt.Errorf("durable snapshot cannot be reused safely")
		}
		// There is no remote side effect to protect. Discard the stale
		// checkpoint before making a replacement, otherwise old path/hash/size
		// values could be persisted alongside the new snapshot.
		if statErr != nil || actualMD5 != attempt.SnapshotMD5 {
			if err := e.snapshotSvc.CleanupSnapshot(ctx, attempt.SnapshotPath); err != nil && !os.IsNotExist(statErr) {
				return err
			}
			attempt.SnapshotPath = ""
			attempt.SnapshotMD5 = ""
			attempt.SnapshotSize = 0
			attempt.SnapshotModifiedAt = nil
			attempt.SourceMD5Before = ""
			rf.SnapshotPath = ""
			rf.SnapshotMD5 = ""
			rf.SnapshotSize = 0
			if err := e.saveAttempt(ctx, store, attempt); err != nil {
				return err
			}
		}
	}
	if snapshotPath == "" {
		if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageHashing); err != nil {
			return err
		}
		snapshot, snapPath, err := e.snapshotSvc.CreateSnapshot(ctx, filePath)
		if err != nil {
			return e.fail(ctx, rf, "SNAPSHOT_FAILED", err.Error(), false)
		}
		snapshotPath = snapPath
		rf.SnapshotPath = snapPath
		rf.SnapshotSize = snapshot.Size
		rf.SnapshotModifiedAt = snapshot.ModifiedAt
		rf.SnapshotMD5 = snapshot.MD5
		attempt.SnapshotPath = snapPath
		attempt.SnapshotMD5 = snapshot.MD5
		attempt.SnapshotSize = snapshot.Size
		attempt.SourceMD5Before = snapshot.MD5
		snapshotTime := time.Unix(snapshot.ModifiedAt, 0)
		attempt.SnapshotModifiedAt = &snapshotTime
		if err := e.saveAttempt(ctx, store, attempt); err != nil {
			return err
		}
		if err := e.runFileRepo.Update(ctx, rf); err != nil {
			return err
		}
	}
	// Do not defer cleanup: failed and reconcile attempts retain their input.
	cleanupOnSuccess := false
	defer func() {
		if cleanupOnSuccess {
			if err := e.snapshotSvc.CleanupSnapshot(ctx, snapshotPath); err != nil {
				// The durable success is already committed. Keep the snapshot for
				// audit/cleanup and surface the cleanup problem in logs instead of
				// pretending the remote sync failed.
				e.logger.Error("failed to cleanup successful snapshot %s: %v", snapshotPath, err)
			}
		}
	}()

	// A stale remote document is deleted in a separate, recoverable stage.
	// delete_completed_at is the durable success checkpoint. If a crash happened
	// while deleting, repeating DELETE for the same id is safe because 404 is
	// treated as already completed.
	if !isNew && sf.RemoteDocID != "" {
		docID := sf.RemoteDocID
		if attempt.DeletingDocumentID != "" {
			docID = attempt.DeletingDocumentID
		}
		if attempt.DeleteCompletedAt == nil {
			if err := e.deleteRemote(ctx, rf, attempt, folder, docID, maxkb); err != nil {
				return err
			}
			if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
				return err
			}
		} else if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBDeleteCompleted); err != nil {
			return err
		}
		// This is an update checkpoint, not the terminal delete commit. The
		// replacement upload remains pending and the old mapping is cleared in
		// the same durable transaction so a failed replacement cannot point at a
		// document that was already deleted.
		if store != nil {
			if err := store.CommitDeleteCheckpoint(ctx, rf.RunFileID, docID); err != nil {
				return err
			}
			sf.RemoteDocID = ""
			sf.FileStatus = types.FileStatusPending
		}
	}

	// The snapshot is the immutable input to every remote operation. Keep it
	// as a path and reopen it for each attempt so large files are streamed
	// without an intermediate []byte allocation.
	contentPath := snapshotPath
	contentFileName := filepath.Base(sf.RelativePath)
	contentSize, err := snapshotFileSize(contentPath)
	if err != nil {
		return e.fail(ctx, rf, "SNAPSHOT_READ_FAILED", err.Error(), false)
	}

	// MinerU is intentionally isolated behind its adapter. The source snapshot
	// is the only input to the remote pipeline; a live source file is never
	// reread after this point.
	shouldUseMinerU := shouldUseMinerU(folder.EnableMinerU, sf.RelativePath, mineruExtensions)

	if shouldUseMinerU {
		if mineru == nil {
			return e.fail(ctx, rf, "CONFIGURATION", "MinerU adapter is not configured", false)
		}
		if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMinerURunning); err != nil {
			return err
		}
		if attempt.MinerUTaskID == "" {
			if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
				return err
			}
			resp, err := mineru.SubmitTask(ctx, &adapter.SubmitTaskRequest{
				FileName: filepath.Base(sf.RelativePath), FilePath: contentPath,
				FileSize: contentSize, AttemptID: attempt.ID,
				OutputFormat: "markdown",
			})
			if err != nil {
				return e.fail(ctx, rf, "MINERU_SUBMIT_UNKNOWN", err.Error(), true)
			}
			if resp == nil || resp.TaskID == "" {
				return e.fail(ctx, rf, "MINERU_SUBMIT_UNKNOWN", "MinerU submit returned no task id", true)
			}
			attempt.MinerUTaskID = resp.TaskID
			attempt.MinerUStatus = resp.Status
			attempt.MinerURemoteRef = resp.TaskID
			if err := e.saveAttempt(ctx, store, attempt); err != nil {
				return err
			}
		}
		var resultRoot string
		contentPath, resultRoot, err = e.waitMinerU(ctx, mineru, attempt, folder, rf.TaskID, sf.RelativePath, mineruTimeout(folder), mineruPollInterval(folder))
		if resultRoot != "" {
			store := e.artifactStore
			if store == nil {
				store = NewMinerUArtifactStore()
			}
			defer func() {
				if cleanupErr := store.CleanupTemporaryResultWithPolicy(ctx, folder, resultRoot); cleanupErr != nil && e.logger != nil {
					e.logger.Error("failed to cleanup MinerU temporary result: %v", cleanupErr)
				}
			}()
		}
		if err != nil {
			var terminal *terminalRemoteError
			if errors.As(err, &terminal) {
				return e.fail(ctx, rf, "MINERU_FAILED", terminal.Error(), false)
			}
			var resultErr *mineruResultError
			if errors.As(err, &resultErr) {
				return e.fail(ctx, rf, resultErr.code, resultErr.Error(), false)
			}
			// A protocol/status parse failure is a terminal, diagnosable MinerU
			// error. Do not label it as an interruption: that code is reserved for
			// cancellation/context interruption during an otherwise valid wait.
			var mineruErr *adapter.MinerUError
			if errors.As(err, &mineruErr) && mineruErr.Class == adapter.RetryClassProtocol {
				return e.fail(ctx, rf, "MINERU_STATUS_UNSUPPORTED", err.Error(), false)
			}
			return fmt.Errorf("MINERU_WAIT_INTERRUPTED: %w", err)
		}
		contentSize, err = snapshotFileSize(contentPath)
		if err != nil {
			return e.fail(ctx, rf, "MINERU_RESULT_READ_FAILED", err.Error(), false)
		}
		// MinerU results are ZIP archives. Preserve the .zip filename when the
		// archive is streamed into MaxKB so MaxKB selects its native ZIP parser.
		contentFileName = filepath.Base(contentPath)
	}

	if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBSplitting); err != nil {
		return err
	}

	// MaxKB's split and batch_create responses are the synchronous upload
	// acknowledgements. Once batch_create returns a document ID, the document
	// has been accepted successfully. MaxKB may continue embedding/indexing it
	// asynchronously; that server-side status is deliberately not a gate for
	// this local sync run.
	var split *adapter.SmartSplitResult
	if attempt.MaxKBSourceFileID == "" {
		if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
			return err
		}
		split, err = e.smartSplitFromPath(ctx, maxkb, folder.WorkspaceID, folder.KBId, contentPath, contentFileName, contentSize)
		if err != nil {
			return e.fail(ctx, rf, smartSplitFailureCode(err), err.Error(), false)
		}
		if split == nil || split.SourceFileID == "" {
			return e.fail(ctx, rf, "MAXKB_SPLIT_INCOMPATIBLE", "MaxKB split returned no source_file_id", false)
		}
		attempt.MaxKBSourceFileID = split.SourceFileID
		if err := e.saveAttempt(ctx, store, attempt); err != nil {
			return err
		}
	} else if attempt.MaxKBDocumentID == "" {
		// A resumed attempt may have the source id but not the paragraphs. The
		// adapter contract allows split to be repeated because it has no local
		// document mapping side effect; repeat it from the immutable content.
		split, err = e.smartSplitFromPath(ctx, maxkb, folder.WorkspaceID, folder.KBId, contentPath, contentFileName, contentSize)
		if err != nil {
			return e.fail(ctx, rf, smartSplitFailureCode(err), err.Error(), false)
		}
	}

	if attempt.MaxKBDocumentID == "" {
		if split == nil || len(split.Paragraphs) == 0 {
			return e.fail(ctx, rf, "MAXKB_SPLIT_INCOMPATIBLE", "MaxKB split returned no paragraphs", false)
		}
		if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBCreating); err != nil {
			return err
		}
		if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
			return err
		}
		name := split.Name
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(sf.RelativePath)
		}
		created, err := maxkb.CreateDocuments(ctx, &adapter.CreateDocumentsRequest{
			WorkspaceID: folder.WorkspaceID, KnowledgeID: folder.KBId,
			Documents: []adapter.DocumentToCreate{{Name: name, Paragraphs: split.Paragraphs, SourceFileID: attempt.MaxKBSourceFileID}},
		})
		if err != nil {
			return e.fail(ctx, rf, "MAXKB_CREATE_UNKNOWN", err.Error(), true)
		}
		if created == nil || len(created.DocumentIDs) != 1 || strings.TrimSpace(created.DocumentIDs[0]) == "" {
			return e.fail(ctx, rf, "MAXKB_CREATE_UNKNOWN", "MaxKB batch_create did not return exactly one document id", true)
		}
		attempt.MaxKBDocumentID = created.DocumentIDs[0]
		if err := e.saveAttempt(ctx, store, attempt); err != nil {
			return err
		}
	}
	current, currentErr := file.CreateSnapshot(filePath)
	if currentErr != nil || current.MD5 != rf.SnapshotMD5 {
		afterMD5 := ""
		if currentErr == nil {
			afterMD5 = current.MD5
		}
		if store != nil {
			if err := store.CommitSourceChanged(ctx, rf.RunFileID, "source file changed during processing; remote outcome requires reconciliation", afterMD5); err != nil {
				return err
			}
		}
		return fmt.Errorf("SOURCE_CHANGED: source file changed during processing; remote document retained")
	}
	if store != nil {
		if err := store.CommitSyncSuccess(ctx, rf.RunFileID, attempt.MaxKBDocumentID, rf.SnapshotMD5, current.MD5); err != nil {
			return err
		}
	} else {
		if err := e.fileRepo.UpdateRemoteDocID(ctx, sf.FileID, attempt.MaxKBDocumentID); err != nil {
			return err
		}
		if err := e.fileRepo.UpdateMD5(ctx, sf.FileID, current.MD5, current.MD5); err != nil {
			return err
		}
		if err := e.fileRepo.UpdateStatus(ctx, sf.FileID, types.FileStatusSynced); err != nil {
			return err
		}
		if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageCompleted); err != nil {
			return err
		}
		if err := e.runFileRepo.UpdateFinalStatus(ctx, rf.RunFileID, types.FileFinalStatusSuccess, ""); err != nil {
			return err
		}
	}
	cleanupOnSuccess = true
	return nil
}

// fileSnapshotFromRun reconstructs the immutable source snapshot used by
// ValidateSnapshot without consulting mutable sync_files state.
func fileSnapshotFromRun(rf *repository.RunFile, _ *repository.SyncFile, path string) file.FileSnapshot {
	return file.FileSnapshot{Path: path, Size: rf.SnapshotSize, ModifiedAt: rf.SnapshotModifiedAt, MD5: rf.SnapshotMD5}
}

func (e *SyncExecutor) executeDelete(ctx context.Context, rf *repository.RunFile, sf *repository.SyncFile, folder *repository.SyncFolder) error {
	maxkb, _, store := e.adapters()
	if maxkb == nil {
		return e.fail(ctx, rf, "CONFIGURATION", "MaxKB adapter is not configured", false)
	}
	a, err := e.loadAttempt(ctx, rf.RunFileID, store)
	if err != nil {
		return err
	}
	if sf.RemoteDocID == "" {
		if store != nil {
			return store.CommitDeleteSuccess(ctx, rf.RunFileID, "")
		}
		if err := e.fileRepo.UpdateStatus(ctx, sf.FileID, types.FileStatusDeleted); err != nil {
			return err
		}
		return e.runFileRepo.UpdateFinalStatus(ctx, rf.RunFileID, types.FileFinalStatusSuccess, "")
	}
	if a.DeleteCompletedAt == nil {
		docID := sf.RemoteDocID
		if a.DeletingDocumentID != "" {
			docID = a.DeletingDocumentID
		}
		if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
			return err
		}
		if err := e.deleteRemote(ctx, rf, a, folder, docID, maxkb); err != nil {
			return err
		}
		if err := checkpointRun(ctx, store, rf.TaskID); err != nil {
			return err
		}
	} else if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBDeleteCompleted); err != nil {
		return err
	}
	if store != nil {
		return store.CommitDeleteSuccess(ctx, rf.RunFileID, sf.RemoteDocID)
	}
	if err := e.fileRepo.UpdateStatus(ctx, sf.FileID, types.FileStatusDeleted); err != nil {
		return err
	}
	if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBDeleteCompleted); err != nil {
		return err
	}
	return e.runFileRepo.UpdateFinalStatus(ctx, rf.RunFileID, types.FileFinalStatusSuccess, "")
}

func (e *SyncExecutor) deleteRemote(ctx context.Context, rf *repository.RunFile, a *repository.FileAttempt, folder *repository.SyncFolder, docID string, maxkb adapter.MaxKBAdapter) error {
	if err := e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBDeleting); err != nil {
		return err
	}
	a.DeletingDocumentID = docID
	t := time.Now().UTC()
	if a.DeleteStartedAt == nil {
		a.DeleteStartedAt = &t
	}
	a.DeleteRetryCount++
	if err := e.adaptersSave(ctx, a); err != nil {
		return err
	}
	err := maxkb.DeleteDocument(ctx, &adapter.DeleteDocumentRequest{WorkspaceID: folder.WorkspaceID, KBId: folder.KBId, DocumentID: docID})
	if err != nil {
		var mk *adapter.MaxKBError
		if errors.As(err, &mk) && mk != nil && mk.StatusCode == 404 {
			err = nil
		}
	}
	if err != nil {
		// The target id is durable, but without a document lookup contract we
		// cannot prove whether a transport error happened before or after delete.
		// Keep the id/snapshot and require an explicit operator decision.
		return e.fail(ctx, rf, "MAXKB_DELETE_UNKNOWN", err.Error(), true)
	}
	t = time.Now().UTC()
	a.DeleteCompletedAt = &t
	if err := e.adaptersSave(ctx, a); err != nil {
		return err
	}
	return e.runFileRepo.UpdateStage(ctx, rf.RunFileID, types.ProcessingStageMaxKBDeleteCompleted)
}

func (e *SyncExecutor) waitMinerU(ctx context.Context, m adapter.MinerUAdapter, a *repository.FileAttempt, folder *repository.SyncFolder, batchID, sourceName string, timeout, pollInterval time.Duration) (string, string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	resultRoot, err := os.MkdirTemp("", mineruTempResultPrefix)
	if err != nil {
		return "", "", fmt.Errorf("create MinerU result directory: %w", err)
	}
	// MaxKB supports ZIP ingestion directly. Keep the MinerU response as an
	// opaque archive: do not extract it, inspect Markdown candidates, upload
	// embedded images, or rewrite references locally.
	resultPath := filepath.Join(resultRoot, mineruResultArchiveName(sourceName))
	keepRoot := false
	defer func() {
		if !keepRoot {
			store := e.artifactStore
			if store == nil {
				store = NewMinerUArtifactStore()
			}
			_ = store.CleanupTemporaryResultWithPolicy(ctx, folder, resultRoot)
		}
	}()

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("MinerU task timed out: %s", a.MinerUTaskID)
		}
		st, err := m.QueryTaskStatus(ctx, a.MinerUTaskID)
		if err != nil {
			return "", "", err
		}
		if st == nil {
			return "", "", fmt.Errorf("MinerU returned an empty task status: %s", a.MinerUTaskID)
		}
		a.MinerUStatus = st.Status
		if err := e.adaptersSave(ctx, a); err != nil {
			return "", "", err
		}
		switch normalizeRemoteStatus(st.Status) {
		case "completed", "success", "done":
			output, err := os.OpenFile(resultPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				return "", "", fmt.Errorf("create MinerU result ZIP: %w", err)
			}
			if streaming, ok := m.(adapter.DurableMinerUAdapter); ok {
				err = streaming.DownloadResultToAt(ctx, a.MinerUTaskID, st.StatusURL, st.ResultURL, output)
			} else if streaming, ok := m.(adapter.StreamingMinerUAdapter); ok {
				err = streaming.DownloadResultTo(ctx, a.MinerUTaskID, output)
			} else {
				var result []byte
				result, err = m.DownloadResult(ctx, a.MinerUTaskID)
				if err == nil {
					_, err = output.Write(result)
				}
			}
			closeErr := output.Close()
			if err != nil {
				return "", "", err
			}
			if closeErr != nil {
				return "", "", closeErr
			}
			info, err := os.Stat(resultPath)
			if err != nil {
				return "", "", &mineruResultError{code: "MINERU_RESULT_READ_FAILED", err: err}
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return "", "", &mineruResultError{code: "MINERU_RESULT_INVALID", err: errors.New("MinerU returned an empty result ZIP")}
			}
			store := e.artifactStore
			if store == nil {
				store = NewMinerUArtifactStore()
			}
			published, err := store.Persist(ctx, folder, batchID, sourceName, resultRoot, resultPath)
			if err != nil {
				return "", "", &mineruResultError{code: "MINERU_RESULT_SAVE_FAILED", err: err}
			}
			keepRoot = true
			return published, resultRoot, nil
		case "waiting-file", "uploading", "pending", "queued", "processing", "running", "converting":
			// Continue polling documented non-terminal states only.
		case "failed", "error", "failure", "cancelled", "canceled":
			return "", "", &terminalRemoteError{err: fmt.Errorf("MinerU task failed: %s", st.ErrorMessage)}
		default:
			return "", "", &terminalRemoteError{err: fmt.Errorf("MinerU returned unknown task status: %s", st.Status)}
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func mineruResultArchiveName(sourceName string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(sourceName), "\\", "/")
	base := filepath.Base(normalized)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stem = safePathComponent(stem, "mineru-result")
	return stem + ".zip"
}

func (e *SyncExecutor) waitBatch(ctx context.Context, m adapter.MaxKBAdapter, folder *repository.SyncFolder, a *repository.FileAttempt, runFileID string, timeout, pollInterval time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("MaxKB batch task timed out: %s", a.MaxKBBatchTaskID)
		}
		st, err := m.QueryBatchStatus(ctx, &adapter.QueryBatchStatusRequest{WorkspaceID: folder.WorkspaceID, KBId: folder.KBId, TaskID: a.MaxKBBatchTaskID})
		if err != nil {
			return err
		}
		if st == nil {
			return fmt.Errorf("MaxKB returned an empty batch status: %s", a.MaxKBBatchTaskID)
		}
		if err := checkpointRun(ctx, e.reliability, runFileID); err != nil {
			return err
		}
		switch normalizeRemoteStatus(st.Status) {
		case "completed", "success", "done":
			return nil
		case "failed", "error", "failure":
			return &terminalRemoteError{err: fmt.Errorf("MaxKB batch failed: %s", st.ErrorMessage)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func smartSplitFailureCode(err error) string {
	var maxErr *adapter.MaxKBError
	if errors.As(err, &maxErr) && maxErr != nil && maxErr.Type == adapter.MaxKBErrorIncompatible {
		return "MAXKB_SPLIT_INCOMPATIBLE"
	}
	return "MAXKB_SPLIT_FAILED"
}

func normalizeRemoteStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func mineruTimeout(folder *repository.SyncFolder) time.Duration {
	if folder != nil && folder.MinerUTaskTimeout > 0 {
		return time.Duration(folder.MinerUTaskTimeout) * time.Millisecond
	}
	return 60 * time.Minute
}

func mineruPollInterval(folder *repository.SyncFolder) time.Duration {
	if folder != nil && folder.MinerUPollInterval > 0 {
		return time.Duration(folder.MinerUPollInterval) * time.Millisecond
	}
	return 2 * time.Second
}

func (e *SyncExecutor) saveAttempt(ctx context.Context, store *repository.ReliabilityStore, a *repository.FileAttempt) error {
	if store == nil {
		return nil
	}
	return store.SaveAttempt(ctx, a)
}

func (e *SyncExecutor) loadAttempt(ctx context.Context, id string, store *repository.ReliabilityStore) (*repository.FileAttempt, error) {
	if store == nil {
		return &repository.FileAttempt{RunFileID: id, Status: "RUNNING", StartedAt: time.Now().UTC()}, nil
	}
	a, err := store.StartOrResumeAttempt(ctx, id)
	if err != nil {
		return nil, err
	}
	return a, nil
}
func (e *SyncExecutor) adaptersSave(ctx context.Context, a *repository.FileAttempt) error {
	_, _, s := e.adapters()
	if s == nil {
		return nil
	}
	return s.SaveAttempt(ctx, a)
}
func (e *SyncExecutor) fail(ctx context.Context, rf *repository.RunFile, code, msg string, reconcile bool) error {
	_, _, s := e.adapters()
	rf.ErrorMessage = msg
	if reconcile && s != nil {
		if err := s.MarkReconcile(ctx, rf.RunFileID, msg); err != nil {
			return err
		}
	} else if s != nil {
		if err := s.CommitAttemptFailure(ctx, rf.RunFileID, code, msg); err != nil {
			return err
		}
	} else {
		if err := e.runFileRepo.UpdateFinalStatus(ctx, rf.RunFileID, types.FileFinalStatusFailed, msg); err != nil {
			return err
		}
	}
	return fmt.Errorf("%s: %s", code, msg)
}

func snapshotFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("snapshot is not a regular file")
	}
	return info.Size(), nil
}

func (e *SyncExecutor) smartSplitFromPath(ctx context.Context, maxkb adapter.MaxKBAdapter, workspaceID, knowledgeID, contentPath, fileName string, fileSize int64) (*adapter.SmartSplitResult, error) {
	f, err := os.Open(contentPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return maxkb.SmartSplit(ctx, &adapter.SmartSplitRequest{
		WorkspaceID: workspaceID, KnowledgeID: knowledgeID,
		File: f, FileName: fileName, FileSize: fileSize,
	})
}
