package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

type fakeReconcileMaxKB struct {
	adapter.MaxKBAdapter
	documents []*adapter.Document
	calls     atomic.Int32
	block     <-chan struct{}
}

func (f *fakeReconcileMaxKB) ListAllDocuments(ctx context.Context, workspaceID, knowledgeID string) ([]*adapter.Document, error) {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.documents, nil
}

type fakeReconcileStore struct {
	items    []*repository.ReconcileItem
	resolved []string
	mu       sync.Mutex
}

func (f *fakeReconcileStore) ListReconcileItems(context.Context) ([]*repository.ReconcileItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*repository.ReconcileItem(nil), f.items...), nil
}

func (f *fakeReconcileStore) ResolveReconcile(_ context.Context, runFileID, resolution, remoteDocumentID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, runFileID+"|"+resolution+"|"+remoteDocumentID)
	return remoteDocumentID, nil
}

func (f *fakeReconcileStore) resolvedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resolved...)
}

type fakeReconcileFolders struct {
	repository.SyncFolderRepository
	folder *repository.SyncFolder
	err    error
}

func (f *fakeReconcileFolders) GetByID(context.Context, string) (*repository.SyncFolder, error) {
	return f.folder, f.err
}

func TestMatchReconcileDocumentPrefersDocumentID(t *testing.T) {
	doc := &adapter.Document{ID: "doc-1", SourceFileID: "source-1"}
	got, found, err := matchReconcileDocument([]*adapter.Document{
		{ID: "doc-other", SourceFileID: "source-1"},
		doc,
	}, "doc-1", "source-1")
	if err != nil || !found || got != doc {
		t.Fatalf("got document=%v found=%v err=%v", got, found, err)
	}
}

func TestMatchReconcileDocumentRequiresUniqueSourceID(t *testing.T) {
	got, found, err := matchReconcileDocument([]*adapter.Document{
		{ID: "doc-1", SourceFileID: "source-1"},
		{ID: "doc-2", SourceFileID: "source-1"},
	}, "", "source-1")
	if err == nil || found || got != nil {
		t.Fatalf("expected source identity conflict, got document=%v found=%v err=%v", got, found, err)
	}
}

func TestMatchReconcileDocumentDetectsIdentityConflict(t *testing.T) {
	got, found, err := matchReconcileDocument([]*adapter.Document{
		{ID: "doc-1", SourceFileID: "source-other"},
	}, "doc-1", "source-1")
	if err == nil || found || got != nil {
		t.Fatalf("expected document/source identity conflict, got document=%v found=%v err=%v", got, found, err)
	}
}

func TestMaxKBReconcilerConfirmsAggregateSuccess(t *testing.T) {
	store := &fakeReconcileStore{items: []*repository.ReconcileItem{{
		RunFileID:         "run-file-1",
		FolderID:          "folder-1",
		ProcessingStage:   string(types.ProcessingStageMaxKBProcessing),
		MaxKBSourceFileID: "source-1",
		MaxKBDocumentID:   "",
	}}}
	folders := &fakeReconcileFolders{folder: &repository.SyncFolder{WorkspaceID: "workspace-1", KBId: "knowledge-1"}}
	maxkb := &fakeReconcileMaxKB{documents: []*adapter.Document{{
		ID:           "doc-1",
		SourceFileID: "source-1",
		Status:       "nnnn",
		StatusMapped: adapter.MaxKBDocStatusSuccess,
	}}}
	reconciler := NewMaxKBReconciler(maxkb, store, folders, nil)

	reconciler.RunNow(context.Background())

	want := []string{"run-file-1|REMOTE_SUCCEEDED|doc-1"}
	if got := store.resolvedCalls(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("resolved calls = %v, want %v", got, want)
	}
}

func TestMaxKBReconcilerWaitsForUnknownOrMissingRemoteDocument(t *testing.T) {
	tests := []struct {
		name      string
		documents []*adapter.Document
	}{
		{name: "missing"},
		{name: "pending", documents: []*adapter.Document{{ID: "doc-1", SourceFileID: "source-1", StatusMapped: adapter.MaxKBDocStatusPending}}},
		{name: "unknown", documents: []*adapter.Document{{ID: "doc-1", SourceFileID: "source-1", StatusMapped: adapter.MaxKBDocStatusUnknown}}},
		{name: "failed", documents: []*adapter.Document{{ID: "doc-1", SourceFileID: "source-1", StatusMapped: adapter.MaxKBDocStatusFailed}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeReconcileStore{items: []*repository.ReconcileItem{{
				RunFileID:         "run-file-1",
				FolderID:          "folder-1",
				ProcessingStage:   string(types.ProcessingStageMaxKBCreating),
				MaxKBSourceFileID: "source-1",
			}}}
			folders := &fakeReconcileFolders{folder: &repository.SyncFolder{WorkspaceID: "workspace-1", KBId: "knowledge-1"}}
			reconciler := NewMaxKBReconciler(&fakeReconcileMaxKB{documents: tt.documents}, store, folders, nil)

			reconciler.RunNow(context.Background())

			if got := store.resolvedCalls(); len(got) != 0 {
				t.Fatalf("resolved calls = %v, want none", got)
			}
		})
	}
}

func TestMaxKBReconcilerSkipsUnsafeItems(t *testing.T) {
	store := &fakeReconcileStore{items: []*repository.ReconcileItem{
		{RunFileID: "no-identity", FolderID: "folder-1", ProcessingStage: string(types.ProcessingStageMaxKBCreating)},
		{RunFileID: "wrong-stage", FolderID: "folder-1", ProcessingStage: string(types.ProcessingStageMinerURunning), MaxKBSourceFileID: "source-1"},
	}}
	maxkb := &fakeReconcileMaxKB{documents: []*adapter.Document{{ID: "doc-1", SourceFileID: "source-1", StatusMapped: adapter.MaxKBDocStatusSuccess}}}
	reconciler := NewMaxKBReconciler(maxkb, store, &fakeReconcileFolders{folder: &repository.SyncFolder{WorkspaceID: "workspace-1", KBId: "knowledge-1"}}, nil)

	reconciler.RunNow(context.Background())

	if got := maxkb.calls.Load(); got != 0 {
		t.Fatalf("ListAllDocuments calls = %d, want 0", got)
	}
}

func TestMaxKBReconcilerRunNowIsSerialized(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	maxkb := &fakeReconcileMaxKB{
		documents: []*adapter.Document{{ID: "doc-1", SourceFileID: "source-1", StatusMapped: adapter.MaxKBDocStatusPending}},
		block:     release,
	}
	store := &fakeReconcileStore{items: []*repository.ReconcileItem{{
		RunFileID: "run-file-1", FolderID: "folder-1", ProcessingStage: string(types.ProcessingStageMaxKBProcessing), MaxKBSourceFileID: "source-1",
	}}}
	reconciler := NewMaxKBReconciler(maxkb, store, &fakeReconcileFolders{folder: &repository.SyncFolder{WorkspaceID: "workspace-1", KBId: "knowledge-1"}}, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		reconciler.RunNow(context.Background())
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	for maxkb.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if maxkb.calls.Load() != 1 {
		t.Fatalf("first reconciliation did not start: calls=%d", maxkb.calls.Load())
	}

	reconciler.RunNow(context.Background())
	if got := maxkb.calls.Load(); got != 1 {
		t.Fatalf("concurrent RunNow entered adapter: calls=%d, want 1", got)
	}
	close(release)
	wg.Wait()
}

func TestMaxKBReconcilerNilDependenciesAreNoop(t *testing.T) {
	for name, reconciler := range map[string]*MaxKBReconciler{
		"nil adapter": NewMaxKBReconciler(nil, &fakeReconcileStore{}, &fakeReconcileFolders{}, nil),
		"nil store":   NewMaxKBReconciler(&fakeReconcileMaxKB{}, nil, &fakeReconcileFolders{}, nil),
		"nil folders": NewMaxKBReconciler(&fakeReconcileMaxKB{}, &fakeReconcileStore{}, nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			reconciler.RunNow(context.Background())
		})
	}
}

func TestMaxKBReconcilerPropagatesFolderErrorWithoutResolving(t *testing.T) {
	store := &fakeReconcileStore{items: []*repository.ReconcileItem{{
		RunFileID: "run-file-1", FolderID: "folder-1", ProcessingStage: string(types.ProcessingStageMaxKBProcessing), MaxKBSourceFileID: "source-1",
	}}}
	maxkb := &fakeReconcileMaxKB{}
	reconciler := NewMaxKBReconciler(maxkb, store, &fakeReconcileFolders{err: errors.New("folder lookup failed")}, nil)

	reconciler.RunNow(context.Background())

	if got := maxkb.calls.Load(); got != 0 {
		t.Fatalf("ListAllDocuments calls = %d, want 0", got)
	}
	if got := store.resolvedCalls(); len(got) != 0 {
		t.Fatalf("resolved calls = %v, want none", got)
	}
}
