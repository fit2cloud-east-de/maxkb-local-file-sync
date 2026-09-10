package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
	for _, tc := range []struct {
		name string
		err  *adapter.MaxKBError
	}{
		{name: "unreachable", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorUnreachable}},
		{name: "tls", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorTLS}},
		{name: "incompatible response", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorIncompatible, StatusCode: 200}},
		{name: "rate limited", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorBusiness, StatusCode: 429}},
		{name: "gateway failure", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorBusiness, StatusCode: 502}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !smartSplitRequiresReconcile(tc.err) {
				t.Fatalf("%s should require reconciliation", tc.name)
			}
		})
	}
	if smartSplitRequiresReconcile(&adapter.MaxKBError{Type: adapter.MaxKBErrorIncompatible}) {
		t.Fatal("incompatible response without an HTTP result should remain a normal failure")
	}
	for _, tc := range []struct {
		name string
		err  *adapter.MaxKBError
	}{
		{name: "authentication", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorInvalidAPIKey, StatusCode: 401}},
		{name: "permission", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorPermissionDenied, StatusCode: 403}},
		{name: "validation", err: &adapter.MaxKBError{Type: adapter.MaxKBErrorBusiness, StatusCode: 400}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if smartSplitRequiresReconcile(tc.err) {
				t.Fatalf("%s should remain a confirmed failure", tc.name)
			}
		})
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

func TestWaitMinerUNormalizesDownloadedZIPForMaxKB(t *testing.T) {
	t.Parallel()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"result-root/full.md":           "# full\n",
		"result-root/images/chart.png":  "fake-png",
		"result-root/content_list.json": "must be removed",
		"result-root/origin.docx":       "must be removed",
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
	entries := zipEntries(t, resultPath)
	want := map[string]string{
		"result-root/full.md":          "# full\n",
		"result-root/images/":          "",
		"result-root/images/chart.png": "fake-png",
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("normalized ZIP entries = %#v, want %#v", entries, want)
	}
}

func zipEntries(t *testing.T, archivePath string) map[string]string {
	t.Helper()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string]string, len(reader.File))
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			entries[entry.Name] = ""
			continue
		}
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(input)
		_ = input.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[entry.Name] = string(content)
	}
	return entries
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

func TestMaxKBDeleteFailureClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantReconc bool
	}{
		{
			name:     "permission failure is confirmed",
			err:      &adapter.MaxKBOperationError{Operation: adapter.MaxKBOperationDelete, Err: &adapter.MaxKBError{Type: adapter.MaxKBErrorPermissionDenied, StatusCode: 403}},
			wantCode: "MAXKB_DELETE_FAILED",
		},
		{
			name:       "timeout is uncertain",
			err:        &adapter.MaxKBOperationError{Operation: adapter.MaxKBOperationDelete, Err: &adapter.MaxKBError{Type: adapter.MaxKBErrorTimeout}},
			wantCode:   "MAXKB_DELETE_UNKNOWN",
			wantReconc: true,
		},
		{
			name:       "server error is uncertain",
			err:        &adapter.MaxKBOperationError{Operation: adapter.MaxKBOperationDelete, Err: &adapter.MaxKBError{Type: adapter.MaxKBErrorBusiness, StatusCode: 502}},
			wantCode:   "MAXKB_DELETE_UNKNOWN",
			wantReconc: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCode, gotReconcile := maxKBDeleteFailure(tc.err)
			if gotCode != tc.wantCode || gotReconcile != tc.wantReconc {
				t.Fatalf("maxKBDeleteFailure() = %q, %v; want %q, %v", gotCode, gotReconcile, tc.wantCode, tc.wantReconc)
			}
		})
	}
}
