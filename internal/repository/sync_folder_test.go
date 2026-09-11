package repository

import (
	"context"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/infra/db"
)

func newSyncFolderRepositoryFixture(t *testing.T) (context.Context, *db.DB, SyncFolderRepository) {
	t.Helper()
	database, err := db.New(db.Config{DataDir: t.TempDir(), DBName: "sync-folder.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.InitSchema(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return context.Background(), database, NewSyncFolderRepository(database)
}

func TestSyncFolderRepositoryIgnoresLegacyTaskMinerUFields(t *testing.T) {
	ctx, database, repo := newSyncFolderRepositoryFixture(t)
	now := time.Now().UTC()
	folder := &SyncFolder{
		FolderID:             "folder-legacy-mineru",
		Name:                 "Legacy fields",
		LocalPath:            "/tmp/legacy-fields",
		KBId:                 "kb-legacy",
		WorkspaceID:          "ws-legacy",
		MaxKBBaseURLSnapshot: "https://maxkb.example.test",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := repo.Create(ctx, folder); err != nil {
		t.Fatal(err)
	}

	var legacyMode, legacyEndpoint interface{}
	if err := database.QueryRow(`SELECT mineru_mode, mineru_endpoint FROM sync_folders WHERE folder_id = ?`, folder.FolderID).Scan(&legacyMode, &legacyEndpoint); err != nil {
		t.Fatal(err)
	}
	if legacyMode != nil || legacyEndpoint != nil {
		t.Fatalf("new repository writes must not populate legacy task MinerU fields: mode=%v endpoint=%v", legacyMode, legacyEndpoint)
	}

	if _, err := database.Exec(`UPDATE sync_folders SET mineru_mode = ?, mineru_endpoint = ? WHERE folder_id = ?`, "legacy-online", "https://legacy-mineru.example.test", folder.FolderID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, folder.FolderID); err != nil {
		t.Fatalf("reading a database row with legacy task MinerU fields must remain compatible: %v", err)
	}

	folder.Name = "Updated"
	if err := repo.Update(ctx, folder); err != nil {
		t.Fatal(err)
	}
	var mode, endpoint string
	if err := database.QueryRow(`SELECT mineru_mode, mineru_endpoint FROM sync_folders WHERE folder_id = ?`, folder.FolderID).Scan(&mode, &endpoint); err != nil {
		t.Fatal(err)
	}
	if mode != "legacy-online" || endpoint != "https://legacy-mineru.example.test" {
		t.Fatalf("legacy task MinerU fields should remain inert and untouched during updates: mode=%q endpoint=%q", mode, endpoint)
	}
}

func TestSyncFolderRepositoryPersistsMaxKBSnapshotsAndNormalizesIdentity(t *testing.T) {
	ctx, database, repo := newSyncFolderRepositoryFixture(t)
	now := time.Date(2026, time.August, 26, 10, 20, 30, 123000000, time.UTC)
	folder := &SyncFolder{
		FolderID:             "folder-snapshot",
		Name:                 "Docs",
		LocalPath:            `C:\Users\alice\Docs`,
		KBId:                 "kb-1",
		WorkspaceID:          "ws-1",
		MaxKBBaseURLSnapshot: "https://maxkb.example.test///",
		WorkspaceName:        "Workspace",
		KnowledgeFolderID:    "folder-root",
		KnowledgeName:        "Knowledge",
		EnableMinerU:         true,
		CronExpression:       "0 * * * *",
		CronEnabled:          true,
		Enabled:              true,
		MinerURetryCount:     3,
		MinerURequestTimeout: 30000,
		MinerUTaskTimeout:    3600000,
		MinerUPollInterval:   2000,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := repo.Create(ctx, folder); err != nil {
		t.Fatal(err)
	}
	if folder.NormalizedMaxKBBaseURL != "https://maxkb.example.test" {
		t.Fatalf("normalized base URL = %q", folder.NormalizedMaxKBBaseURL)
	}

	got, err := repo.GetByID(ctx, folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxKBBaseURLSnapshot != folder.MaxKBBaseURLSnapshot {
		t.Fatalf("base URL snapshot = %q, want %q", got.MaxKBBaseURLSnapshot, folder.MaxKBBaseURLSnapshot)
	}
	if got.NormalizedMaxKBBaseURL != "https://maxkb.example.test" {
		t.Fatalf("normalized base URL = %q", got.NormalizedMaxKBBaseURL)
	}
	if got.WorkspaceName != "Workspace" || got.KnowledgeFolderID != "folder-root" || got.KnowledgeName != "Knowledge" {
		t.Fatalf("binding snapshot not persisted: %#v", got)
	}

	var normalizedPath string
	if err := database.QueryRow(`SELECT normalized_local_path FROM sync_folders WHERE folder_id = ?`, folder.FolderID).Scan(&normalizedPath); err != nil {
		t.Fatal(err)
	}
	if normalizedPath != "C:/Users/alice/Docs" {
		t.Fatalf("normalized local path = %q", normalizedPath)
	}

}

func TestSyncFolderRepositoryUpdateKeepsBindingSnapshots(t *testing.T) {
	ctx, _, repo := newSyncFolderRepositoryFixture(t)
	now := time.Now().UTC()
	folder := &SyncFolder{
		FolderID:             "folder-update",
		Name:                 "Before",
		LocalPath:            "/tmp/before",
		KBId:                 "kb-before",
		WorkspaceID:          "ws-before",
		MaxKBBaseURLSnapshot: "https://old.example.test/",
		WorkspaceName:        "Old Workspace",
		KnowledgeFolderID:    "old-folder",
		KnowledgeName:        "Old Knowledge",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := repo.Create(ctx, folder); err != nil {
		t.Fatal(err)
	}

	folder.Name = "After"
	folder.LocalPath = "/tmp/after"
	folder.KBId = "kb-after"
	folder.WorkspaceID = "ws-after"
	folder.MaxKBBaseURLSnapshot = "https://new.example.test///"
	folder.WorkspaceName = "New Workspace"
	folder.KnowledgeFolderID = "new-folder"
	folder.KnowledgeName = "New Knowledge"
	if err := repo.Update(ctx, folder); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, folder.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "After" || got.LocalPath != "/tmp/after" || got.KBId != "kb-after" || got.WorkspaceID != "ws-after" {
		t.Fatalf("updated identity fields not persisted: %#v", got)
	}
	if got.MaxKBBaseURLSnapshot != "https://new.example.test///" || got.NormalizedMaxKBBaseURL != "https://new.example.test" {
		t.Fatalf("updated URL snapshot not persisted: %#v", got)
	}
	if got.WorkspaceName != "New Workspace" || got.KnowledgeFolderID != "new-folder" || got.KnowledgeName != "New Knowledge" {
		t.Fatalf("updated binding snapshot not persisted: %#v", got)
	}
}

func TestSyncFolderRepositoryAllowsSharedLocalPathAndRemoteTarget(t *testing.T) {
	ctx, _, repo := newSyncFolderRepositoryFixture(t)
	now := time.Now().UTC()
	for _, folder := range []*SyncFolder{
		{
			FolderID: "folder-a", Name: "任务 A", LocalPath: "/tmp/shared-folder",
			KBId: "kb-1", WorkspaceID: "ws-1", MaxKBBaseURLSnapshot: "https://maxkb.example.test",
			CreatedAt: now, UpdatedAt: now,
		},
		{
			FolderID: "folder-b", Name: "任务 B", LocalPath: "/tmp/shared-folder",
			KBId: "kb-1", WorkspaceID: "ws-1", MaxKBBaseURLSnapshot: "https://maxkb.example.test/",
			CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := repo.Create(ctx, folder); err != nil {
			t.Fatalf("create %s: %v", folder.FolderID, err)
		}
	}
}
