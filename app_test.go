package main

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundAPIsReturnStartupErrorInsteadOfPanicking(t *testing.T) {
	app := NewApp()

	folders, err := app.ListFolders()
	if err == nil {
		t.Fatal("expected not-ready error")
	}
	if folders != nil {
		t.Fatalf("expected nil folders on startup failure, got %#v", folders)
	}
	if !strings.Contains(err.Error(), "application is not ready") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBoundAPIsExposeSanitizedStartupError(t *testing.T) {
	app := NewApp()
	app.startupErr = errors.New("database migration failed: dirty database version 1")

	if _, err := app.ListFolders(); err == nil || err.Error() != app.startupErr.Error() {
		t.Fatalf("expected stored startup error, got %v", err)
	}
}
