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
	if err == nil || !strings.Contains(err.Error(), "TASK_CREATE_FAILED: 创建同步批次失败：database unavailable") {
		t.Fatalf("error = %v, want wrapped create failure", err)
	}
	if errors.Is(err, service.ErrNoPendingChanges) {
		t.Fatal("ordinary create failure was classified as no pending changes")
	}
}

func TestErrorCategoryCoversExecutionStages(t *testing.T) {
	tests := map[string]string{
		"MAXKB_SPLIT_TIMEOUT":           "MAXKB_SPLIT",
		"MINERU_RESULT_DOWNLOAD_FAILED": "MINERU_DOWNLOAD",
		"CONFIGURATION":                 "CONFIGURATION",
		"UNSUPPORTED_FILE_TYPE":         "UNSUPPORTED_FILE_TYPE",
		"TASK_QUEUE_FAILED":             "TASK_QUEUE",
		"LOCAL_STORAGE_FAILED":          "LOCAL_SYSTEM",
		"CRASH_WINDOW_UNKNOWN":          "RECONCILE",
	}
	for code, want := range tests {
		if got := errorCategory(code); got != want {
			t.Errorf("errorCategory(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestTaskAndRunFileFallbackErrorCodes(t *testing.T) {
	if got := taskErrorCode("failed to commit durable run state"); got != "LOCAL_STORAGE_FAILED" {
		t.Fatalf("taskErrorCode() = %q", got)
	}
	if got := runFileFallbackErrorCode("MAXKB_CREATING", "unexpected executor failure"); got != "MAXKB_CREATE_FAILED" {
		t.Fatalf("runFileFallbackErrorCode() = %q", got)
	}
}
