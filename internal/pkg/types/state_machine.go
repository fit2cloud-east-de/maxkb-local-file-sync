package types

import "fmt"

// CanTransitionRunStatus is the single allow-list for durable batch state
// transitions. Callers must persist only transitions returned as allowed by
// this function; terminal states are intentionally not reusable.
func CanTransitionRunStatus(from, to RunStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case RunStatusQueued:
		return to == RunStatusRunning || to == RunStatusCancelled
	case RunStatusRunning:
		return to == RunStatusPauseRequested || to == RunStatusStopRequested ||
			to == RunStatusSuccess || to == RunStatusPartialSuccess ||
			to == RunStatusFailed || to == RunStatusInterrupted
	case RunStatusPauseRequested:
		return to == RunStatusPaused || to == RunStatusStopRequested || to == RunStatusInterrupted
	case RunStatusPaused:
		return to == RunStatusQueued || to == RunStatusStopped
	case RunStatusStopRequested:
		return to == RunStatusStopped || to == RunStatusInterrupted
	case RunStatusInterrupted:
		return to == RunStatusQueued || to == RunStatusPaused || to == RunStatusStopped
	default:
		return false
	}
}

// ValidateRunTransition returns a descriptive error for an illegal batch
// transition. Idempotent transitions are accepted by design so repeated UI
// requests are safe.
func ValidateRunTransition(from, to RunStatus) error {
	if CanTransitionRunStatus(from, to) {
		return nil
	}
	return fmt.Errorf("invalid run status transition: %s -> %s", from, to)
}
