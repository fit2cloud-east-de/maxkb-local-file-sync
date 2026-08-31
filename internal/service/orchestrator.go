package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// TaskOrchestrator is backed by job_queue. The channel is only a wake-up hint;
// no business work is stored in memory, so a process restart cannot lose runs.
type TaskOrchestrator struct {
	taskSvc      *TaskService
	syncExecutor *SyncExecutor
	runFileRepo  repository.RunFileRepository
	reliability  *repository.ReliabilityStore
	logger       *logger.Logger

	wakeChan chan struct{}
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
}

func NewTaskOrchestrator(taskSvc *TaskService, syncExecutor *SyncExecutor, runFileRepo repository.RunFileRepository, logger *logger.Logger) *TaskOrchestrator {
	return &TaskOrchestrator{taskSvc: taskSvc, syncExecutor: syncExecutor, runFileRepo: runFileRepo, logger: logger, wakeChan: make(chan struct{}, 1), stopChan: make(chan struct{})}
}

func (o *TaskOrchestrator) SetReliabilityStore(store *repository.ReliabilityStore) {
	o.reliability = store
}
func (o *TaskOrchestrator) SetSyncExecutor(executor *SyncExecutor) {
	o.mu.Lock()
	o.syncExecutor = executor
	o.mu.Unlock()
}

func (o *TaskOrchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.running {
		return fmt.Errorf("orchestrator already running")
	}
	o.running = true
	o.stopChan = make(chan struct{})
	o.wg.Add(1)
	go o.processQueue(ctx)
	o.signal()
	o.logger.Info("Task orchestrator started")
	return nil
}

func (o *TaskOrchestrator) Stop(ctx context.Context) error {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return nil
	}
	close(o.stopChan)
	o.running = false
	o.mu.Unlock()
	done := make(chan struct{})
	go func() { o.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	o.logger.Info("Task orchestrator stopped")
	return nil
}

func (o *TaskOrchestrator) Wake() { o.signal() }

func (o *TaskOrchestrator) signal() {
	select {
	case o.wakeChan <- struct{}{}:
	default:
	}
}

// EnqueueTask only wakes the worker. CreateRunPlan has already committed the
// queue row, which makes this operation safe even if the worker is stopped.
func (o *TaskOrchestrator) EnqueueTask(ctx context.Context, taskID string) error {
	o.mu.Lock()
	running := o.running
	o.mu.Unlock()
	if !running {
		return fmt.Errorf("orchestrator not running")
	}
	o.signal()
	return nil
}

func (o *TaskOrchestrator) processQueue(ctx context.Context) {
	defer o.wg.Done()
	for {
		select {
		case <-o.stopChan:
			return
		default:
		}
		if o.reliability == nil {
			select {
			case <-o.stopChan:
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		runID, err := o.reliability.ClaimNext(ctx, "local-worker")
		if err != nil {
			o.logger.Error("durable queue claim failed: %v", err)
			select {
			case <-o.stopChan:
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if runID == "" {
			select {
			case <-o.stopChan:
				return
			case <-o.wakeChan:
			case <-time.After(750 * time.Millisecond):
			}
			continue
		}
		if err := o.executeTask(ctx, runID); err != nil {
			o.logger.Error("Task execution failed: task_id=%s, error=%v", runID, err)
			// A claimed queue row has already been removed. Persist an explicit
			// requeue (or finalize a pending control request) so an unexpected
			// repository/context error cannot strand the run in RUNNING forever.
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, recoveryErr := o.reliability.HandleExecutionError(recoveryCtx, runID, "", err.Error())
			cancel()
			if recoveryErr != nil {
				o.logger.Error("Failed to persist task execution recovery: task_id=%s, error=%v", runID, recoveryErr)
			}
		}
	}
}

func (o *TaskOrchestrator) executeTask(ctx context.Context, taskID string) error {
	startTime := time.Now()
	if err := o.checkpoint(ctx, taskID); err != nil {
		return err
	}
	runFiles, err := o.taskSvc.GetRunFiles(ctx, taskID)
	if err != nil {
		if completeErr := o.taskSvc.CompleteTask(ctx, taskID, false, fmt.Sprintf("failed to get run files: %v", err)); completeErr != nil {
			return fmt.Errorf("get run files: %w; complete task: %v", err, completeErr)
		}
		return err
	}

	for ordinal, runFile := range runFiles {
		if err := o.checkpoint(ctx, taskID); err != nil {
			return err
		}
		if runFile.FinalStatus != types.FileFinalStatusPending {
			continue
		}

		o.mu.Lock()
		executor := o.syncExecutor
		o.mu.Unlock()
		if executor == nil {
			return fmt.Errorf("sync executor is not configured")
		}
		if err := executor.ExecuteRunFile(ctx, runFile.RunFileID); err != nil {
			var control *controlCheckpointError
			if errors.As(err, &control) {
				return o.finalizeControl(ctx, taskID, control.Status)
			}
			o.logger.Error("Run file execution failed: run_file_id=%s, error=%v", runFile.RunFileID, err)
			if o.reliability != nil {
				handled, recoveryErr := o.reliability.HandleExecutionError(ctx, taskID, runFile.RunFileID, err.Error())
				if recoveryErr != nil {
					return fmt.Errorf("run file execution: %w; durable recovery: %v", err, recoveryErr)
				}
				if handled {
					return nil
				}
			}
		}

		// Re-read every durable result. In particular, RECONCILE_REQUIRED is not a
		// normal failure and must never be counted as a successful retry.
		currentFiles, err := o.taskSvc.GetRunFiles(ctx, taskID)
		if err != nil {
			return err
		}
		successCount, failedCount, skippedCount := durableCounts(currentFiles)
		if o.reliability != nil {
			if err := o.reliability.UpdateProgress(ctx, taskID, successCount, failedCount, skippedCount); err != nil {
				return err
			}
			if err := o.reliability.UpdateCheckpoint(ctx, taskID, ordinal+1); err != nil {
				return err
			}
		} else if err := o.taskSvc.UpdateTaskProgress(ctx, taskID, successCount, failedCount, skippedCount); err != nil {
			return err
		}

		// Stop/pause is cooperative: the side effect that was already in flight
		// gets to commit, and only then do we finalize the control request.
		if err := o.checkpoint(ctx, taskID); err != nil {
			return err
		}
	}

	if err := o.checkpoint(ctx, taskID); err != nil {
		return err
	}
	if o.reliability != nil {
		if _, err := o.reliability.Complete(ctx, taskID, ""); err != nil {
			return fmt.Errorf("failed to complete task: %w", err)
		}
	} else if err := o.taskSvc.CompleteTask(ctx, taskID, false, ""); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}
	o.logger.Info("Task execution completed: task_id=%s, duration=%v", taskID, time.Since(startTime))
	return nil
}

func (o *TaskOrchestrator) checkpoint(ctx context.Context, taskID string) error {
	if o.reliability == nil {
		return nil
	}
	status, err := o.reliability.GetRunStatus(ctx, taskID)
	if err != nil {
		return err
	}
	switch status {
	case types.RunStatusRunning:
		return nil
	case types.RunStatusPauseRequested, types.RunStatusStopRequested:
		return o.finalizeControl(ctx, taskID, status)
	case types.RunStatusPaused, types.RunStatusStopped, types.RunStatusCancelled:
		return &controlCheckpointError{Status: status}
	default:
		return fmt.Errorf("run %s cannot execute from status %s", taskID, status)
	}
}

func (o *TaskOrchestrator) finalizeControl(ctx context.Context, taskID string, status types.RunStatus) error {
	if o.reliability == nil {
		return nil
	}
	switch status {
	case types.RunStatusPauseRequested:
		return o.reliability.FinalizePause(ctx, taskID, "pause finalized at safe checkpoint")
	case types.RunStatusStopRequested:
		return o.reliability.FinalizeStop(ctx, taskID, "stop finalized at safe checkpoint")
	case types.RunStatusPaused, types.RunStatusStopped, types.RunStatusCancelled:
		return nil
	default:
		return fmt.Errorf("cannot finalize control for status %s", status)
	}
}

func durableCounts(files []*repository.RunFile) (success, failed, skipped int) {
	for _, file := range files {
		switch file.FinalStatus {
		case types.FileFinalStatusSuccess:
			success++
		case types.FileFinalStatusFailed, types.FileFinalStatusReconcileRequired:
			failed++
		case types.FileFinalStatusSkipped:
			skipped++
		}
	}
	return
}

func (o *TaskOrchestrator) RecoverTask(ctx context.Context, taskID string) error {
	if o.reliability == nil {
		return fmt.Errorf("durable reliability store is not configured")
	}
	if _, err := o.reliability.RecoverInterrupted(ctx); err != nil {
		return err
	}
	o.signal()
	return nil
}

func (o *TaskOrchestrator) RecoverAllTasks(ctx context.Context) error {
	if o.reliability == nil {
		return fmt.Errorf("durable reliability store is not configured")
	}
	n, err := o.reliability.RecoverInterrupted(ctx)
	if err != nil {
		return err
	}
	o.signal()
	o.logger.Info("Recovered %d durable runs", n)
	return nil
}
