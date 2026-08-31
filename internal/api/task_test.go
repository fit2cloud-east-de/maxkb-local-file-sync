package api

import (
	"errors"
	"strings"
	"testing"

	"maxkb-local-file-sync/internal/service"
)

func TestWrapCreateTaskErrorMarksNoPendingChanges(t *testing.T) {
	err := wrapCreateTaskError(service.ErrNoPendingChanges)
	if err == nil {
		t.Fatal("wrapCreateTaskError returned nil")
	}
	if got := err.Error(); got != "NO_PENDING_CHANGES: no pending changes to sync" {
		t.Fatalf("error = %q, want stable no-change marker", got)
	}
}

func TestWrapCreateTaskErrorKeepsOtherFailures(t *testing.T) {
	cause := errors.New("database unavailable")
	err := wrapCreateTaskError(cause)
	if err == nil || !strings.Contains(err.Error(), "failed to create task: database unavailable") {
		t.Fatalf("error = %v, want wrapped create failure", err)
	}
	if errors.Is(err, service.ErrNoPendingChanges) {
		t.Fatal("ordinary create failure was classified as no pending changes")
	}
}
