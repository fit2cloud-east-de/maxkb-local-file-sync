package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/repository"
)

func newArtifactCleanupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "cleanup.db"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.InitSchema(); err != nil {
		_ = database.Close()
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func insertArtifactCleanupFolder(t *testing.T, database *db.DB, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	localPath := filepath.Join(t.TempDir(), name)
	_, err := database.Exec(`
		INSERT INTO sync_folders(folder_id,name,local_path,normalized_local_path,kb_id,workspace_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)`, id, name, localPath, localPath, "kb-"+id, "ws-"+id, now, now)
	if err != nil {
		t.Fatalf("insert folder %s: %v", id, err)
	}
}

func insertArtifactCleanupRun(t *testing.T, database *db.DB, id, folderID, status string, queuedAt time.Time) {
	t.Helper()
	taskID := "task-" + id
	now := queuedAt.UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec(`
		INSERT INTO sync_tasks(task_id,folder_id,kb_id,workspace_id,trigger_type,run_status,processing_stage,control_state,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, taskID, folderID, "kb-"+folderID, "ws-"+folderID, "manual", status, "TEST", "ACTIVE", now); err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
	_, err := database.Exec(`
		INSERT INTO sync_runs(id,task_id,folder_id,trigger_type,status,queued_at)
		VALUES(?,?,?,?,?,?)`, id, taskID, folderID, "manual", status, now)
	if err != nil {
		t.Fatalf("insert run %s: %v", id, err)
	}
}

func writeArtifactBatch(t *testing.T, root, taskName, runID string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(root, safePathComponent(taskName, "task name"), runID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create artifact batch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "result.zip"), []byte("fake zip"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("set artifact mtime: %v", err)
	}
	return path
}

func configureArtifactCleanup(t *testing.T, repo repository.SystemSettingsRepository, root, policy string, keepBatches, afterValue int, afterUnit string) {
	t.Helper()
	if err := repo.UpdateMinerUArtifactSettings(context.Background(), repository.MinerUArtifactSettings{
		ResultSaveDir:      root,
		CleanupPolicy:      policy,
		CleanupKeepBatches: keepBatches,
		CleanupAfterValue:  afterValue,
		CleanupAfterUnit:   afterUnit,
		CleanupCron:        "0 3 * * *",
	}); err != nil {
		t.Fatalf("configure cleanup: %v", err)
	}
}

func TestMinerUArtifactCleanupKeepBatchesIsScopedPerTask(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Task A")
	insertArtifactCleanupFolder(t, database, "folder-b", "Task B")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-a-old", "folder-a", "SUCCESS", now.Add(-3*time.Hour))
	insertArtifactCleanupRun(t, database, "run-a-new", "folder-a", "SUCCESS", now.Add(-2*time.Hour))
	insertArtifactCleanupRun(t, database, "run-b-only", "folder-b", "SUCCESS", now.Add(-time.Hour))
	oldA := writeArtifactBatch(t, root, "Task A", "run-a-old", now.Add(-3*time.Hour))
	newA := writeArtifactBatch(t, root, "Task A", "run-a-new", now.Add(-2*time.Hour))
	onlyB := writeArtifactBatch(t, root, "Task B", "run-b-only", now.Add(-time.Hour))
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyKeepBatches, 1, 0, "day")

	result, err := NewMinerUArtifactCleanupService(database, settingsRepo, nil).RunNow(context.Background())
	if err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if result.DeletedCount != 1 || result.SkippedCount != 2 {
		t.Fatalf("cleanup result = %+v, want one delete and two skips", result)
	}
	if _, err := os.Stat(oldA); !os.IsNotExist(err) {
		t.Fatalf("old batch still exists, stat error = %v", err)
	}
	for _, retained := range []string{newA, onlyB} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained batch %s missing: %v", retained, err)
		}
	}
}

func TestMinerUArtifactCleanupAfterDurationAndProtectedRuns(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Task A")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-old", "folder-a", "SUCCESS", now.Add(-4*time.Hour))
	insertArtifactCleanupRun(t, database, "run-new", "folder-a", "SUCCESS", now.Add(-30*time.Minute))
	insertArtifactCleanupRun(t, database, "run-active", "folder-a", "RUNNING", now.Add(-5*time.Hour))
	old := writeArtifactBatch(t, root, "Task A", "run-old", now.Add(-4*time.Hour))
	fresh := writeArtifactBatch(t, root, "Task A", "run-new", now.Add(-30*time.Minute))
	active := writeArtifactBatch(t, root, "Task A", "run-active", now.Add(-5*time.Hour))
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyAfterDuration, 0, 1, "hour")

	result, err := NewMinerUArtifactCleanupService(database, settingsRepo, nil).RunNow(context.Background())
	if err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if result.DeletedCount != 1 || result.SkippedCount != 2 {
		t.Fatalf("cleanup result = %+v, want one delete and two skips", result)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired batch still exists, stat error = %v", err)
	}
	for _, retained := range []string{fresh, active} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained batch %s missing: %v", retained, err)
		}
	}
}

func TestMinerUArtifactCleanupManualNeverPolicyRemovesCompletedBatches(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Task A")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-complete", "folder-a", "SUCCESS", now)
	insertArtifactCleanupRun(t, database, "run-paused", "folder-a", "PAUSED", now.Add(-time.Hour))
	completed := writeArtifactBatch(t, root, "Task A", "run-complete", now)
	paused := writeArtifactBatch(t, root, "Task A", "run-paused", now.Add(-time.Hour))
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyNever, 0, 0, "day")

	result, err := NewMinerUArtifactCleanupService(database, settingsRepo, nil).RunNow(context.Background())
	if err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if result.DeletedCount != 1 || result.SkippedCount != 1 {
		t.Fatalf("cleanup result = %+v, want one delete and one skip", result)
	}
	if _, err := os.Stat(completed); !os.IsNotExist(err) {
		t.Fatalf("completed batch still exists, stat error = %v", err)
	}
	if _, err := os.Stat(paused); err != nil {
		t.Fatalf("paused batch should be protected: %v", err)
	}
}

func TestDeleteFolderArtifactsKeepsSameNameTaskIsolated(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Shared Name")
	insertArtifactCleanupFolder(t, database, "folder-b", "Shared Name")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-a", "folder-a", "SUCCESS", now)
	insertArtifactCleanupRun(t, database, "run-b", "folder-b", "SUCCESS", now)
	batchA := writeArtifactBatch(t, root, "Shared Name", "run-a", now)
	batchB := writeArtifactBatch(t, root, "Shared Name", "run-b", now)
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyNever, 0, 0, "day")

	service := NewMinerUArtifactCleanupService(database, settingsRepo, nil)
	if err := service.DeleteFolderArtifacts(context.Background(), "folder-a"); err != nil {
		t.Fatalf("DeleteFolderArtifacts() error = %v", err)
	}
	if _, err := os.Stat(batchA); !os.IsNotExist(err) {
		t.Fatalf("deleted task batch still exists, stat error = %v", err)
	}
	if _, err := os.Stat(batchB); err != nil {
		t.Fatalf("same-name task batch was removed: %v", err)
	}
	var folderID string
	if err := database.QueryRow(`SELECT folder_id FROM sync_folders WHERE folder_id = ?`, "folder-a").Scan(&folderID); err != nil || folderID != "folder-a" {
		t.Fatalf("artifact cleanup changed the sync folder record: id=%q error=%v", folderID, err)
	}
}

func TestDeleteFolderArtifactsFindsBatchUnderPreviousTaskName(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Current Name")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-a", "folder-a", "SUCCESS", now)
	batch := writeArtifactBatch(t, root, "Previous Name", "run-a", now)
	if err := os.WriteFile(filepath.Join(filepath.Dir(batch), ".DS_Store"), []byte("finder metadata"), 0o600); err != nil {
		t.Fatalf("write task directory metadata: %v", err)
	}
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyNever, 0, 0, "day")

	service := NewMinerUArtifactCleanupService(database, settingsRepo, nil)
	if err := service.DeleteFolderArtifacts(context.Background(), "folder-a"); err != nil {
		t.Fatalf("DeleteFolderArtifacts() error = %v", err)
	}
	if _, err := os.Stat(batch); !os.IsNotExist(err) {
		t.Fatalf("batch under the previous task name still exists, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(batch)); !os.IsNotExist(err) {
		t.Fatalf("task artifact directory containing only system metadata still exists, stat error = %v", err)
	}
}

func TestDeleteFolderArtifactsKeepsTaskDirectoryWithUnknownFile(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := t.TempDir()
	insertArtifactCleanupFolder(t, database, "folder-a", "Task A")
	now := time.Now().UTC()
	insertArtifactCleanupRun(t, database, "run-a", "folder-a", "SUCCESS", now)
	batch := writeArtifactBatch(t, root, "Task A", "run-a", now)
	taskDirectory := filepath.Dir(batch)
	retainedFile := filepath.Join(taskDirectory, "keep.txt")
	if err := os.WriteFile(retainedFile, []byte("not managed by cleanup"), 0o600); err != nil {
		t.Fatalf("write retained file: %v", err)
	}
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyNever, 0, 0, "day")

	service := NewMinerUArtifactCleanupService(database, settingsRepo, nil)
	if err := service.DeleteFolderArtifacts(context.Background(), "folder-a"); err != nil {
		t.Fatalf("DeleteFolderArtifacts() error = %v", err)
	}
	if _, err := os.Stat(batch); !os.IsNotExist(err) {
		t.Fatalf("task batch still exists, stat error = %v", err)
	}
	if _, err := os.Stat(retainedFile); err != nil {
		t.Fatalf("unknown task directory file was removed: %v", err)
	}
}

func TestDeleteFolderArtifactsAllowsMissingSaveDirectory(t *testing.T) {
	database := newArtifactCleanupTestDB(t)
	settingsRepo := repository.NewSystemSettingsRepository(database)
	root := filepath.Join(t.TempDir(), "not-created")
	configureArtifactCleanup(t, settingsRepo, root, repository.MinerUCleanupPolicyNever, 0, 0, "day")

	service := NewMinerUArtifactCleanupService(database, settingsRepo, nil)
	if err := service.DeleteFolderArtifacts(context.Background(), "folder-a"); err != nil {
		t.Fatalf("DeleteFolderArtifacts() missing directory error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cleanup unexpectedly created the missing save directory: %v", err)
	}
}
