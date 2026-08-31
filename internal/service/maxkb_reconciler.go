package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/infra/logger"
	"maxkb-local-file-sync/internal/pkg/types"
	"maxkb-local-file-sync/internal/repository"
)

const defaultMaxKBReconcileInterval = 2 * time.Second

// maxKBReconcileStore is deliberately smaller than ReliabilityStore so the
// polling policy can be tested without coupling it to SQLite internals.
type maxKBReconcileStore interface {
	ListReconcileItems(ctx context.Context) ([]*repository.ReconcileItem, error)
	ResolveReconcile(ctx context.Context, runFileID, resolution, remoteDocumentID string) (string, error)
}

// MaxKBReconciler asynchronously checks durable MaxKB outcomes which were
// interrupted after a remote side effect but before the client could persist
// its final state. It only claims documents using a locally persisted document
// ID or source_file_id; it never adopts a document by name.
type MaxKBReconciler struct {
	store    maxKBReconcileStore
	folders  repository.SyncFolderRepository
	logger   *logger.Logger
	interval time.Duration

	mu       sync.Mutex
	adapter  adapter.MaxKBAdapter
	started  bool
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
}

func NewMaxKBReconciler(maxkb adapter.MaxKBAdapter, store maxKBReconcileStore, folders repository.SyncFolderRepository, log *logger.Logger) *MaxKBReconciler {
	return &MaxKBReconciler{
		adapter:  maxkb,
		store:    store,
		folders:  folders,
		logger:   log,
		interval: defaultMaxKBReconcileInterval,
	}
}

// SetAdapter makes configuration changes take effect without restarting the
// application. A nil adapter simply pauses remote reconciliation until a
// validated MaxKB configuration is available again.
func (r *MaxKBReconciler) SetAdapter(maxkb adapter.MaxKBAdapter) {
	r.mu.Lock()
	r.adapter = maxkb
	r.mu.Unlock()
}

// SetInterval is intended for tests and controlled embedders. Non-positive
// values restore the production default.
func (r *MaxKBReconciler) SetInterval(interval time.Duration) {
	if interval <= 0 {
		interval = defaultMaxKBReconcileInterval
	}
	r.mu.Lock()
	r.interval = interval
	r.mu.Unlock()
}

func (r *MaxKBReconciler) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return fmt.Errorf("MaxKB reconciler already running")
	}
	r.started = true
	r.stopChan = make(chan struct{})
	stopChan := r.stopChan
	r.wg.Add(1)
	r.mu.Unlock()

	go r.loop(ctx, stopChan)
	if r.logger != nil {
		r.logger.Info("MaxKB reconciliation service started")
	}
	return nil
}

func (r *MaxKBReconciler) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	close(r.stopChan)
	r.started = false
	done := make(chan struct{})
	r.mu.Unlock()

	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if r.logger != nil {
			r.logger.Info("MaxKB reconciliation service stopped")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *MaxKBReconciler) loop(ctx context.Context, stopChan <-chan struct{}) {
	defer r.wg.Done()
	// Reconcile immediately at startup so a completed MaxKB operation does not
	// wait for the first interval tick.
	r.RunNow(ctx)

	r.mu.Lock()
	interval := r.interval
	r.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RunNow(ctx)
		}
	}
}

// RunNow executes one serialized reconciliation pass. It is exposed for
// startup tests and for a future manual "立即对账" action.
func (r *MaxKBReconciler) RunNow(ctx context.Context) {
	if !r.beginRun() {
		return
	}
	defer r.endRun()

	r.mu.Lock()
	maxkb := r.adapter
	r.mu.Unlock()
	if maxkb == nil || r.store == nil || r.folders == nil {
		return
	}
	items, err := r.store.ListReconcileItems(ctx)
	if err != nil {
		r.logError("list MaxKB reconciliation items", err)
		return
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return
		}
		if item == nil || !isMaxKBReconcileStage(item.ProcessingStage) {
			continue
		}
		if err := r.reconcileItem(ctx, maxkb, item); err != nil {
			r.logError("reconcile MaxKB run file "+safeID(item.RunFileID), err)
		}
	}
}

func (r *MaxKBReconciler) beginRun() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

func (r *MaxKBReconciler) endRun() {
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
}

func isMaxKBReconcileStage(stage string) bool {
	return stage == string(types.ProcessingStageMaxKBCreating) || stage == string(types.ProcessingStageMaxKBProcessing) || stage == string(types.ProcessingStageMaxKBSplitting)
}

func (r *MaxKBReconciler) reconcileItem(ctx context.Context, maxkb adapter.MaxKBAdapter, item *repository.ReconcileItem) error {
	if strings.TrimSpace(item.MaxKBDocumentID) == "" && strings.TrimSpace(item.MaxKBSourceFileID) == "" {
		// There is no safe local identity with which to query or claim a remote
		// document. This remains an operator decision rather than a filename
		// lookup or blind batch_create retry.
		return nil
	}
	folder, err := r.folders.GetByID(ctx, item.FolderID)
	if err != nil {
		return fmt.Errorf("load folder: %w", err)
	}
	if folder == nil || strings.TrimSpace(folder.WorkspaceID) == "" || strings.TrimSpace(folder.KBId) == "" {
		return fmt.Errorf("folder binding is incomplete")
	}
	documents, err := maxkb.ListAllDocuments(ctx, folder.WorkspaceID, folder.KBId)
	if err != nil {
		return err
	}
	document, found, err := matchReconcileDocument(documents, item.MaxKBDocumentID, item.MaxKBSourceFileID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	switch document.StatusMapped {
	case adapter.MaxKBDocStatusSuccess:
		if _, err := r.store.ResolveReconcile(ctx, item.RunFileID, "REMOTE_SUCCEEDED", document.ID); err != nil {
			return fmt.Errorf("persist remote success: %w", err)
		}
		if r.logger != nil {
			r.logger.Info("MaxKB reconciliation confirmed remote success: run_file_id=%s, document_id=%s", safeID(item.RunFileID), safeID(document.ID))
		}
	case adapter.MaxKBDocStatusFailed:
		// A failed remote document is not automatically marked failed here: the
		// local operation may have crossed a delete/update boundary and the
		// existing explicit reconciliation action remains the safe authority.
		if r.logger != nil {
			r.logger.Warn("MaxKB reconciliation found remote failed document: run_file_id=%s, document_id=%s, status=%s", safeID(item.RunFileID), safeID(document.ID), document.Status)
		}
	case adapter.MaxKBDocStatusPending, adapter.MaxKBDocStatusProcessing, adapter.MaxKBDocStatusUnknown:
		// Keep RECONCILE_REQUIRED and observe again on the next pass.
	}
	return nil
}

// matchReconcileDocument first honors an already persisted document ID. If
// only a source_file_id exists, exactly one source match is required. A
// duplicate match is a conflict and is never auto-claimed.
func matchReconcileDocument(documents []*adapter.Document, documentID, sourceFileID string) (*adapter.Document, bool, error) {
	documentID = strings.TrimSpace(documentID)
	sourceFileID = strings.TrimSpace(sourceFileID)
	var byID *adapter.Document
	matches := make([]*adapter.Document, 0, 1)
	for _, document := range documents {
		if document == nil {
			continue
		}
		if documentID != "" && document.ID == documentID {
			byID = document
		}
		if sourceFileID != "" && document.SourceFileID == sourceFileID {
			matches = append(matches, document)
		}
	}
	if byID != nil {
		// If both identities are available and disagree, do not silently bind a
		// document from another source.
		if sourceFileID != "" && byID.SourceFileID != "" && byID.SourceFileID != sourceFileID {
			return nil, false, fmt.Errorf("MaxKB document identity conflict for local reconciliation")
		}
		return byID, true, nil
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	if len(matches) > 1 {
		return nil, false, fmt.Errorf("multiple MaxKB documents matched the local source_file_id")
	}
	return matches[0], true, nil
}

func (r *MaxKBReconciler) logError(operation string, err error) {
	if r.logger != nil {
		r.logger.Error("%s failed: %v", operation, err)
	}
}

func safeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	return value
}
