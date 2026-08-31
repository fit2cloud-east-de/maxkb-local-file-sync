package service

import "maxkb-local-file-sync/internal/pkg/types"

// RunStateMachine is a small service-facing facade over the single explicit
// batch transition allow-list. Keeping this in the service package makes
// control-flow decisions testable without involving Wails or the database.
type RunStateMachine struct{}

func (RunStateMachine) CanTransition(from, to types.RunStatus) bool {
	return types.CanTransitionRunStatus(from, to)
}

func (RunStateMachine) Validate(from, to types.RunStatus) error {
	return types.ValidateRunTransition(from, to)
}
