package repository

import (
	"context"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
)

func newSystemSettingsTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "settings.db"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.InitSchema(); err != nil {
		database.Close()
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestSystemSettingsRepositoryDefaultsAndRoundTrip(t *testing.T) {
	database := newSystemSettingsTestDB(t)
	repo := NewSystemSettingsRepository(database)

	got, err := repo.GetMinerUArtifactSettings(context.Background())
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if got.SaveFullResult {
		t.Fatal("SaveFullResult default should remain false in the legacy InitSchema fixture")
	}
	if got.ResultSaveDir != "" {
		t.Fatalf("ResultSaveDir default=%q, want empty", got.ResultSaveDir)
	}
	if !got.CleanupTemporaryResults {
		t.Fatal("CleanupTemporaryResults default should be true")
	}

	want := MinerUArtifactSettings{
		SaveFullResult:          true,
		ResultSaveDir:           "/tmp/fake-mineru-results",
		CleanupTemporaryResults: true,
		CleanupPolicy:           MinerUCleanupPolicyAfterDays,
		CleanupAfterValue:       14,
		CleanupAfterUnit:        "day",
		CleanupAfterDays:        14,
		CleanupKeepBatches:      0,
		CleanupCron:             "0 3 * * *",
	}
	if err := repo.UpdateMinerUArtifactSettings(context.Background(), want); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	got, err = repo.GetMinerUArtifactSettings(context.Background())
	if err != nil {
		t.Fatalf("get updated settings: %v", err)
	}
	if got != want {
		t.Fatalf("round trip=%+v, want %+v", got, want)
	}
}

func TestSystemSettingsRepositoryDoesNotStoreCredentials(t *testing.T) {
	database := newSystemSettingsTestDB(t)
	repo := NewSystemSettingsRepository(database)
	if err := repo.UpdateMinerUArtifactSettings(context.Background(), MinerUArtifactSettings{
		SaveFullResult:          true,
		ResultSaveDir:           "/tmp/fake-results",
		CleanupTemporaryResults: true,
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM system_settings WHERE mineru_result_save_dir = ?`, "/tmp/fake-results").Scan(&count); err != nil {
		t.Fatalf("query settings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one artifact settings row, got %d", count)
	}
	var columns int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('system_settings') WHERE (name LIKE '%key%' OR name LIKE '%token%' OR name LIKE '%secret%') AND name NOT IN ('maxkb_user_key_ref', 'mineru_user_key_ref')`).Scan(&columns); err != nil {
		t.Fatalf("inspect settings schema: %v", err)
	}
	if columns != 0 {
		t.Fatalf("artifact settings schema unexpectedly added credential-like columns: %d", columns)
	}
}

func TestSystemSettingsRepositoryRecordsRecentCleanupResult(t *testing.T) {
	database := newSystemSettingsTestDB(t)
	repo := NewSystemSettingsRepository(database)
	at := time.Date(2026, time.August, 26, 3, 0, 0, 0, time.UTC)
	if err := repo.RecordMinerUArtifactCleanupResult(context.Background(), MinerUArtifactCleanupResult{
		At:           &at,
		Status:       "SUCCESS",
		DeletedCount: 7,
		Error:        "cleanup failed for /Users/example/private/file.pdf",
	}); err != nil {
		t.Fatalf("record cleanup result: %v", err)
	}
	got, err := repo.GetMinerUArtifactSettings(context.Background())
	if err != nil {
		t.Fatalf("get cleanup result: %v", err)
	}
	if got.LastCleanupAt == nil || !got.LastCleanupAt.Equal(at) {
		t.Fatalf("last cleanup at=%v, want %v", got.LastCleanupAt, at)
	}
	if got.LastCleanupStatus != "SUCCESS" || got.LastCleanupDeletedCount != 7 {
		t.Fatalf("cleanup summary=%+v", got)
	}
	if got.LastCleanupError == "" || got.LastCleanupError == "cleanup failed for /Users/example/private/file.pdf" {
		t.Fatalf("cleanup error was not sanitized: %q", got.LastCleanupError)
	}
}
