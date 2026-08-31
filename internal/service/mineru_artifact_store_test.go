package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maxkb-local-file-sync/internal/repository"
)

func TestMinerUArtifactStorePersistsResultZIPByTaskAndBatch(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	archiveContent := []byte("fake-mineru-zip-content")
	resultZIP := filepath.Join(sourceRoot, "report.zip")
	if err := os.WriteFile(resultZIP, archiveContent, 0o600); err != nil {
		t.Fatal(err)
	}
	saveRoot := t.TempDir()
	folder := &repository.SyncFolder{
		Name:                 "Finance / Q4",
		MinerUSaveFullResult: true,
		MinerUResultSaveDir:  saveRoot,
	}
	store := NewMinerUArtifactStore()

	published, err := store.Persist(context.Background(), folder, "run-001", "docs/report.pdf", sourceRoot, resultZIP)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if published == resultZIP {
		t.Fatal("Persist() returned the temporary path while ZIP retention was enabled")
	}
	if filepath.Base(published) != "report.zip" {
		t.Fatalf("published archive name = %q", filepath.Base(published))
	}
	if got, err := os.ReadFile(published); err != nil || string(got) != string(archiveContent) {
		t.Fatalf("published ZIP = %q, error = %v", got, err)
	}
	if !strings.Contains(published, safePathComponent(folder.Name, "task name")) || !strings.Contains(published, "run-001") {
		t.Fatalf("published path does not contain task/batch isolation: %s", published)
	}

	publishedSecond, err := store.Persist(context.Background(), folder, "run-002", "docs/report.pdf", sourceRoot, resultZIP)
	if err != nil {
		t.Fatalf("Persist() second batch error = %v", err)
	}
	if filepath.Dir(filepath.Dir(published)) == filepath.Dir(filepath.Dir(publishedSecond)) {
		t.Fatalf("different batches unexpectedly share an artifact directory: %s / %s", published, publishedSecond)
	}
	if _, err := os.Stat(published); err != nil {
		t.Fatalf("first batch artifact was overwritten or removed: %v", err)
	}
}

func TestMinerUArtifactStoreDisabledLeavesTemporaryResultUntouched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	processed := filepath.Join(root, "result.zip")
	if err := os.WriteFile(processed, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := &repository.SyncFolder{MinerUSaveFullResult: false, MinerUResultSaveDir: "not-used"}
	published, err := NewMinerUArtifactStore().Persist(context.Background(), folder, "run", "file.pdf", root, processed)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if published != processed {
		t.Fatalf("disabled Persist() path = %q, want %q", published, processed)
	}
}

func TestMinerUArtifactStoreRejectsUnsafeSourcePathsAndSaveRoots(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	processed := filepath.Join(sourceRoot, "result.zip")
	if err := os.WriteFile(processed, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewMinerUArtifactStore()
	cases := []string{"../escape.md", `..\\escape.md`, `/absolute/file.pdf`, `C:\\absolute\\file.pdf`, `\\\\server\\share\\file.pdf`}
	for _, source := range cases {
		t.Run(source, func(t *testing.T) {
			folder := &repository.SyncFolder{Name: "Task", MinerUSaveFullResult: true, MinerUResultSaveDir: t.TempDir()}
			if _, err := store.Persist(context.Background(), folder, "run", source, sourceRoot, processed); err == nil {
				t.Fatalf("Persist(%q) unexpectedly succeeded", source)
			}
		})
	}

	folder := &repository.SyncFolder{Name: "Task", MinerUSaveFullResult: true, MinerUResultSaveDir: "relative/results"}
	if _, err := store.Persist(context.Background(), folder, "run", "file.pdf", sourceRoot, processed); err == nil {
		t.Fatal("relative save root unexpectedly accepted")
	}
}

func TestMinerUArtifactStoreCleanupTemporaryResultIsScoped(t *testing.T) {
	t.Parallel()

	store := NewMinerUArtifactStore()
	resultRoot, err := os.MkdirTemp("", mineruTempResultPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultRoot, "result.zip"), []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupTemporaryResult(resultRoot); err != nil {
		t.Fatalf("CleanupTemporaryResult() error = %v", err)
	}
	if _, err := os.Stat(resultRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary result still exists, stat error = %v", err)
	}

	protected := t.TempDir()
	if err := store.CleanupTemporaryResult(protected); err == nil {
		t.Fatal("CleanupTemporaryResult() accepted an unknown directory")
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("protected directory changed: %v", err)
	}
}

func TestMinerUArtifactStoreRejectsSymlinkArtifactEntries(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	processed := filepath.Join(sourceRoot, "result.zip")
	if err := os.WriteFile(processed, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(processed, filepath.Join(sourceRoot, "link.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	folder := &repository.SyncFolder{Name: "Task", MinerUSaveFullResult: true, MinerUResultSaveDir: t.TempDir()}
	if _, err := NewMinerUArtifactStore().Persist(context.Background(), folder, "run", "file.pdf", sourceRoot, processed); err == nil {
		t.Fatal("Persist() accepted a symlink artifact entry")
	}
}
