package service

import (
	"context"
	"errors"
	"fmt"

	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

// controlCheckpointError is returned only after an atomic operation has
// reached a durable checkpoint. The orchestrator finalizes the request; it
// must never be treated as a file failure or an execution-error retry.
type controlCheckpointError struct {
	Status types.RunStatus
}

func (e *controlCheckpointError) Error() string {
	return fmt.Sprintf("run control requested at safe checkpoint: %s", e.Status)
}

func isControlCheckpoint(err error) (*controlCheckpointError, bool) {
	var control *controlCheckpointError
	return control, errors.As(err, &control)
}

func checkpointRun(ctx context.Context, store *repository.ReliabilityStore, runID string) error {
	if store == nil {
		return nil
	}
	status, err := store.GetRunStatus(ctx, runID)
	if err != nil {
		return err
	}
	switch status {
	case types.RunStatusRunning:
		return nil
	case types.RunStatusPauseRequested, types.RunStatusStopRequested,
		types.RunStatusPaused, types.RunStatusStopped, types.RunStatusCancelled:
		return &controlCheckpointError{Status: status}
	default:
		return fmt.Errorf("run %s reached unexpected execution status %s", runID, status)
	}
}
