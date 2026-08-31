package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

func reliabilityFixture(t *testing.T) (*db.DB, *ReliabilityStore, *SyncTask, []*RunFile) {
	t.Helper()
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "reliability.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitSchema(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = database.Exec(`INSERT INTO sync_folders(folder_id,name,local_path,kb_id,workspace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "folder-1", "Folder", t.TempDir(), "kb-1", "ws-1", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO sync_files(file_id,folder_id,relative_path,file_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "file-1", "folder-1", "a.md", "PENDING", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	task := &SyncTask{TaskID: "run-1", FolderID: "folder-1", KBId: "kb-1", WorkspaceID: "ws-1", TriggerType: types.TriggerTypeManual, CreatedAt: now}
	files := []*RunFile{{RunFileID: "rf-1", TaskID: "run-1", FileID: "file-1", CreatedAt: now}}
	return database, NewReliabilityStore(database), task, files
}

func TestReliabilityCreateClaimPauseResumeStop(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRunPlan(ctx, &SyncTask{TaskID: "run-2", FolderID: "folder-1", KBId: "kb-1", WorkspaceID: "ws-1", TriggerType: types.TriggerTypeManual, CreatedAt: time.Now().UTC()}, []*RunFile{}); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("expected active run, got %v", err)
	}
	run, err := store.ClaimNext(ctx, "test")
	if err != nil || run != "run-1" {
		t.Fatalf("claim = %q, %v", run, err)
	}
	if err := store.Pause(ctx, "run-1", "operator"); err != nil {
		t.Fatal(err)
	}
	var status, control, lockStatus string
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSE_REQUESTED" {
		t.Fatalf("status=%s", status)
	}
	if err := store.FinalizePause(ctx, "run-1", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Fatalf("finalized status=%s", status)
	}
	if err := database.QueryRow(`SELECT control_state FROM run_files WHERE run_file_id='rf-1'`).Scan(&control); err != nil {
		t.Fatal(err)
	}
	if control != "PAUSED" {
		t.Fatalf("file control=%s", control)
	}
	if err := database.QueryRow(`SELECT run_status FROM active_task_locks WHERE run_id='run-1'`).Scan(&lockStatus); err != nil {
		t.Fatal(err)
	}
	if lockStatus != "PAUSED" {
		t.Fatalf("lock status=%s", lockStatus)
	}
	if err := store.Resume(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "QUEUED" {
		t.Fatalf("resumed status=%s", status)
	}
	if _, err := store.ClaimNext(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if err := store.Stop(ctx, "run-1", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "STOP_REQUESTED" {
		t.Fatalf("stop request status=%s", status)
	}
	if err := store.FinalizeStop(ctx, "run-1", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "STOPPED" {
		t.Fatalf("stopped status=%s", status)
	}
}

func TestReliabilityRecoveryIdempotent(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNext(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverInterrupted(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue WHERE run_id='run-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("queue count=%d", count)
	}
	if recovered, err = store.RecoverInterrupted(ctx); err != nil || recovered != 0 {
		t.Fatalf("second recovery=%d err=%v", recovered, err)
	}
	var recoveryCount int
	if err := database.QueryRow(`SELECT recovery_count FROM sync_runs WHERE id='run-1'`).Scan(&recoveryCount); err != nil {
		t.Fatal(err)
	}
	if recoveryCount != 1 {
		t.Fatalf("recovery count=%d", recoveryCount)
	}
	var history int
	if err := database.QueryRow(`SELECT COUNT(*) FROM operation_history WHERE task_id='run-1' AND operation_type='RECOVERY_INTERRUPTED'`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 1 {
		t.Fatalf("history=%d", history)
	}
}

func TestReliabilityStartAttemptAndReconcile(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	a, err := store.StartOrResumeAttempt(ctx, "rf-1")
	if err != nil {
		t.Fatal(err)
	}
	a.SnapshotPath = "/tmp/snapshot"
	a.SnapshotMD5 = "0123456789abcdef0123456789abcdef"
	a.MaxKBBatchTaskID = "batch-1"
	if err := store.SaveAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReconcile(ctx, "rf-1", "batch create outcome is unknown"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReconcile(ctx, "rf-1", "again"); err == nil {
		t.Fatal("expected duplicate reconcile to fail")
	}
	var fs, final, attempt string
	if err := database.QueryRow(`SELECT file_status FROM sync_files WHERE file_id='file-1'`).Scan(&fs); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id='rf-1'`).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM file_attempts WHERE id=?`, a.ID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if fs != "RECONCILE_REQUIRED" || final != "RECONCILE_REQUIRED" || attempt != "RECONCILE_REQUIRED" {
		t.Fatalf("states %s %s %s", fs, final, attempt)
	}
	if _, err := store.ResolveReconcile(ctx, "rf-1", "REMOTE_ABSENT_RETRY", ""); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id='run-1'`).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if final != "QUEUED" {
		t.Fatalf("retry status=%s", final)
	}
}

func TestResolveReconcileMarkFailedKeepsStoppedRunTerminal(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReconcile(ctx, files[0].RunFileID, "unknown remote result"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sync_runs SET status='STOPPED',completed_at=? WHERE id=?`, nowText(), task.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sync_tasks SET run_status='STOPPED',completed_at=? WHERE task_id=?`, nowText(), task.TaskID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ResolveReconcile(ctx, files[0].RunFileID, "MARK_FAILED", ""); err != nil {
		t.Fatalf("mark failed reconciliation: %v", err)
	}

	var runStatus string
	var failedCount int
	if err := database.QueryRow(`SELECT status,failed_count FROM sync_runs WHERE id=?`, task.TaskID).Scan(&runStatus, &failedCount); err != nil {
		t.Fatal(err)
	}
	if runStatus != "STOPPED" || failedCount != 1 {
		t.Fatalf("durable run status=%s failed_count=%d", runStatus, failedCount)
	}
	var publicStatus string
	if err := database.QueryRow(`SELECT run_status FROM sync_tasks WHERE task_id=?`, task.TaskID).Scan(&publicStatus); err != nil {
		t.Fatal(err)
	}
	if publicStatus != "STOPPED" {
		t.Fatalf("public run status=%s", publicStatus)
	}
	var finalStatus, attemptStatus, fileStatus string
	if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&finalStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM file_attempts WHERE run_file_id=?`, files[0].RunFileID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT file_status FROM sync_files WHERE file_id=?`, files[0].FileID).Scan(&fileStatus); err != nil {
		t.Fatal(err)
	}
	if finalStatus != "FAILED" || attemptStatus != "FAILED" || fileStatus != "RECONCILE_REQUIRED" {
		t.Fatalf("file states final=%s attempt=%s sync_file=%s", finalStatus, attemptStatus, fileStatus)
	}
}

func TestClaimRebuildsMissingActiveLock(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM active_task_locks WHERE run_id=?`, task.TaskID); err != nil {
		t.Fatal(err)
	}
	runID, err := store.ClaimNext(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if runID != task.TaskID {
		t.Fatalf("claimed %q", runID)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id=?`, task.TaskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "RUNNING" {
		t.Fatalf("run status=%s", status)
	}
	if err := database.QueryRow(`SELECT run_status FROM active_task_locks WHERE run_id=?`, task.TaskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "RUNNING" {
		t.Fatalf("lock status=%s", status)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue WHERE run_id=?`, task.TaskID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("queue rows=%d", count)
	}
}

func TestQueuedPauseAndPausedStopAreTerminalAtSafeBoundary(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, task.TaskID, "operator"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id=?`, task.TaskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Fatalf("status=%s", status)
	}
	if err := store.Stop(ctx, task.TaskID, "operator"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id=?`, task.TaskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "STOPPED" {
		t.Fatalf("status=%s", status)
	}
	if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "STOPPED" {
		t.Fatalf("file status=%s", status)
	}
}

func TestRunFileRepositoryListPendingReturnsPendingOnly(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	repo := NewRunFileRepository(database)
	pending, err := repo.ListPending(ctx, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RunFileID != files[0].RunFileID {
		t.Fatalf("pending=%+v", pending)
	}
	if err := repo.UpdateFinalStatus(ctx, files[0].RunFileID, types.FileFinalStatusSuccess, "done"); err != nil {
		t.Fatal(err)
	}
	pending, err = repo.ListPending(ctx, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending files, got %d", len(pending))
	}
}

func TestRecoveryRepairsControlRequestsAndOrphans(t *testing.T) {
	ctx := context.Background()

	t.Run("running is requeued", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		n, err := store.RecoverInterrupted(ctx)
		if err != nil || n != 1 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		assertRunState(t, database, task.TaskID, "QUEUED", 1, 1, 1)
	})

	t.Run("pause requested becomes paused", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		if err := store.Pause(ctx, task.TaskID, "operator"); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 0 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		assertRunState(t, database, task.TaskID, "PAUSED", 1, 0, 1)
		var control string
		if err := database.QueryRow(`SELECT control_state FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&control); err != nil {
			t.Fatal(err)
		}
		if control != "PAUSED" {
			t.Fatalf("control=%s", control)
		}
	})

	t.Run("stop requested becomes stopped", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		if err := store.Stop(ctx, task.TaskID, "operator"); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 0 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		assertRunState(t, database, task.TaskID, "STOPPED", 1, 0, 0)
		var final, control string
		if err := database.QueryRow(`SELECT final_status,control_state FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&final, &control); err != nil {
			t.Fatal(err)
		}
		if final != "STOPPED" || control != "STOPPED" {
			t.Fatalf("file state=%s/%s", final, control)
		}
	})

	t.Run("interrupted without lock is recoverable", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE sync_runs SET status='INTERRUPTED' WHERE id=?`, task.TaskID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`DELETE FROM active_task_locks WHERE run_id=?`, task.TaskID); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 1 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		assertRunState(t, database, task.TaskID, "QUEUED", 0, 1, 1)
	})

	t.Run("queued missing queue and lock is repaired", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`DELETE FROM job_queue WHERE run_id=?`, task.TaskID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`DELETE FROM active_task_locks WHERE run_id=?`, task.TaskID); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 0 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		assertRunState(t, database, task.TaskID, "QUEUED", 0, 1, 1)
	})
}

func assertRunState(t *testing.T, database *db.DB, runID, wantStatus string, wantHistory, wantQueue, wantLock int) {
	t.Helper()
	var status string
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus {
		t.Fatalf("status=%s want=%s", status, wantStatus)
	}
	var queue, locks, history int
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue WHERE run_id=?`, runID).Scan(&queue); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM active_task_locks WHERE run_id=?`, runID).Scan(&locks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM operation_history WHERE task_id=?`, runID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if queue != wantQueue || locks != wantLock || history != wantHistory {
		t.Fatalf("queue/lock/history=%d/%d/%d want=%d/%d/%d", queue, locks, history, wantQueue, wantLock, wantHistory)
	}
}

func TestRecoveryDetectsUnsafeAndResumesKnownExternalBatch(t *testing.T) {
	ctx := context.Background()

	t.Run("MaxKB processing without task id requires reconciliation", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE run_files SET processing_stage='MAXKB_PROCESSING' WHERE run_file_id=?`, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 1 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		var final, reason string
		if err := database.QueryRow(`SELECT final_status,error_message FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&final, &reason); err != nil {
			t.Fatal(err)
		}
		if final != "RECONCILE_REQUIRED" || reason == "" {
			t.Fatalf("reconcile=%s reason=%q", final, reason)
		}
	})

	t.Run("MaxKB processing with task id remains retryable", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		a, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID)
		if err != nil {
			t.Fatal(err)
		}
		a.MaxKBBatchTaskID = "batch-1"
		if err := store.SaveAttempt(ctx, a); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE run_files SET processing_stage='MAXKB_PROCESSING' WHERE run_file_id=?`, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		if n, err := store.RecoverInterrupted(ctx); err != nil || n != 1 {
			t.Fatalf("recovered=%d err=%v", n, err)
		}
		var final string
		if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&final); err != nil {
			t.Fatal(err)
		}
		if final != "PENDING" {
			t.Fatalf("final=%s", final)
		}
	})
}

func TestHandleExecutionErrorRetriesSafeRunAndReconcilesUnsafeRun(t *testing.T) {
	ctx := context.Background()

	t.Run("safe error requeues", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		handled, err := store.HandleExecutionError(ctx, task.TaskID, files[0].RunFileID, "temporary repository failure")
		if err != nil || !handled {
			t.Fatalf("handled=%v err=%v", handled, err)
		}
		assertRunState(t, database, task.TaskID, "QUEUED", 1, 1, 1)
	})

	t.Run("unsafe error requires reconciliation", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE run_files SET processing_stage='MAXKB_PROCESSING' WHERE run_file_id=?`, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNext(ctx, "test"); err != nil {
			t.Fatal(err)
		}
		handled, err := store.HandleExecutionError(ctx, task.TaskID, files[0].RunFileID, "batch create outcome unknown")
		if err != nil || handled {
			t.Fatalf("handled=%v err=%v", handled, err)
		}
		var final string
		if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&final); err != nil {
			t.Fatal(err)
		}
		if final != "RECONCILE_REQUIRED" {
			t.Fatalf("final=%s", final)
		}
	})
}

func TestFinalizeRunStatusIncludesReconciliation(t *testing.T) {
	ctx := context.Background()

	t.Run("only reconciliation is failed", func(t *testing.T) {
		_, store, task, files := reliabilityFixture(t)
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkReconcile(ctx, files[0].RunFileID, "unknown remote result"); err != nil {
			t.Fatal(err)
		}
		status, err := store.Complete(ctx, task.TaskID, "reconciliation required")
		if err != nil || status != types.RunStatusFailed {
			t.Fatalf("status=%s err=%v", status, err)
		}
	})

	t.Run("success plus reconciliation is partial", func(t *testing.T) {
		database, store, task, files := reliabilityFixture(t)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := database.Exec(`INSERT INTO sync_files(file_id,folder_id,relative_path,file_status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, "file-2", "folder-1", "b.md", "PENDING", now, now); err != nil {
			t.Fatal(err)
		}
		files = append(files, &RunFile{RunFileID: "rf-2", TaskID: task.TaskID, FileID: "file-2", CreatedAt: time.Now().UTC()})
		if err := store.CreateRunPlan(ctx, task, files); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartOrResumeAttempt(ctx, files[1].RunFileID); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkReconcile(ctx, files[1].RunFileID, "unknown remote result"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE run_files SET final_status='SUCCESS',processing_stage='COMPLETED',completed_at=? WHERE run_file_id=?`, now, files[0].RunFileID); err != nil {
			t.Fatal(err)
		}
		status, err := store.Complete(ctx, task.TaskID, "partial reconciliation")
		if err != nil || status != types.RunStatusPartialSuccess {
			t.Fatalf("status=%s err=%v", status, err)
		}
	})
}

func TestResolveDeleteReconciliationKeepsLocalFileDeleted(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if _, err := database.Exec(`UPDATE sync_files SET file_status='NEEDS_DELETE',remote_doc_id='doc-old' WHERE file_id=?`, files[0].FileID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	a, err := store.StartOrResumeAttempt(ctx, files[0].RunFileID)
	if err != nil {
		t.Fatal(err)
	}
	a.DeletingDocumentID = "doc-old"
	if err := store.SaveAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE run_files SET processing_stage='MAXKB_DELETING' WHERE run_file_id=?`, files[0].RunFileID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReconcile(ctx, files[0].RunFileID, "delete outcome unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveReconcile(ctx, files[0].RunFileID, "REMOTE_SUCCEEDED", ""); err != nil {
		t.Fatal(err)
	}
	var fileStatus, remoteDocID, stage, final, runStatus string
	if err := database.QueryRow(`SELECT file_status,remote_doc_id FROM sync_files WHERE file_id=?`, files[0].FileID).Scan(&fileStatus, &remoteDocID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT processing_stage,final_status FROM run_files WHERE run_file_id=?`, files[0].RunFileID).Scan(&stage, &final); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT status FROM sync_runs WHERE id=?`, task.TaskID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if fileStatus != "DELETED" || remoteDocID != "" || stage != "MAXKB_DELETE_COMPLETED" || final != "SUCCESS" || runStatus != "SUCCESS" {
		t.Fatalf("file=%s remote=%q stage=%s final=%s run=%s", fileStatus, remoteDocID, stage, final, runStatus)
	}
}

func TestCommitDeleteCheckpointClearsOldMappingWithoutFinalizingRun(t *testing.T) {
	ctx := context.Background()
	database, store, task, files := reliabilityFixture(t)
	if err := store.CreateRunPlan(ctx, task, files); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sync_files SET file_status='STALE_REMOTE_EXISTS',remote_doc_id='doc-old' WHERE file_id='file-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartOrResumeAttempt(ctx, "rf-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE file_attempts SET deleting_document_id='doc-old',delete_completed_at=? WHERE run_file_id='rf-1'`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitDeleteCheckpoint(ctx, "rf-1", "doc-old"); err != nil {
		t.Fatal(err)
	}
	var status, remote, runStatus string
	if err := database.QueryRow(`SELECT file_status,remote_doc_id FROM sync_files WHERE file_id='file-1'`).Scan(&status, &remote); err != nil {
		t.Fatal(err)
	}
	if status != "PENDING" || remote != "" {
		t.Fatalf("mapping after delete checkpoint = status=%q remote=%q", status, remote)
	}
	if err := database.QueryRow(`SELECT final_status FROM run_files WHERE run_file_id='rf-1'`).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "PENDING" {
		t.Fatalf("run file should remain pending for replacement upload, got %q", runStatus)
	}
}
