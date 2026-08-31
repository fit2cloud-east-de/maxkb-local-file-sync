package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maxkb-local-file-sync/internal/adapter"
	"maxkb-local-file-sync/internal/repository"
)

type smartSplitAdapterStub struct {
	adapter.MaxKBAdapter
	request *adapter.SmartSplitRequest
	body    []byte
	result  *adapter.SmartSplitResult
	err     error
}

func (s *smartSplitAdapterStub) SmartSplit(_ context.Context, request *adapter.SmartSplitRequest) (*adapter.SmartSplitResult, error) {
	s.request = request
	if request != nil && request.File != nil {
		s.body, _ = io.ReadAll(request.File)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func TestSmartSplitFromPathUsesSnapshotAndStableMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "snapshot.bin")
	content := []byte("name,description\ntest1,content\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	stub := &smartSplitAdapterStub{
		result: &adapter.SmartSplitResult{
			Name:         "data.csv",
			SourceFileID: "source-1",
			Paragraphs:   []adapter.Paragraph{{Title: "title", Content: "content"}},
		},
	}
	executor := &SyncExecutor{}
	result, err := executor.smartSplitFromPath(context.Background(), stub, "workspace-1", "knowledge-1", path, "data.csv", int64(len(content)))
	if err != nil {
		t.Fatalf("smartSplitFromPath() error = %v", err)
	}
	if result == nil || result.SourceFileID != "source-1" {
		t.Fatalf("smartSplitFromPath() result = %#v", result)
	}
	if stub.request == nil {
		t.Fatal("SmartSplit was not called")
	}
	if stub.request.WorkspaceID != "workspace-1" || stub.request.KnowledgeID != "knowledge-1" {
		t.Fatalf("request IDs = %q/%q", stub.request.WorkspaceID, stub.request.KnowledgeID)
	}
	if stub.request.FileName != "data.csv" || stub.request.FileSize != int64(len(content)) {
		t.Fatalf("request metadata = name %q size %d", stub.request.FileName, stub.request.FileSize)
	}
	if string(stub.body) != string(content) {
		t.Fatalf("request content = %q, want snapshot content", string(stub.body))
	}
}

func TestSmartSplitTimeoutRequiresReconciliation(t *testing.T) {
	t.Parallel()

	if !smartSplitRequiresReconcile(&adapter.MaxKBError{Type: adapter.MaxKBErrorTimeout}) {
		t.Fatal("timeout should require reconciliation")
	}
	if smartSplitRequiresReconcile(&adapter.MaxKBError{Type: adapter.MaxKBErrorIncompatible}) {
		t.Fatal("incompatible response should remain a normal failure")
	}
}

func TestSmartSplitFailureCodeClassifiesIncompatibleMaxKBResponse(t *testing.T) {
	t.Parallel()

	incompatible := &adapter.MaxKBError{
		Type:    adapter.MaxKBErrorIncompatible,
		Message: "MaxKB response data has an incompatible shape",
	}
	if got := smartSplitFailureCode(incompatible); got != "MAXKB_SPLIT_INCOMPATIBLE" {
		t.Fatalf("incompatible response code = %q", got)
	}

	business := &adapter.MaxKBError{
		Type:    adapter.MaxKBErrorBusiness,
		Message: "MaxKB rejected request",
	}
	if got := smartSplitFailureCode(business); got != "MAXKB_SPLIT_FAILED" {
		t.Fatalf("business response code = %q", got)
	}
	if got := smartSplitFailureCode(errors.New("network failure")); got != "MAXKB_SPLIT_FAILED" {
		t.Fatalf("generic error code = %q", got)
	}
}

type completedMinerUStub struct {
	adapter.MinerUAdapter
	result []byte
}

func (s *completedMinerUStub) QueryTaskStatus(context.Context, string) (*adapter.TaskStatusResponse, error) {
	return &adapter.TaskStatusResponse{Status: "completed"}, nil
}

func (s *completedMinerUStub) DownloadResult(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.result...), nil
}

func TestWaitMinerUKeepsDownloadedZIPOpaque(t *testing.T) {
	t.Parallel()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"first.md":         "# first\n",
		"nested/second.md": "# second\n",
		"images/chart.png": "fake-png",
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	executor := &SyncExecutor{artifactStore: NewMinerUArtifactStore()}
	attempt := &repository.FileAttempt{MinerUTaskID: "mineru-task-1"}
	folder := &repository.SyncFolder{Name: "Task", MinerUSaveFullResult: false}
	resultPath, resultRoot, err := executor.waitMinerU(
		context.Background(),
		&completedMinerUStub{result: archive.Bytes()},
		attempt,
		folder,
		"run-1",
		`docs\report.pdf`,
		time.Second,
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("waitMinerU() error = %v", err)
	}
	defer func() { _ = executor.artifactStore.CleanupTemporaryResult(resultRoot) }()

	if got := filepath.Base(resultPath); got != "report.zip" {
		t.Fatalf("result filename = %q, want report.zip", got)
	}
	got, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, archive.Bytes()) {
		t.Fatal("downloaded ZIP was modified before MaxKB upload")
	}
	if _, err := os.Stat(filepath.Join(resultRoot, "extracted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MinerU ZIP was unexpectedly extracted: %v", err)
	}
}

func TestWaitMinerURejectsEmptyResultZIP(t *testing.T) {
	t.Parallel()

	executor := &SyncExecutor{artifactStore: NewMinerUArtifactStore()}
	_, _, err := executor.waitMinerU(
		context.Background(),
		&completedMinerUStub{},
		&repository.FileAttempt{MinerUTaskID: "mineru-task-empty"},
		&repository.SyncFolder{Name: "Task"},
		"run-empty",
		"empty.docx",
		time.Second,
		time.Millisecond,
	)
	var resultErr *mineruResultError
	if !errors.As(err, &resultErr) {
		t.Fatalf("waitMinerU() error = %T %v, want mineruResultError", err, err)
	}
	if resultErr.code != "MINERU_RESULT_INVALID" {
		t.Fatalf("result error code = %q", resultErr.code)
	}
}

func TestMinerUResultArchiveNameIsSafeAndAlwaysZIP(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"docs/report.pdf":     "report.zip",
		`docs\quarterly.docx`: "quarterly.zip",
		"presentation.pptx":   "presentation.zip",
	}
	for input, want := range cases {
		if got := mineruResultArchiveName(input); got != want {
			t.Errorf("mineruResultArchiveName(%q) = %q, want %q", input, got, want)
		}
	}
	unsafe := mineruResultArchiveName("../unsafe:name?.pdf")
	if !strings.HasPrefix(unsafe, "unsafe_name_-") || !strings.HasSuffix(unsafe, ".zip") || strings.ContainsAny(unsafe, `/:?`) {
		t.Fatalf("unsafe archive name was not sanitized: %q", unsafe)
	}
}
