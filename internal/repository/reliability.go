package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

var ErrActiveRun = errors.New("an active sync run already exists for this folder")

// FileAttempt records every externally observable attempt. Remote references
// are persisted before the next non-idempotent step so a restart can reconcile
// instead of blindly repeating an operation.
type FileAttempt struct {
	ID, RunFileID                       string
	AttemptNo                           int
	Status                              string
	StartedAt                           time.Time
	CompletedAt                         *time.Time
	ErrorCode, ErrorMessage             string
	MinerURemoteRef, MinerUTaskID       string
	MinerUStatus                        string
	MaxKBSourceFileID, MaxKBBatchTaskID string
	MaxKBDocumentID, DeletingDocumentID string
	DeleteStartedAt, DeleteCompletedAt  *time.Time
	DeleteRetryCount                    int
	SnapshotPath, SnapshotMD5           string
	SnapshotSize                        int64
	SnapshotModifiedAt                  *time.Time
	SourceMD5Before, SourceMD5After     string
	SourceChangedDuringProcessing       bool
	RequestFingerprint, ReconcileReason string
}

type QueueStats struct {
	Queued, Running, Paused, ReconcileRequired int
}

// RunMetadata exposes durable-only state that is not duplicated in the
// backward-compatible sync_tasks table.
type RunMetadata struct {
	RecoveryCount  int
	ReconcileCount int
	ControlReason  string
	ErrorSummary   string
}

// ReconcileItem is the durable audit view shown to the user when the outcome
// of a remote side effect cannot be determined safely.
type ReconcileItem struct {
	RunFileID, TaskID, FileID, FolderID, FolderName, RelativePath string
	ProcessingStage, Reason, SnapshotPath, SnapshotMD5            string
	SnapshotSize                                                  int64
	MaxKBSourceFileID, MaxKBBatchTaskID, MaxKBDocumentID          string
	DeletingDocumentID, MinerUTaskID, MinerUStatus                string
	CreatedAt, CompletedAt                                        string
}

type ReliabilityStore struct{ db *db.DB }

func NewReliabilityStore(database *db.DB) *ReliabilityStore { return &ReliabilityStore{db: database} }

func nowText() string             { return time.Now().UTC().Format(time.RFC3339Nano) }
func timeText(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func execChecked(tx *db.ImmediateTx, ctx context.Context, query, operation string, args ...interface{}) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	} else if affected == 0 {
		return fmt.Errorf("%s: no rows affected", operation)
	}
	return nil
}

func execAny(tx *db.ImmediateTx, ctx context.Context, query, operation string, args ...interface{}) error {
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func rowsAffected(result sql.Result, operation string, expected int64) error {
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if n != expected {
		return fmt.Errorf("%s: expected %d rows affected, got %d", operation, expected, n)
	}
	return nil
}

// finalizeRunTx derives a terminal batch status from all run files. It must be
// called while the caller owns a BEGIN IMMEDIATE transaction.
func finalizeRunTx(ctx context.Context, tx *db.ImmediateTx, runID, errorSummary, now string) (types.RunStatus, error) {
	var pending, success, failed, skipped, reconcile int
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN final_status='PENDING' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN final_status='SUCCESS' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN final_status='FAILED' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN final_status='SKIPPED' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN final_status='RECONCILE_REQUIRED' THEN 1 ELSE 0 END),0)
		FROM run_files WHERE task_id=?`, runID).Scan(&pending, &success, &failed, &skipped, &reconcile); err != nil {
		return "", fmt.Errorf("calculate run result: %w", err)
	}
	if pending > 0 {
		return types.RunStatusRunning, nil
	}
	status := types.RunStatusSuccess
	if failed+reconcile > 0 {
		if success > 0 {
			status = types.RunStatusPartialSuccess
		} else {
			status = types.RunStatusFailed
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status=?,completed_at=?,success_count=?,failed_count=?,skipped_count=?,reconcile_count=?,error_summary=? WHERE id=? AND status NOT IN ('STOPPED','CANCELLED')`, string(status), now, success, failed, skipped, reconcile, errorSummary, runID)
	if err != nil {
		return "", fmt.Errorf("complete durable run: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		return "", fmt.Errorf("complete durable run rows affected: %w", err)
	} else if affected == 0 {
		// A reconciliation decision can arrive after the batch was explicitly
		// stopped or cancelled. The file decision is still valid, but a terminal
		// control state must not be overwritten by the derived SUCCESS/FAILED
		// status. Refresh its counters and leave the terminal status untouched.
		var currentStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&currentStatus); err != nil {
			return "", fmt.Errorf("complete durable run: %w", err)
		}
		if currentStatus != "STOPPED" && currentStatus != "CANCELLED" {
			return "", fmt.Errorf("complete durable run: expected 1 rows affected, got %d", affected)
		}
		if err := execAny(tx, ctx, `UPDATE sync_runs SET success_count=?,failed_count=?,skipped_count=?,reconcile_count=?,error_summary=? WHERE id=?`, "refresh terminal run counters", success, failed, skipped, reconcile, errorSummary, runID); err != nil {
			return "", err
		}
		res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET success_count=?,failed_count=?,skipped_count=?,error_message=? WHERE task_id=?`, success, failed, skipped, errorSummary, runID)
		if err != nil {
			return "", fmt.Errorf("refresh terminal public task: %w", err)
		}
		if err := rowsAffected(res, "refresh terminal public task", 1); err != nil {
			return "", err
		}
		return types.RunStatus(currentStatus), nil
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status=?,completed_at=?,success_count=?,failed_count=?,skipped_count=?,error_message=? WHERE task_id=?`, string(status), now, success, failed, skipped, errorSummary, runID)
	if err != nil {
		return "", fmt.Errorf("complete public task: %w", err)
	}
	if err := rowsAffected(res, "complete public task", 1); err != nil {
		return "", err
	}
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove completed queue row", runID); err != nil {
		return "", err
	}
	if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove completed lock", runID); err != nil {
		return "", err
	}
	return status, nil
}

// CreateRunPlan atomically creates the public run, durable run state, file
// execution plan, queue entry, and active-folder lock.
func (s *ReliabilityStore) CreateRunPlan(ctx context.Context, task *SyncTask, runFiles []*RunFile) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM active_task_locks WHERE folder_id=?`, task.FolderID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrActiveRun
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO sync_tasks(
		task_id,folder_id,kb_id,workspace_id,trigger_type,run_status,processing_stage,
		control_state,created_at,total_files,success_count,failed_count,skipped_count,error_message
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.TaskID, task.FolderID, task.KBId, task.WorkspaceID,
		string(task.TriggerType), string(types.RunStatusQueued), string(types.ProcessingStageInit),
		string(types.ControlStateActive), timeText(task.CreatedAt), len(runFiles), 0, 0, 0, "")
	if err != nil {
		return fmt.Errorf("create public run: %w", err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO sync_runs(
		id,task_id,folder_id,trigger_type,status,queued_at,total_files
	) VALUES(?,?,?,?,?,?,?)`, task.TaskID, task.TaskID, task.FolderID, string(task.TriggerType),
		string(types.RunStatusQueued), timeText(task.CreatedAt), len(runFiles))
	if err != nil {
		return fmt.Errorf("create durable run: %w", err)
	}

	for i, rf := range runFiles {
		_, err = tx.ExecContext(ctx, `INSERT INTO run_files(
			run_file_id,task_id,file_id,ordinal,processing_stage,control_state,final_status,
			snapshot_path,snapshot_size,snapshot_modified_at,snapshot_md5,error_message,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, rf.RunFileID, task.TaskID, rf.FileID, i+1,
			string(types.ProcessingStageInit), string(types.ControlStateActive), string(types.FileFinalStatusPending),
			"", 0, nil, "", "", timeText(rf.CreatedAt))
		if err != nil {
			return fmt.Errorf("create run file: %w", err)
		}
	}

	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at)
		VALUES(?,?,?,?,?)`, task.TaskID, task.TaskID, 100, now, now); err != nil {
		return fmt.Errorf("enqueue durable run: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at)
		VALUES(?,?,?,?,?,?,?)`, uuid.NewString(), task.TaskID, task.TaskID, task.FolderID,
		string(types.RunStatusQueued), now, now); err != nil {
		if isUniqueConstraint(err) {
			return ErrActiveRun
		}
		return fmt.Errorf("create active lock: %w", err)
	}
	return tx.Commit(ctx)
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return containsText(m, "UNIQUE constraint failed") || containsText(m, "constraint failed")
}
func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ClaimNext atomically removes the queue head and transitions it to RUNNING.
// Invalid queue rows are cleaned in the same transaction.
func (s *ReliabilityStore) ClaimNext(ctx context.Context, owner string) (string, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var running int
	// PAUSE_REQUESTED and STOP_REQUESTED still own the global execution slot
	// until the current atomic operation reaches a safety checkpoint.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE status IN ('RUNNING','PAUSE_REQUESTED','STOP_REQUESTED')`).Scan(&running); err != nil {
		return "", fmt.Errorf("count running runs: %w", err)
	}
	if running > 0 {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", nil
	}
	// Remove rows whose parent run or task was deleted. They cannot be claimed
	// and otherwise remain at the head of the durable queue forever.
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE NOT EXISTS (SELECT 1 FROM sync_runs r WHERE r.id=job_queue.run_id) OR NOT EXISTS (SELECT 1 FROM sync_tasks t WHERE t.task_id=job_queue.task_id)`, "clean orphan queue rows"); err != nil {
		return "", err
	}
	for {
		var runID, taskID, status string
		err := tx.QueryRowContext(ctx, `SELECT q.run_id,q.task_id,r.status
			FROM job_queue q JOIN sync_runs r ON r.id=q.run_id
			JOIN sync_tasks t ON t.task_id=q.task_id
			WHERE q.available_at<=? ORDER BY q.priority,q.queued_at,q.id LIMIT 1`, nowText()).Scan(&runID, &taskID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return "", err
			}
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read queue head: %w", err)
		}
		if status != string(types.RunStatusQueued) {
			if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove invalid queue row", runID); err != nil {
				return "", err
			}
			if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove invalid queue lock", runID); err != nil {
				return "", err
			}
			continue
		}
		var folderID string
		if err := tx.QueryRowContext(ctx, `SELECT folder_id FROM sync_runs WHERE id=?`, runID).Scan(&folderID); err != nil {
			return "", err
		}
		var conflictingRun string
		err = tx.QueryRowContext(ctx, `SELECT run_id FROM active_task_locks WHERE folder_id=? AND run_id<>? LIMIT 1`, folderID, runID).Scan(&conflictingRun)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("check folder lock: %w", err)
		}
		if conflictingRun != "" {
			// Keep the durable queue row intact. A folder conflict is a temporary
			// scheduling condition, not an interruption or a failed batch. Returning
			// no work lets the worker retry after the conflicting lock is released.
			if err := tx.Commit(ctx); err != nil {
				return "", err
			}
			return "", nil
		}
		now := nowText()
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='RUNNING',started_at=COALESCE(started_at,?),current_file_ordinal=0 WHERE id=? AND status='QUEUED'`, now, runID)
		if err != nil {
			return "", fmt.Errorf("claim durable run: %w", err)
		}
		if err := rowsAffected(res, "claim durable run", 1); err != nil {
			return "", err
		}
		res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='RUNNING',control_state='ACTIVE',started_at=COALESCE(started_at,?) WHERE task_id=?`, now, taskID)
		if err != nil {
			return "", fmt.Errorf("claim public task: %w", err)
		}
		if err := rowsAffected(res, "claim public task", 1); err != nil {
			return "", err
		}
		res, err = tx.ExecContext(ctx, `UPDATE active_task_locks SET run_status='RUNNING',heartbeat_at=? WHERE run_id=?`, now, runID)
		if err != nil {
			return "", fmt.Errorf("claim active lock: %w", err)
		}
		if err := rowsAffected(res, "claim active lock", 1); err != nil {
			// A queued run may have lost its lock due to a crash. Rebuild it in the
			// same transaction rather than claiming an unprotected run.
			if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?)`, "rebuild claim lock", uuid.NewString(), taskID, runID, folderID, "RUNNING", now, now); err != nil {
				return "", err
			}
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM job_queue WHERE run_id=?`, runID)
		if err != nil {
			return "", fmt.Errorf("remove claimed queue row: %w", err)
		}
		if err := rowsAffected(res, "remove claimed queue row", 1); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		_ = owner // the worker is intentionally globally serial; owner is audit API compatibility.
		return runID, nil
	}
}

func (s *ReliabilityStore) Pause(ctx context.Context, runID, reason string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&status); err != nil {
		return err
	}
	if status == "PAUSED" || status == "PAUSE_REQUESTED" {
		return tx.Commit(ctx)
	}
	now := nowText()
	if status == "QUEUED" {
		if err := finalizePauseTx(ctx, tx, runID, reason, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if status != "RUNNING" {
		return fmt.Errorf("cannot pause run in %s", status)
	}
	if status == "RUNNING" {
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='PAUSE_REQUESTED',pause_requested_at=COALESCE(pause_requested_at,?),control_reason=? WHERE id=? AND status='RUNNING'`, now, reason, runID)
		if err != nil {
			return fmt.Errorf("request pause durable run: %w", err)
		}
		if err := rowsAffected(res, "request pause durable run", 1); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='PAUSE_REQUESTED',control_state='ACTIVE' WHERE task_id=?`, runID)
		if err != nil {
			return err
		}
		if err := rowsAffected(res, "request pause public task", 1); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return tx.Commit(ctx)
}

// FinalizePause transitions a requested pause at a worker safety checkpoint.
func (s *ReliabilityStore) FinalizePause(ctx context.Context, runID, reason string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status, err := runStatusTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if status == "PAUSED" {
		return tx.Commit(ctx)
	}
	if status != "PAUSE_REQUESTED" {
		return fmt.Errorf("cannot finalize pause from %s", status)
	}
	if err := finalizePauseTx(ctx, tx, runID, reason, nowText()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func runStatusTx(ctx context.Context, tx *db.ImmediateTx, runID string) (string, error) {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&status); err != nil {
		return "", err
	}
	return status, nil
}

func finalizePauseTx(ctx context.Context, tx *db.ImmediateTx, runID, reason, now string) error {
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='PAUSED',paused_at=?,control_reason=? WHERE id=? AND status IN ('QUEUED','PAUSE_REQUESTED')`, now, reason, runID)
	if err != nil {
		return fmt.Errorf("pause durable run: %w", err)
	}
	if err := rowsAffected(res, "pause durable run", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='PAUSED',control_state='PAUSED' WHERE task_id=?`, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "pause public task", 1); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `UPDATE run_files SET control_state='PAUSED' WHERE task_id=? AND final_status='PENDING'`, "pause pending files", runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove paused queue row", runID); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE active_task_locks SET run_status='PAUSED',heartbeat_at=? WHERE run_id=?`, now, runID)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e != nil {
		return e
	} else if n == 0 {
		if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) SELECT ?,task_id,id,folder_id,'PAUSED',?,? FROM sync_runs WHERE id=?`, "rebuild pause lock", uuid.NewString(), now, now, runID); err != nil {
			return err
		}
	}
	return nil
}

func (s *ReliabilityStore) Resume(ctx context.Context, runID string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var taskID, folderID, status string
	if err := tx.QueryRowContext(ctx, `SELECT task_id,folder_id,status FROM sync_runs WHERE id=?`, runID).Scan(&taskID, &folderID, &status); err != nil {
		return err
	}
	if status != "PAUSED" && status != "INTERRUPTED" {
		return fmt.Errorf("cannot resume run in %s", status)
	}
	var conflict string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM active_task_locks WHERE folder_id=? AND run_id<>? LIMIT 1`, folderID, runID).Scan(&conflict)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if conflict != "" {
		return ErrActiveRun
	}
	now := nowText()
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='QUEUED',resumed_at=?,completed_at=NULL,control_reason='' WHERE id=? AND status IN ('PAUSED','INTERRUPTED')`, now, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "resume durable run", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='QUEUED',control_state='ACTIVE',completed_at=NULL WHERE task_id=?`, taskID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "resume public task", 1); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `UPDATE run_files SET control_state='ACTIVE' WHERE task_id=? AND control_state='PAUSED'`, "resume paused files", taskID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET priority=excluded.priority,available_at=excluded.available_at,last_error=''`, "enqueue resumed run", runID, taskID, 100, now, now); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='QUEUED',heartbeat_at=excluded.heartbeat_at`, "rebuild resumed lock", uuid.NewString(), taskID, runID, folderID, "QUEUED", now, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CancelQueued atomically cancels a batch that has not acquired the
// execution slot. It is deliberately separate from Stop: QUEUED -> CANCELLED
// is not the same lifecycle transition as RUNNING -> STOP_REQUESTED.
func (s *ReliabilityStore) CancelQueued(ctx context.Context, runID, reason string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status, err := runStatusTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if status == string(types.RunStatusCancelled) {
		return tx.Commit(ctx)
	}
	if status != string(types.RunStatusQueued) {
		return fmt.Errorf("cannot cancel queued run in %s", status)
	}
	if err := types.ValidateRunTransition(types.RunStatusQueued, types.RunStatusCancelled); err != nil {
		return err
	}
	now := nowText()
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='CANCELLED',cancelled_at=?,completed_at=?,control_reason=? WHERE id=? AND status='QUEUED'`, now, now, reason, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "cancel queued durable run", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='CANCELLED',control_state='STOPPED',completed_at=?,error_message=? WHERE task_id=?`, now, reason, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "cancel queued public task", 1); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `UPDATE run_files SET control_state='STOPPED',final_status='STOPPED',completed_at=?,error_message=? WHERE task_id=? AND final_status='PENDING'`, "stop cancelled run files", now, reason, runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove cancelled queue row", runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove cancelled lock", runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record queued cancellation", uuid.NewString(), runID, "CANCELLED", reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CancelQueuedByFolder cancels every queued batch for a disabled folder in one
// transaction. This prevents a page-size limit or a worker wake-up race from
// leaving a queued batch capable of scanning or issuing remote requests.
func (s *ReliabilityStore) CancelQueuedByFolder(ctx context.Context, folderID, reason string) (int, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sync_runs WHERE folder_id=? AND status='QUEUED' ORDER BY queued_at`, folderID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	count := 0
	for _, id := range ids {
		status, err := runStatusTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		if status != string(types.RunStatusQueued) {
			continue
		}
		if err := types.ValidateRunTransition(types.RunStatusQueued, types.RunStatusCancelled); err != nil {
			return 0, err
		}
		now := nowText()
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='CANCELLED',cancelled_at=?,completed_at=?,control_reason=? WHERE id=? AND status='QUEUED'`, now, now, reason, id)
		if err != nil {
			return 0, err
		}
		if err := rowsAffected(res, "cancel queued folder run", 1); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `UPDATE sync_tasks SET run_status='CANCELLED',control_state='STOPPED',completed_at=?,error_message=? WHERE task_id=?`, "cancel queued folder task", now, reason, id); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `UPDATE run_files SET control_state='STOPPED',final_status='STOPPED',completed_at=?,error_message=? WHERE task_id=? AND final_status='PENDING'`, "stop queued folder files", now, reason, id); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove queued folder job", id); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove queued folder lock", id); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record queued folder cancellation", uuid.NewString(), id, "CANCELLED", reason, now); err != nil {
			return 0, err
		}
		count++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *ReliabilityStore) Stop(ctx context.Context, runID, reason string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status, err := runStatusTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	switch status {
	case "SUCCESS", "FAILED", "PARTIAL_SUCCESS", "STOPPED", "CANCELLED", "COMPLETED":
		return fmt.Errorf("run is already terminal: %s", status)
	case "RUNNING", "PAUSE_REQUESTED", "STOP_REQUESTED":
		now := nowText()
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='STOP_REQUESTED',stop_requested_at=COALESCE(stop_requested_at,?),control_reason=? WHERE id=? AND status IN ('RUNNING','PAUSE_REQUESTED','STOP_REQUESTED')`, now, reason, runID)
		if err != nil {
			return err
		}
		if err := rowsAffected(res, "request stop durable run", 1); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='STOP_REQUESTED',control_state='ACTIVE' WHERE task_id=?`, runID)
		if err != nil {
			return err
		}
		if err := rowsAffected(res, "request stop public task", 1); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "QUEUED", "PAUSED", "INTERRUPTED":
		if err := finalizeStopTx(ctx, tx, runID, reason, nowText()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	default:
		return fmt.Errorf("cannot stop run in %s", status)
	}
}

// FinalizeStop transitions a requested stop at a worker safety checkpoint.
func (s *ReliabilityStore) FinalizeStop(ctx context.Context, runID, reason string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status, err := runStatusTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if status == "STOPPED" {
		return tx.Commit(ctx)
	}
	if status != "STOP_REQUESTED" {
		return fmt.Errorf("cannot finalize stop from %s", status)
	}
	if err := finalizeStopTx(ctx, tx, runID, reason, nowText()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func finalizeStopTx(ctx context.Context, tx *db.ImmediateTx, runID, reason, now string) error {
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='STOPPED',stopped_at=?,completed_at=?,control_reason=? WHERE id=? AND status IN ('QUEUED','PAUSE_REQUESTED','STOP_REQUESTED','PAUSED','INTERRUPTED')`, now, now, reason, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "stop durable run", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='STOPPED',control_state='STOPPED',completed_at=? WHERE task_id=?`, now, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "stop public task", 1); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `UPDATE run_files SET control_state='STOPPED',final_status='STOPPED',completed_at=? WHERE task_id=? AND final_status='PENDING'`, "stop pending files", now, runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove stopped queue row", runID); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove stopped lock", runID); err != nil {
		return err
	}
	return nil
}

func (s *ReliabilityStore) Complete(ctx context.Context, runID, errorSummary string) (types.RunStatus, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&status); err != nil {
		return "", err
	}
	if status == "STOPPED" || status == "CANCELLED" {
		return types.RunStatus(status), fmt.Errorf("run cannot be completed from %s", status)
	}
	result, err := finalizeRunTx(ctx, tx, runID, errorSummary, nowText())
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return result, nil
}

func (s *ReliabilityStore) UpdateProgress(ctx context.Context, runID string, success, failed, skipped int) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET success_count=?,failed_count=?,skipped_count=? WHERE id=?`, success, failed, skipped, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "update run progress", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET success_count=?,failed_count=?,skipped_count=? WHERE task_id=?`, success, failed, skipped, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "update task progress", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) UpdateCheckpoint(ctx context.Context, runID string, ordinal int) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	checkpoint := fmt.Sprintf(`{"version":1,"last_processed_index":%d,"current_file_ordinal":%d}`, ordinal, ordinal)
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET current_file_ordinal=?,checkpoint_data=? WHERE id=? AND status IN ('RUNNING','PAUSE_REQUESTED','STOP_REQUESTED')`, ordinal, checkpoint, runID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "update run checkpoint", 1); err != nil {
		return err
	}
	var taskID, folderID string
	if err := tx.QueryRowContext(ctx, `SELECT task_id,folder_id FROM sync_runs WHERE id=?`, runID).Scan(&taskID, &folderID); err != nil {
		return err
	}
	// Repair a lock lost by an interrupted write in the same transaction. The
	// folder unique index still rejects a conflicting active batch.
	if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='RUNNING',heartbeat_at=excluded.heartbeat_at`, "update run heartbeat", uuid.NewString(), taskID, runID, folderID, "RUNNING", now, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// markAmbiguousRunFileTx converts an unsafe crash window into an explicit
// reconciliation item. Returning false means the durable identifiers are
// sufficient to resume by polling/repeating an idempotent operation.
func markAmbiguousRunFileTx(ctx context.Context, tx *db.ImmediateTx, runFileID, now string) (bool, error) {
	var fileID, stage, attemptID string
	var minerUTaskID, maxKBSourceID, maxKBBatchID, deletingDocumentID string
	var deleteCompletedAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT rf.file_id,rf.processing_stage,
		COALESCE(fa.id,''),COALESCE(fa.mineru_task_id,''),COALESCE(fa.maxkb_source_file_id,''),
		COALESCE(fa.maxkb_batch_task_id,''),COALESCE(fa.deleting_document_id,''),fa.delete_completed_at
		FROM run_files rf
		LEFT JOIN file_attempts fa ON fa.id=(SELECT id FROM file_attempts WHERE run_file_id=rf.run_file_id ORDER BY attempt_no DESC LIMIT 1)
		WHERE rf.run_file_id=? AND rf.final_status='PENDING'`, runFileID).Scan(
		&fileID, &stage, &attemptID, &minerUTaskID, &maxKBSourceID, &maxKBBatchID, &deletingDocumentID, &deleteCompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect interrupted run file: %w", err)
	}

	reason := ""
	switch types.ProcessingStage(stage) {
	case types.ProcessingStageMinerURunning:
		if minerUTaskID == "" {
			reason = "process stopped around MinerU submission before a task id was durably recorded"
		}
	case types.ProcessingStageMaxKBCreating:
		if maxKBSourceID == "" {
			reason = "process stopped around MaxKB upload before a document id was durably recorded"
		}
	case types.ProcessingStageMaxKBProcessing:
		if maxKBBatchID == "" {
			reason = "process stopped around non-idempotent MaxKB batch creation before a task id was durably recorded"
		}
	case types.ProcessingStageMaxKBDeleting:
		if deletingDocumentID == "" && !deleteCompletedAt.Valid {
			reason = "process stopped around MaxKB deletion before the target/outcome was durably recorded"
		}
	}
	if reason == "" {
		return false, nil
	}

	res, err := tx.ExecContext(ctx, `UPDATE run_files SET final_status='RECONCILE_REQUIRED',error_message=?,completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, reason, now, runFileID)
	if err != nil {
		return false, err
	}
	if err := rowsAffected(res, "mark interrupted run file for reconciliation", 1); err != nil {
		return false, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='RECONCILE_REQUIRED',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return false, err
	}
	if err := rowsAffected(res, "mark interrupted sync file for reconciliation", 1); err != nil {
		return false, err
	}
	if attemptID != "" {
		res, err = tx.ExecContext(ctx, `UPDATE file_attempts SET status='RECONCILE_REQUIRED',reconcile_reason=?,error_code='CRASH_WINDOW_UNKNOWN',error_message=?,completed_at=? WHERE id=?`, reason, reason, now, attemptID)
		if err != nil {
			return false, err
		}
		if err := rowsAffected(res, "mark interrupted attempt for reconciliation", 1); err != nil {
			return false, err
		}
	}
	return true, nil
}

func markAmbiguousRunFilesTx(ctx context.Context, tx *db.ImmediateTx, runID, now string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT run_file_id FROM run_files WHERE task_id=? AND final_status='PENDING' ORDER BY ordinal`, runID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	count := 0
	for _, id := range ids {
		marked, err := markAmbiguousRunFileTx(ctx, tx, id, now)
		if err != nil {
			return 0, err
		}
		if marked {
			count++
		}
	}
	return count, nil
}

// RecoverInterrupted repairs the durable queue under one BEGIN IMMEDIATE
// transaction. RUNNING work is requeued, while pause/stop requests are
// finalized so a crash never discards the operator's intent.
func (s *ReliabilityStore) RecoverInterrupted(ctx context.Context) (int, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE NOT EXISTS (SELECT 1 FROM sync_runs r WHERE r.id=job_queue.run_id) OR NOT EXISTS (SELECT 1 FROM sync_tasks t WHERE t.task_id=job_queue.task_id)`, "clean orphan queue rows"); err != nil {
		return 0, err
	}
	if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE NOT EXISTS (SELECT 1 FROM sync_runs r WHERE r.id=active_task_locks.run_id) OR rtrim(run_id)=''`, "clean orphan locks"); err != nil {
		return 0, err
	}
	if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id IN (SELECT id FROM sync_runs WHERE status IN ('SUCCESS','FAILED','PARTIAL_SUCCESS','STOPPED','CANCELLED','COMPLETED'))`, "clean terminal locks"); err != nil {
		return 0, err
	}
	if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id IN (SELECT id FROM sync_runs WHERE status NOT IN ('QUEUED','RUNNING','INTERRUPTED'))`, "clean invalid queue states"); err != nil {
		return 0, err
	}

	type recoveryItem struct{ run, task, folder, status string }
	var controls []recoveryItem
	rows, err := tx.QueryContext(ctx, `SELECT id,task_id,folder_id,status FROM sync_runs WHERE status IN ('PAUSE_REQUESTED','STOP_REQUESTED') ORDER BY queued_at`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var x recoveryItem
		if err := rows.Scan(&x.run, &x.task, &x.folder, &x.status); err != nil {
			rows.Close()
			return 0, err
		}
		controls = append(controls, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, x := range controls {
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET last_interrupted_at=?,last_recovery_at=?,recovery_count=recovery_count+1 WHERE id=? AND status=?`, now, now, x.run, x.status)
		if err != nil {
			return 0, err
		}
		if err := rowsAffected(res, "audit interrupted control request", 1); err != nil {
			return 0, err
		}
		op := "RECOVERY_PAUSE_FINALIZED"
		detail := "process exited after pause was requested; finalized pause at startup"
		if x.status == "STOP_REQUESTED" {
			op = "RECOVERY_STOP_FINALIZED"
			detail = "process exited after stop was requested; finalized stop at startup"
		}
		if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record recovered control request", uuid.NewString(), x.task, op, detail, now); err != nil {
			return 0, err
		}
		if x.status == "PAUSE_REQUESTED" {
			if err := finalizePauseTx(ctx, tx, x.run, "recovered_user_pause_request", now); err != nil {
				return 0, err
			}
		} else if err := finalizeStopTx(ctx, tx, x.run, "recovered_user_stop_request", now); err != nil {
			return 0, err
		}
	}

	var candidates []recoveryItem
	rows, err = tx.QueryContext(ctx, `SELECT id,task_id,folder_id,status FROM sync_runs WHERE status IN ('RUNNING','INTERRUPTED') ORDER BY queued_at`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var x recoveryItem
		if err := rows.Scan(&x.run, &x.task, &x.folder, &x.status); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	recovered := 0
	for _, x := range candidates {
		var conflict string
		err := tx.QueryRowContext(ctx, `SELECT run_id FROM active_task_locks WHERE folder_id=? AND run_id<>? LIMIT 1`, x.folder, x.run).Scan(&conflict)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "replace recovery queue", x.run); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "replace recovery lock", x.run); err != nil {
			return 0, err
		}
		if x.status == "RUNNING" {
			res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='INTERRUPTED',last_interrupted_at=?,control_reason='crash_interrupted' WHERE id=? AND status='RUNNING'`, now, x.run)
			if err != nil {
				return 0, err
			}
			if err := rowsAffected(res, "mark interrupted run", 1); err != nil {
				return 0, err
			}
			if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record recovery interruption", uuid.NewString(), x.task, "RECOVERY_INTERRUPTED", "process interrupted durable batch", now); err != nil {
				return 0, err
			}
		}
		if conflict != "" {
			res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='INTERRUPTED',control_reason='recovery_blocked_by_active_folder_run',last_interrupted_at=COALESCE(last_interrupted_at,?) WHERE id=?`, now, x.run)
			if err != nil {
				return 0, err
			}
			if err := rowsAffected(res, "record blocked recovery", 1); err != nil {
				return 0, err
			}
			res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='INTERRUPTED',control_state='ACTIVE',error_message='recovery blocked by another active batch' WHERE task_id=?`, x.task)
			if err != nil {
				return 0, err
			}
			if err := rowsAffected(res, "record blocked public task", 1); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := markAmbiguousRunFilesTx(ctx, tx, x.run, now); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `UPDATE run_files SET control_state='ACTIVE' WHERE task_id=? AND final_status='PENDING'`, "activate recovered files", x.task); err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='QUEUED',recovery_count=recovery_count+1,last_recovery_at=?,control_reason='recovered_from_interrupted',completed_at=NULL WHERE id=? AND status='INTERRUPTED'`, now, x.run)
		if err != nil {
			return 0, err
		}
		if err := rowsAffected(res, "queue recovered run", 1); err != nil {
			return 0, err
		}
		res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='QUEUED',control_state='ACTIVE',completed_at=NULL,error_message='' WHERE task_id=?`, x.task)
		if err != nil {
			return 0, err
		}
		if err := rowsAffected(res, "queue recovered public task", 1); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET priority=excluded.priority,available_at=excluded.available_at,last_error=''`, "enqueue recovered run", x.run, x.task, 10, now, now); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='QUEUED',heartbeat_at=excluded.heartbeat_at`, "lock recovered run", uuid.NewString(), x.task, x.run, x.folder, "QUEUED", now, now); err != nil {
			return 0, err
		}
		recovered++
	}

	// Repair half-written QUEUED state. A conflicting folder run is converted to
	// INTERRUPTED so it can be explicitly resumed after the active run finishes.
	rows, err = tx.QueryContext(ctx, `SELECT id,task_id,folder_id,status FROM sync_runs WHERE status='QUEUED' ORDER BY queued_at`)
	if err != nil {
		return 0, err
	}
	var queued []recoveryItem
	for rows.Next() {
		var x recoveryItem
		if err := rows.Scan(&x.run, &x.task, &x.folder, &x.status); err != nil {
			rows.Close()
			return 0, err
		}
		queued = append(queued, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, x := range queued {
		var conflict string
		err := tx.QueryRowContext(ctx, `SELECT run_id FROM active_task_locks WHERE folder_id=? AND run_id<>? LIMIT 1`, x.folder, x.run).Scan(&conflict)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if conflict != "" {
			if err := execAny(tx, ctx, `DELETE FROM job_queue WHERE run_id=?`, "remove conflicting queued row", x.run); err != nil {
				return 0, err
			}
			if err := execAny(tx, ctx, `DELETE FROM active_task_locks WHERE run_id=?`, "remove conflicting queued lock", x.run); err != nil {
				return 0, err
			}
			res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='INTERRUPTED',control_reason='recovery_blocked_by_active_folder_run',last_interrupted_at=COALESCE(last_interrupted_at,?) WHERE id=? AND status='QUEUED'`, now, x.run)
			if err != nil {
				return 0, err
			}
			if err := rowsAffected(res, "interrupt conflicting queued run", 1); err != nil {
				return 0, err
			}
			if err := execAny(tx, ctx, `UPDATE sync_tasks SET run_status='INTERRUPTED',control_state='ACTIVE',error_message='recovery blocked by another active batch' WHERE task_id=?`, "interrupt conflicting queued task", x.task); err != nil {
				return 0, err
			}
			continue
		}
		if err := execAny(tx, ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, "repair queued row", x.run, x.task, 100, now, now); err != nil {
			return 0, err
		}
		if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='QUEUED',heartbeat_at=excluded.heartbeat_at`, "repair queued lock", uuid.NewString(), x.task, x.run, x.folder, "QUEUED", now, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return recovered, nil
}

// HandleExecutionError makes an unexpected executor/repository error durable.
// It never retries a crash window whose remote outcome is unknown.
func (s *ReliabilityStore) HandleExecutionError(ctx context.Context, runID, runFileID, message string) (bool, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	status, err := runStatusTx(ctx, tx, runID)
	if err != nil {
		return false, err
	}
	now := nowText()
	switch status {
	case "PAUSE_REQUESTED":
		if err := finalizePauseTx(ctx, tx, runID, "pause finalized after executor error", now); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	case "STOP_REQUESTED":
		if err := finalizeStopTx(ctx, tx, runID, "stop finalized after executor error", now); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	case "QUEUED", "PAUSED", "STOPPED", "SUCCESS", "FAILED", "PARTIAL_SUCCESS", "CANCELLED", "COMPLETED":
		return false, tx.Commit(ctx)
	case "RUNNING", "INTERRUPTED":
	default:
		return false, fmt.Errorf("cannot recover execution error from %s", status)
	}

	if runFileID == "" {
		if _, err := markAmbiguousRunFilesTx(ctx, tx, runID, now); err != nil {
			return false, err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_files WHERE task_id=? AND final_status='PENDING'`, runID).Scan(&pending); err != nil {
			return false, err
		}
		if pending == 0 {
			if _, err := finalizeRunTx(ctx, tx, runID, message, now); err != nil {
				return false, err
			}
			return true, tx.Commit(ctx)
		}
	}

	if runFileID != "" {
		marked, err := markAmbiguousRunFileTx(ctx, tx, runFileID, now)
		if err != nil {
			return false, err
		}
		if marked {
			if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record ambiguous executor failure", uuid.NewString(), runID, "EXECUTION_RECONCILE_REQUIRED", message, now); err != nil {
				return false, err
			}
			return false, tx.Commit(ctx)
		}
		var finalStatus string
		if err := tx.QueryRowContext(ctx, `SELECT final_status FROM run_files WHERE run_file_id=?`, runFileID).Scan(&finalStatus); err != nil {
			return false, err
		}
		if finalStatus != "PENDING" {
			return false, tx.Commit(ctx)
		}
	}

	var taskID, folderID string
	if err := tx.QueryRowContext(ctx, `SELECT task_id,folder_id FROM sync_runs WHERE id=?`, runID).Scan(&taskID, &folderID); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_runs SET status='QUEUED',completed_at=NULL,control_reason='requeued_after_execution_error',error_summary=? WHERE id=? AND status IN ('RUNNING','INTERRUPTED')`, message, runID)
	if err != nil {
		return false, err
	}
	if err := rowsAffected(res, "requeue run after execution error", 1); err != nil {
		return false, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='QUEUED',control_state='ACTIVE',completed_at=NULL,error_message=? WHERE task_id=?`, message, taskID)
	if err != nil {
		return false, err
	}
	if err := rowsAffected(res, "requeue public task after execution error", 1); err != nil {
		return false, err
	}
	available := timeText(time.Now().UTC().Add(time.Second))
	if err := execAny(tx, ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at,attempts,last_error) VALUES(?,?,?,?,?,1,?) ON CONFLICT(run_id) DO UPDATE SET available_at=excluded.available_at,attempts=job_queue.attempts+1,last_error=excluded.last_error`, "requeue job after execution error", runID, taskID, 20, now, available, message); err != nil {
		return false, err
	}
	if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='QUEUED',heartbeat_at=excluded.heartbeat_at`, "requeue lock after execution error", uuid.NewString(), taskID, runID, folderID, "QUEUED", now, now); err != nil {
		return false, err
	}
	if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record execution requeue", uuid.NewString(), taskID, "EXECUTION_REQUEUED", message, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// GetRunStatus returns the authoritative durable batch status for safety
// checkpoint checks performed while a remote operation is being polled.
func (s *ReliabilityStore) GetRunStatus(ctx context.Context, runID string) (types.RunStatus, error) {
	var status string
	if err := s.db.Conn().QueryRowContext(ctx, `SELECT status FROM sync_runs WHERE id=?`, runID).Scan(&status); err != nil {
		return "", fmt.Errorf("get run status: %w", err)
	}
	return types.RunStatus(status), nil
}

func (s *ReliabilityStore) GetRunMetadata(ctx context.Context, runID string) (*RunMetadata, error) {
	out := &RunMetadata{}
	err := s.db.Conn().QueryRowContext(ctx, `SELECT recovery_count,
		(SELECT COUNT(*) FROM run_files WHERE task_id=sync_runs.id AND final_status='RECONCILE_REQUIRED'),
		control_reason,error_summary FROM sync_runs WHERE id=?`, runID).Scan(
		&out.RecoveryCount, &out.ReconcileCount, &out.ControlReason, &out.ErrorSummary)
	if err != nil {
		return nil, fmt.Errorf("get durable run metadata: %w", err)
	}
	return out, nil
}

func (s *ReliabilityStore) QueueStats(ctx context.Context) (*QueueStats, error) {
	var out QueueStats
	queries := []struct {
		name  string
		query string
		dst   *int
	}{
		{"queued queue stats", `SELECT COUNT(*) FROM job_queue`, &out.Queued},
		{"running queue stats", `SELECT COUNT(*) FROM sync_runs WHERE status='RUNNING'`, &out.Running},
		{"paused queue stats", `SELECT COUNT(*) FROM sync_runs WHERE status='PAUSED'`, &out.Paused},
		{"reconcile queue stats", `SELECT COUNT(*) FROM run_files WHERE final_status='RECONCILE_REQUIRED'`, &out.ReconcileRequired},
	}
	for _, item := range queries {
		if err := s.db.Conn().QueryRowContext(ctx, item.query).Scan(item.dst); err != nil {
			return nil, fmt.Errorf("%s: %w", item.name, err)
		}
	}
	return &out, nil
}

func (s *ReliabilityStore) StartOrResumeAttempt(ctx context.Context, runFileID string) (*FileAttempt, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id, status string
	var attemptNo int
	err = tx.QueryRowContext(ctx, `SELECT id,status,attempt_no FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&id, &status, &attemptNo)
	if err == nil && status == "RUNNING" {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return s.LatestAttempt(ctx, runFileID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		attemptNo++
	} else {
		attemptNo = 1
	}
	a := &FileAttempt{ID: uuid.NewString(), RunFileID: runFileID, AttemptNo: attemptNo, Status: "RUNNING", StartedAt: time.Now().UTC()}
	if err := execAny(tx, ctx, `INSERT INTO file_attempts(id,run_file_id,attempt_no,status,started_at) VALUES(?,?,?,?,?)`, "create file attempt", a.ID, a.RunFileID, a.AttemptNo, a.Status, timeText(a.StartedAt)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *ReliabilityStore) SaveAttempt(ctx context.Context, a *FileAttempt) error {
	changed := 0
	if a.SourceChangedDuringProcessing {
		changed = 1
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res, err := tx.ExecContext(ctx, `UPDATE file_attempts SET status=?,completed_at=?,error_code=?,error_message=?,mineru_remote_ref=?,mineru_task_id=?,mineru_status=?,maxkb_source_file_id=?,maxkb_batch_task_id=?,maxkb_document_id=?,deleting_document_id=?,delete_started_at=?,delete_completed_at=?,delete_retry_count=?,snapshot_path=?,snapshot_size=?,snapshot_modified_at=?,snapshot_md5=?,source_md5_before=?,source_md5_after=?,source_changed_during_processing=?,request_fingerprint=?,reconcile_reason=? WHERE id=?`, a.Status, timePtrText(a.CompletedAt), a.ErrorCode, a.ErrorMessage, a.MinerURemoteRef, a.MinerUTaskID, a.MinerUStatus, a.MaxKBSourceFileID, a.MaxKBBatchTaskID, a.MaxKBDocumentID, a.DeletingDocumentID, timePtrText(a.DeleteStartedAt), timePtrText(a.DeleteCompletedAt), a.DeleteRetryCount, a.SnapshotPath, a.SnapshotSize, timePtrText(a.SnapshotModifiedAt), a.SnapshotMD5, a.SourceMD5Before, a.SourceMD5After, changed, a.RequestFingerprint, a.ReconcileReason, a.ID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "save file attempt", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) MarkReconcile(ctx context.Context, runFileID, reason string) error {
	if reason == "" {
		return errors.New("reconciliation reason is required")
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var fileID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM run_files WHERE run_file_id=?`, runFileID).Scan(&fileID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE run_files SET final_status='RECONCILE_REQUIRED',error_message=?,completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, reason, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "mark run file reconciliation", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE file_attempts SET status='RECONCILE_REQUIRED',reconcile_reason=?,error_message=?,completed_at=? WHERE id=(SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1)`, reason, reason, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "mark attempt reconciliation", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='RECONCILE_REQUIRED',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "mark sync file reconciliation", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CommitSyncSuccess atomically commits the remote success, local file state,
// run-file state, and attempt audit. No partially-successful combination can be
// observed after this method returns.
func (s *ReliabilityStore) CommitSyncSuccess(ctx context.Context, runFileID, remoteDocumentID, snapshotMD5, sourceMD5After string) error {
	if remoteDocumentID == "" || snapshotMD5 == "" || sourceMD5After == "" {
		return errors.New("remote document id and source MD5 are required")
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var fileID, attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM run_files WHERE run_file_id=? AND final_status='PENDING'`, runFileID).Scan(&fileID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&attemptID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='SYNCED',remote_doc_id=?,pending_remote_doc_id='',observed_md5=?,last_success_md5=?,last_synced_at=?,updated_at=? WHERE file_id=?`, remoteDocumentID, sourceMD5After, snapshotMD5, now, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit sync file success", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE run_files SET processing_stage='COMPLETED',control_state='ACTIVE',final_status='SUCCESS',snapshot_md5=?,error_message='',completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, snapshotMD5, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit run file success", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE file_attempts SET status='SUCCESS',maxkb_document_id=?,source_md5_after=?,source_changed_during_processing=0,reconcile_reason='',error_code='',error_message='',completed_at=? WHERE id=?`, remoteDocumentID, sourceMD5After, now, attemptID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit attempt success", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) CommitSourceChanged(ctx context.Context, runFileID, reason, sourceMD5After string) error {
	if reason == "" {
		reason = "source file changed during processing"
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var fileID, attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM run_files WHERE run_file_id=?`, runFileID).Scan(&fileID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&attemptID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE file_attempts SET status='FAILED',source_md5_after=?,source_changed_during_processing=1,error_code='SOURCE_CHANGED',error_message=?,completed_at=? WHERE id=?`, sourceMD5After, reason, now, attemptID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "record source change", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='STALE_REMOTE_EXISTS',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "mark stale remote file", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE run_files SET final_status='FAILED',error_message=?,completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, reason, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "mark changed run file", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CommitDeleteCheckpoint records that the old remote document was deleted as
// part of an update. It deliberately does not finalize the run file: the next
// upload is still pending. Clearing remote_doc_id in the same transaction as
// this checkpoint prevents a failed replacement upload from leaving a local
// mapping that points at a document which has already been deleted.
func (s *ReliabilityStore) CommitDeleteCheckpoint(ctx context.Context, runFileID, documentID string) error {
	if documentID == "" {
		return errors.New("deleted document id is required")
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var fileID, deletingDocumentID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM run_files WHERE run_file_id=? AND final_status='PENDING'`, runFileID).Scan(&fileID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(deleting_document_id,'') FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&deletingDocumentID); err != nil {
		return err
	}
	if deletingDocumentID != "" && deletingDocumentID != documentID {
		return fmt.Errorf("delete checkpoint document mismatch: expected %s, got %s", deletingDocumentID, documentID)
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='PENDING',remote_doc_id='',pending_remote_doc_id='',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "clear deleted replacement mapping", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) CommitDeleteSuccess(ctx context.Context, runFileID string, documentID string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var fileID, attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM run_files WHERE run_file_id=?`, runFileID).Scan(&fileID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&attemptID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='DELETED',remote_doc_id='',pending_remote_doc_id='',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit deleted sync file", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE run_files SET processing_stage='MAXKB_DELETE_COMPLETED',control_state='ACTIVE',final_status='SUCCESS',error_message='',completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit deleted run file", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE file_attempts SET status='SUCCESS',deleting_document_id='',maxkb_document_id=?,delete_completed_at=?,error_code='',error_message='',completed_at=? WHERE id=?`, documentID, now, now, attemptID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "commit delete attempt", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) CommitAttemptFailure(ctx context.Context, runFileID, code, message string) error {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&attemptID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE file_attempts SET status='FAILED',error_code=?,error_message=?,completed_at=? WHERE id=? AND status='RUNNING'`, code, message, now, attemptID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "record failed attempt", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE run_files SET final_status='FAILED',error_message=?,completed_at=? WHERE run_file_id=? AND final_status='PENDING'`, message, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "record failed run file", 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) LatestAttempt(ctx context.Context, runFileID string) (*FileAttempt, error) {
	a := &FileAttempt{}
	var started string
	var completed, delStarted, delCompleted, snapMod sql.NullString
	var changed int
	err := s.db.QueryRow(`SELECT id,run_file_id,attempt_no,status,started_at,completed_at,error_code,error_message,mineru_remote_ref,mineru_task_id,mineru_status,maxkb_source_file_id,maxkb_batch_task_id,maxkb_document_id,deleting_document_id,delete_started_at,delete_completed_at,delete_retry_count,snapshot_path,snapshot_size,snapshot_modified_at,snapshot_md5,source_md5_before,source_md5_after,source_changed_during_processing,request_fingerprint,reconcile_reason FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&a.ID, &a.RunFileID, &a.AttemptNo, &a.Status, &started, &completed, &a.ErrorCode, &a.ErrorMessage, &a.MinerURemoteRef, &a.MinerUTaskID, &a.MinerUStatus, &a.MaxKBSourceFileID, &a.MaxKBBatchTaskID, &a.MaxKBDocumentID, &a.DeletingDocumentID, &delStarted, &delCompleted, &a.DeleteRetryCount, &a.SnapshotPath, &a.SnapshotSize, &snapMod, &a.SnapshotMD5, &a.SourceMD5Before, &a.SourceMD5After, &changed, &a.RequestFingerprint, &a.ReconcileReason)
	if err != nil {
		return nil, err
	}
	a.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	a.CompletedAt = parseNullTime(completed)
	a.DeleteStartedAt = parseNullTime(delStarted)
	a.DeleteCompletedAt = parseNullTime(delCompleted)
	a.SnapshotModifiedAt = parseNullTime(snapMod)
	a.SourceChangedDuringProcessing = changed != 0
	return a, nil
}
func parseNullTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		t, err = time.Parse(time.RFC3339, v.String)
	}
	if err != nil {
		return nil
	}
	return &t
}
func timePtrText(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return timeText(*t)
}

// PromoteFailedToReconcile converts a failed file with a persisted remote
// reference into an explicit exception item. A retry request can discover this
// condition after the original failure was recorded; it must be made visible to
// the operator instead of returning an error that disappears from the exception
// handling screen.
func (s *ReliabilityStore) PromoteFailedToReconcile(ctx context.Context, runFileID, reason string) error {
	if reason == "" {
		return errors.New("reconciliation reason is required")
	}
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := nowText()
	var fileID, taskID, finalStatus string
	if err := tx.QueryRowContext(ctx, `SELECT file_id,task_id,final_status FROM run_files WHERE run_file_id=?`, runFileID).Scan(&fileID, &taskID, &finalStatus); err != nil {
		return err
	}
	if finalStatus == "RECONCILE_REQUIRED" {
		return tx.Commit(ctx)
	}
	if finalStatus != "FAILED" {
		return fmt.Errorf("run file %s is not failed", runFileID)
	}

	res, err := tx.ExecContext(ctx, `UPDATE run_files SET final_status='RECONCILE_REQUIRED',error_message=?,completed_at=COALESCE(completed_at,?) WHERE run_file_id=? AND final_status='FAILED'`, reason, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "promote failed run file to reconciliation", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='RECONCILE_REQUIRED',updated_at=? WHERE file_id=?`, now, fileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "promote sync file to reconciliation", 1); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE file_attempts SET status='RECONCILE_REQUIRED',reconcile_reason=?,error_code='RETRY_REQUIRES_RECONCILIATION',error_message=?,completed_at=COALESCE(completed_at,?) WHERE id=(SELECT id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1)`, reason, reason, now, runFileID)
	if err != nil {
		return err
	}
	if err := rowsAffected(res, "promote failed attempt to reconciliation", 1); err != nil {
		return err
	}
	if err := execAny(tx, ctx, `INSERT INTO operation_history(history_id,task_id,operation_type,operation_detail,created_at) VALUES(?,?,?,?,?)`, "record retry reconciliation requirement", uuid.NewString(), taskID, "RETRY_REQUIRES_RECONCILIATION", reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ReliabilityStore) ListReconcile(ctx context.Context) ([]*RunFile, error) {
	rows, err := s.db.Query(`SELECT run_file_id,task_id,file_id,processing_stage,control_state,final_status,snapshot_path,snapshot_size,snapshot_modified_at,snapshot_md5,error_message,created_at,started_at,completed_at FROM run_files WHERE final_status='RECONCILE_REQUIRED' ORDER BY completed_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return (&runFileRepo{db: s.db}).scanRunFiles(rows)
}

// ListReconcileItems returns the complete local and remote checkpoint needed
// for an operator to decide whether an ambiguous external operation succeeded.
func (s *ReliabilityStore) ListReconcileItems(ctx context.Context) ([]*ReconcileItem, error) {
	rows, err := s.db.Query(`SELECT rf.run_file_id,rf.task_id,rf.file_id,sf.folder_id,f.name,sf.relative_path,
		rf.processing_stage,COALESCE(NULLIF(a.reconcile_reason,''),rf.error_message,''),
		COALESCE(a.snapshot_path,rf.snapshot_path,''),COALESCE(a.snapshot_md5,rf.snapshot_md5,''),
		COALESCE(a.snapshot_size,rf.snapshot_size,0),COALESCE(a.maxkb_source_file_id,''),
		COALESCE(a.maxkb_batch_task_id,''),COALESCE(a.maxkb_document_id,''),
		COALESCE(a.deleting_document_id,''),COALESCE(a.mineru_task_id,''),COALESCE(a.mineru_status,''),
		rf.created_at,COALESCE(rf.completed_at,'')
	FROM run_files rf
	JOIN sync_files sf ON sf.file_id=rf.file_id
	JOIN sync_folders f ON f.folder_id=sf.folder_id
	LEFT JOIN file_attempts a ON a.id=(SELECT id FROM file_attempts WHERE run_file_id=rf.run_file_id ORDER BY attempt_no DESC LIMIT 1)
	WHERE rf.final_status='RECONCILE_REQUIRED' ORDER BY rf.completed_at DESC,rf.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReconcileItem
	for rows.Next() {
		x := &ReconcileItem{}
		if err := rows.Scan(&x.RunFileID, &x.TaskID, &x.FileID, &x.FolderID, &x.FolderName, &x.RelativePath,
			&x.ProcessingStage, &x.Reason, &x.SnapshotPath, &x.SnapshotMD5, &x.SnapshotSize,
			&x.MaxKBSourceFileID, &x.MaxKBBatchTaskID, &x.MaxKBDocumentID, &x.DeletingDocumentID,
			&x.MinerUTaskID, &x.MinerUStatus, &x.CreatedAt, &x.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResolveReconcile applies an explicit operator decision. A retry is only
// requeued after the operator has positively confirmed remote absence.
func (s *ReliabilityStore) ResolveReconcile(ctx context.Context, runFileID, resolution, remoteDocumentID string) (string, error) {
	tx, err := s.db.BeginImmediate(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	now := nowText()
	var runID, fileID, folderID, stage, finalStatus, snapshotMD5, attemptID, existingDoc, deleteDoc string
	if err := tx.QueryRowContext(ctx, `SELECT rf.task_id,rf.file_id,sf.folder_id,rf.processing_stage,rf.final_status,COALESCE(rf.snapshot_md5,'') FROM run_files rf JOIN sync_files sf ON sf.file_id=rf.file_id WHERE rf.run_file_id=?`, runFileID).Scan(&runID, &fileID, &folderID, &stage, &finalStatus, &snapshotMD5); err != nil {
		return "", err
	}
	if finalStatus != "RECONCILE_REQUIRED" {
		return "", fmt.Errorf("run file is not awaiting reconciliation")
	}
	if err := tx.QueryRowContext(ctx, `SELECT id,maxkb_document_id,deleting_document_id FROM file_attempts WHERE run_file_id=? ORDER BY attempt_no DESC LIMIT 1`, runFileID).Scan(&attemptID, &existingDoc, &deleteDoc); err != nil {
		return "", err
	}
	if remoteDocumentID == "" {
		remoteDocumentID = existingDoc
	}
	if remoteDocumentID == "" {
		remoteDocumentID = deleteDoc
	}
	switch resolution {
	case "REMOTE_SUCCEEDED":
		if remoteDocumentID == "" {
			return "", errors.New("remote document id is required")
		}
		if snapshotMD5 == "" && stage != string(types.ProcessingStageMaxKBDeleting) && stage != string(types.ProcessingStageMaxKBDeleteCompleted) && deleteDoc == "" {
			return "", errors.New("snapshot MD5 is required to confirm remote success")
		}
		// Deletion success must never resurrect the document in local state. The
		// operator may provide the target id when the delete request's target was
		// not durably recorded, but the resulting local state is always DELETED.
		if stage == string(types.ProcessingStageMaxKBDeleting) || stage == string(types.ProcessingStageMaxKBDeleteCompleted) || deleteDoc != "" {
			res, e := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='DELETED',remote_doc_id='',pending_remote_doc_id='',updated_at=? WHERE file_id=?`, now, fileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve remote delete success file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE run_files SET processing_stage='MAXKB_DELETE_COMPLETED',control_state='ACTIVE',final_status='SUCCESS',error_message='',completed_at=? WHERE run_file_id=? AND final_status='RECONCILE_REQUIRED'`, now, runFileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve remote delete success run file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE file_attempts SET status='SUCCESS',maxkb_document_id=?,deleting_document_id='',reconcile_reason='',error_code='',error_message='',completed_at=? WHERE id=?`, remoteDocumentID, now, attemptID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve remote delete success attempt", 1); err != nil {
				return "", err
			}
			break
		}
		res, e := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='SYNCED',remote_doc_id=?,pending_remote_doc_id='',observed_md5=?,last_success_md5=?,last_synced_at=?,updated_at=? WHERE file_id=?`, remoteDocumentID, snapshotMD5, snapshotMD5, now, now, fileID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "resolve remote success file", 1); err != nil {
			return "", err
		}
		res, e = tx.ExecContext(ctx, `UPDATE run_files SET processing_stage='COMPLETED',control_state='ACTIVE',final_status='SUCCESS',error_message='',completed_at=? WHERE run_file_id=? AND final_status='RECONCILE_REQUIRED'`, now, runFileID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "resolve remote success run file", 1); err != nil {
			return "", err
		}
		res, e = tx.ExecContext(ctx, `UPDATE file_attempts SET status='SUCCESS',maxkb_document_id=?,reconcile_reason='',error_code='',error_message='',completed_at=? WHERE id=?`, remoteDocumentID, now, attemptID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "resolve remote success attempt", 1); err != nil {
			return "", err
		}
	case "REMOTE_ABSENT_RETRY":
		var conflict string
		err := tx.QueryRowContext(ctx, `SELECT run_id FROM active_task_locks WHERE folder_id=? AND run_id<>? LIMIT 1`, folderID, runID).Scan(&conflict)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if conflict != "" {
			return "", ErrActiveRun
		}
		if stage == string(types.ProcessingStageMaxKBDeleting) || stage == string(types.ProcessingStageMaxKBDeleteCompleted) || deleteDoc != "" {
			res, e := tx.ExecContext(ctx, `UPDATE sync_files SET file_status='DELETED',remote_doc_id='',pending_remote_doc_id='',updated_at=? WHERE file_id=?`, now, fileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve absent delete file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE run_files SET processing_stage='MAXKB_DELETE_COMPLETED',control_state='ACTIVE',final_status='SUCCESS',error_message='',completed_at=? WHERE run_file_id=?`, now, runFileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve absent delete run file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE file_attempts SET status='SUCCESS',deleting_document_id='',reconcile_reason='operator_confirmed_remote_absent',error_code='',error_message='',completed_at=? WHERE id=?`, now, attemptID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "resolve absent delete attempt", 1); err != nil {
				return "", err
			}
		} else {
			res, e := tx.ExecContext(ctx, `UPDATE file_attempts SET status='RUNNING',maxkb_batch_task_id='',maxkb_document_id='',deleting_document_id='',reconcile_reason='operator_confirmed_remote_absent',error_code='',error_message='',completed_at=NULL WHERE id=?`, attemptID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "reopen retry attempt", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE run_files SET final_status='PENDING',control_state='ACTIVE',error_message='',completed_at=NULL WHERE run_file_id=?`, runFileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "reopen retry run file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='PENDING',remote_doc_id='',pending_remote_doc_id='',updated_at=? WHERE file_id=?`, now, fileID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "reopen retry sync file", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE sync_runs SET status='QUEUED',completed_at=NULL,control_reason='operator_confirmed_remote_absent',error_summary='' WHERE id=? AND status NOT IN ('STOPPED','CANCELLED')`, runID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "requeue reconciled run", 1); err != nil {
				return "", err
			}
			res, e = tx.ExecContext(ctx, `UPDATE sync_tasks SET run_status='QUEUED',control_state='ACTIVE',completed_at=NULL,error_message='' WHERE task_id=?`, runID)
			if e != nil {
				return "", e
			}
			if err := rowsAffected(res, "requeue reconciled task", 1); err != nil {
				return "", err
			}
			if err := execAny(tx, ctx, `INSERT INTO job_queue(run_id,task_id,priority,queued_at,available_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET priority=excluded.priority,available_at=excluded.available_at,last_error=''`, "requeue reconciled job", runID, runID, 5, now, now); err != nil {
				return "", err
			}
			if err := execAny(tx, ctx, `INSERT INTO active_task_locks(lock_id,task_id,run_id,folder_id,run_status,locked_at,heartbeat_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET run_status='QUEUED',heartbeat_at=excluded.heartbeat_at`, "requeue reconciled lock", uuid.NewString(), runID, runID, folderID, "QUEUED", now, now); err != nil {
				return "", err
			}
		}
	case "MARK_FAILED":
		res, e := tx.ExecContext(ctx, `UPDATE run_files SET final_status='FAILED',error_message='operator marked reconciliation as failed',completed_at=? WHERE run_file_id=? AND final_status='RECONCILE_REQUIRED'`, now, runFileID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "mark reconciliation failed run file", 1); err != nil {
			return "", err
		}
		res, e = tx.ExecContext(ctx, `UPDATE file_attempts SET status='FAILED',reconcile_reason='operator_marked_failed',error_message='operator marked reconciliation as failed',completed_at=? WHERE id=?`, now, attemptID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "mark reconciliation failed attempt", 1); err != nil {
			return "", err
		}
		res, e = tx.ExecContext(ctx, `UPDATE sync_files SET file_status='RECONCILE_REQUIRED',updated_at=? WHERE file_id=?`, now, fileID)
		if e != nil {
			return "", e
		}
		if err := rowsAffected(res, "retain reconciliation file status", 1); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported reconciliation resolution: %s", resolution)
	}
	if resolution != "REMOTE_ABSENT_RETRY" || stage == string(types.ProcessingStageMaxKBDeleting) || stage == string(types.ProcessingStageMaxKBDeleteCompleted) || deleteDoc != "" {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN final_status='PENDING' THEN 1 ELSE 0 END),0) FROM run_files WHERE task_id=?`, runID).Scan(&pending); err != nil {
			return "", err
		}
		if pending == 0 {
			if _, err := finalizeRunTx(ctx, tx, runID, "", now); err != nil {
				return "", err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return runID, nil
}
