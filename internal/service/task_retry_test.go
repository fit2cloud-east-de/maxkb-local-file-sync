package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

func retryTaskFixture(t *testing.T) (*TaskService, repository.SyncTaskRepository, repository.RunFileRepository, *repository.ReliabilityStore, *db.DB) {
	t.Helper()
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "task-retry.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitSchema(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec(`INSERT INTO sync_folders(folder_id,name,local_path,kb_id,workspace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		"folder-retry", "Retry folder", t.TempDir(), "kb-retry", "workspace-retry", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO sync_files(file_id,folder_id,relative_path,file_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		"file-retry", "folder-retry", "failed.md", string(types.FileStatusPending), now, now); err != nil {
		t.Fatal(err)
	}
	log, err := logger.New(logger.Config{Level: logger.LevelError, LogDir: t.TempDir(), LogFileName: "test.log", Sanitize: true, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	taskRepo := repository.NewSyncTaskRepository(database)
	folderRepo := repository.NewSyncFolderRepository(database)
	fileRepo := repository.NewSyncFileRepository(database)
	runFileRepo := repository.NewRunFileRepository(database)
	reliability := repository.NewReliabilityStore(database)
	service := NewTaskService(taskRepo, folderRepo, fileRepo, runFileRepo, log)
	service.SetReliabilityStore(reliability)
	return service, taskRepo, runFileRepo, reliability, database
}

func TestRetryFailedTaskCreatesBatchWithoutLocalChanges(t *testing.T) {
	ctx := context.Background()
	service, taskRepo, runFileRepo, _, database := retryTaskFixture(t)
	now := time.Now().UTC()
	source := &repository.SyncTask{
		TaskID: "failed-run", FolderID: "folder-retry", KBId: "kb-retry", WorkspaceID: "workspace-retry",
		TriggerType: types.TriggerTypeManual, RunStatus: types.RunStatusFailed,
		ProcessingStage: types.ProcessingStageMaxKBProcessing, ControlState: types.ControlStateActive,
		CreatedAt: now, CompletedAt: &now, TotalFiles: 2, FailedCount: 1, SkippedCount: 1,
	}
	if err := taskRepo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := runFileRepo.BatchCreate(ctx, []*repository.RunFile{
		{RunFileID: "failed-file-run", TaskID: source.TaskID, FileID: "file-retry", FinalStatus: types.FileFinalStatusFailed, ProcessingStage: types.ProcessingStageMaxKBProcessing, ControlState: types.ControlStateActive, ErrorMessage: "temporary failure", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}

	retry, err := service.RetryFailedTask(ctx, source.TaskID)
	if err != nil {
		t.Fatalf("RetryFailedTask() error = %v", err)
	}
	if retry.TaskID == source.TaskID {
		t.Fatal("retry batch reused the source task id")
	}
	if retry.TriggerType != types.TriggerTypeSingleFileRetry {
		t.Fatalf("retry trigger = %q", retry.TriggerType)
	}
	if retry.TotalFiles != 1 || retry.RunStatus != types.RunStatusQueued {
		t.Fatalf("retry summary = total %d status %s", retry.TotalFiles, retry.RunStatus)
	}
	files, err := runFileRepo.ListByTask(ctx, retry.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].FileID != "file-retry" || files[0].FinalStatus != types.FileFinalStatusPending {
		t.Fatalf("retry files = %+v", files)
	}
	var queued int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue WHERE run_id=?`, retry.TaskID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("job queue rows = %d, want 1", queued)
	}
	original, err := taskRepo.GetByID(ctx, source.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if original.RunStatus != types.RunStatusFailed || original.FailedCount != 1 {
		t.Fatalf("source batch was changed: %+v", original)
	}
}

func TestRetryFailedTaskRejectsReconciliationRequired(t *testing.T) {
	ctx := context.Background()
	service, taskRepo, runFileRepo, _, _ := retryTaskFixture(t)
	now := time.Now().UTC()
	source := &repository.SyncTask{
		TaskID: "ambiguous-run", FolderID: "folder-retry", KBId: "kb-retry", WorkspaceID: "workspace-retry",
		TriggerType: types.TriggerTypeManual, RunStatus: types.RunStatusFailed,
		ProcessingStage: types.ProcessingStageMaxKBProcessing, ControlState: types.ControlStateActive,
		CreatedAt: now, TotalFiles: 1, FailedCount: 1,
	}
	if err := taskRepo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := runFileRepo.BatchCreate(ctx, []*repository.RunFile{
		{RunFileID: "ambiguous-file-run", TaskID: source.TaskID, FileID: "file-retry", FinalStatus: types.FileFinalStatusReconcileRequired, ProcessingStage: types.ProcessingStageMaxKBProcessing, ControlState: types.ControlStateActive, CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryFailedTask(ctx, source.TaskID); !errors.Is(err, ErrRetryRequiresReconciliation) {
		t.Fatalf("error = %v, want ErrRetryRequiresReconciliation", err)
	}
}

func TestRetryFailedTaskPromotesAmbiguousFailureToReconcile(t *testing.T) {
	ctx := context.Background()
	service, taskRepo, runFileRepo, reliability, database := retryTaskFixture(t)
	now := time.Now().UTC()
	source := &repository.SyncTask{
		TaskID: "ambiguous-failed-run", FolderID: "folder-retry", KBId: "kb-retry", WorkspaceID: "workspace-retry",
		TriggerType: types.TriggerTypeManual, RunStatus: types.RunStatusFailed,
		ProcessingStage: types.ProcessingStageMaxKBCreating, ControlState: types.ControlStateActive,
		CreatedAt: now, CompletedAt: &now, TotalFiles: 1, FailedCount: 1,
	}
	if err := taskRepo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := runFileRepo.BatchCreate(ctx, []*repository.RunFile{
		{RunFileID: "ambiguous-failed-file-run", TaskID: source.TaskID, FileID: "file-retry", FinalStatus: types.FileFinalStatusFailed, ProcessingStage: types.ProcessingStageMaxKBCreating, ControlState: types.ControlStateActive, ErrorMessage: "batch create outcome is unknown", CreatedAt: now, CompletedAt: &now},
	}); err != nil {
		t.Fatal(err)
	}
	attempt, err := reliability.StartOrResumeAttempt(ctx, "ambiguous-failed-file-run")
	if err != nil {
		t.Fatal(err)
	}
	attempt.Status = "FAILED"
	attempt.ErrorCode = "MAXKB_CREATE_UNKNOWN"
	attempt.ErrorMessage = "batch create outcome is unknown"
	attempt.MaxKBSourceFileID = "source-file-fake"
	attempt.MaxKBBatchTaskID = "batch-task-fake"
	attempt.CompletedAt = &now
	if err := reliability.SaveAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}

	if _, err := service.RetryFailedTask(ctx, source.TaskID); !errors.Is(err, ErrRetryRequiresReconciliation) {
		t.Fatalf("RetryFailedTask() error = %v, want ErrRetryRequiresReconciliation", err)
	}

	var runStatus, fileStatus, attemptStatus string
	if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id=?`, "ambiguous-failed-file-run").Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT file_status FROM sync_files WHERE file_id=?`, "file-retry").Scan(&fileStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM file_attempts WHERE id=?`, attempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "RECONCILE_REQUIRED" || fileStatus != "RECONCILE_REQUIRED" || attemptStatus != "RECONCILE_REQUIRED" {
		t.Fatalf("states run=%s file=%s attempt=%s, want RECONCILE_REQUIRED", runStatus, fileStatus, attemptStatus)
	}

	items, err := reliability.ListReconcileItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RunFileID != "ambiguous-failed-file-run" {
		t.Fatalf("reconciliation items = %+v, want the promoted failed file", items)
	}
	if items[0].MaxKBSourceFileID != "source-file-fake" || items[0].MaxKBBatchTaskID != "batch-task-fake" {
		t.Fatalf("reconciliation remote references = %+v", items[0])
	}

	var queued int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue WHERE task_id=?`, source.TaskID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("job queue rows = %d, want 0", queued)
	}
}
