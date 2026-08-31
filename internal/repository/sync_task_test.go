package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
	"maxkb-local-file-sync/internal/pkg/types"
)

func TestSyncTaskRepositoryListByFolderEmptyListsAllTasks(t *testing.T) {
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "sync-task.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for i, folderID := range []string{"folder-1", "folder-2"} {
		localPath := t.TempDir()
		kbID := fmt.Sprintf("kb-%d", i+1)
		_, err := database.Exec(`INSERT INTO sync_folders(folder_id,name,local_path,kb_id,workspace_id,created_at,updated_at,normalized_local_path,normalized_maxkb_base_url) VALUES(?,?,?,?,?,?,?,?,?)`,
			folderID, folderID, localPath, kbID, "workspace-1", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), localPath, fmt.Sprintf("https://maxkb-%d.example", i+1))
		if err != nil {
			t.Fatal(err)
		}
	}

	repo := NewSyncTaskRepository(database)
	for i, folderID := range []string{"folder-1", "folder-2"} {
		task := &SyncTask{
			TaskID:          fmt.Sprintf("task-%d", i+1),
			FolderID:        folderID,
			KBId:            fmt.Sprintf("kb-%d", i+1),
			WorkspaceID:     "workspace-1",
			TriggerType:     types.TriggerTypeManual,
			RunStatus:       types.RunStatusQueued,
			ProcessingStage: types.ProcessingStageInit,
			ControlState:    types.ControlStateActive,
			CreatedAt:       now.Add(time.Duration(i) * time.Second),
		}
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}

	all, err := repo.ListByFolder(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("empty folder filter returned %d tasks, want 2", len(all))
	}

	filtered, err := repo.ListByFolder(context.Background(), "folder-1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].FolderID != "folder-1" {
		t.Fatalf("folder filter returned %#v, want one task from folder-1", filtered)
	}
}
