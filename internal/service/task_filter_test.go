package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

func TestCreateTaskReappliesCurrentExcludeToHistoricalPendingFile(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, database := retryTaskFixture(t)

	if _, err := database.Exec(`UPDATE sync_folders SET cron_expression=?, exclude_patterns=? WHERE folder_id=?`, "", "管理员", "folder-retry"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sync_files SET relative_path=?, observed_md5=?, last_success_md5=?, remote_doc_id=? WHERE file_id=?`, "JumpServer v4-LTS-管理员手册.pdf", "", "", "", "file-retry"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.CreateTask(ctx, "folder-retry", types.TriggerTypeManual); !errors.Is(err, ErrNoPendingChanges) {
		t.Fatalf("CreateTask() error = %v, want ErrNoPendingChanges", err)
	}

	var runs, runFiles, queue int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sync_tasks`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM run_files`).Scan(&runFiles); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM job_queue`).Scan(&queue); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || runFiles != 0 || queue != 0 {
		t.Fatalf("excluded file created durable work: sync_tasks=%d run_files=%d job_queue=%d", runs, runFiles, queue)
	}
}

func TestFilterPendingFilesKeepsRemoteDeletionOutsideCurrentFilter(t *testing.T) {
	folder := &repository.SyncFolder{
		IncludePatterns: "",
		ExcludePatterns: "管理员",
	}
	pending := []*repository.SyncFile{
		{FileID: "excluded-pending", RelativePath: "JumpServer v4-LTS-管理员手册.pdf", FileStatus: types.FileStatusPending},
		{FileID: "remote-delete", RelativePath: "JumpServer v4-LTS-管理员手册.pdf", FileStatus: types.FileStatusNeedsDelete},
	}

	filtered, err := filterPendingFilesByCurrentFolderConfig(folder, pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].FileID != "remote-delete" {
		t.Fatalf("filtered pending files = %+v, want only remote deletion", filtered)
	}
}

func TestFilterPendingFilesRejectsInvalidCurrentExclude(t *testing.T) {
	folder := &repository.SyncFolder{ExcludePatterns: "[invalid"}
	_, err := filterPendingFilesByCurrentFolderConfig(folder, nil)
	if err == nil {
		t.Fatal("filterPendingFilesByCurrentFolderConfig() error = nil, want invalid exclude error")
	}
	if !strings.Contains(err.Error(), "invalid exclude pattern") {
		t.Fatalf("error = %v, want invalid exclude pattern", err)
	}
}
