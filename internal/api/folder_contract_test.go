package api

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"maxkb-local-file-sync/internal/repository"
)

type folderNameRepositoryStub struct {
	repository.SyncFolderRepository
	folders []*repository.SyncFolder
}

func (stub *folderNameRepositoryStub) List(context.Context) ([]*repository.SyncFolder, error) {
	return stub.folders, nil
}

func TestFolderContractDoesNotExposeTaskMinerUTransportSettings(t *testing.T) {
	var req CreateFolderRequest
	if err := json.Unmarshal([]byte(`{
		"name":"Docs",
		"localPath":"/tmp/docs",
		"enableMinerU":true,
		"mineruMode":"online",
		"mineruEndpoint":"https://legacy-mineru.example.test"
	}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.EnableMinerU {
		t.Fatal("legacy-compatible payload must still preserve the supported enableMinerU field")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("expected a JSON request representation")
	}
	for _, forbidden := range []string{"mineruMode", "mineruEndpoint"} {
		if jsonStringContains(encoded, forbidden) {
			t.Fatalf("task-level MinerU transport setting %q must not be exposed by the Go API: %s", forbidden, encoded)
		}
	}
}

func jsonStringContains(data []byte, want string) bool {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return false
	}
	_, ok := object[want]
	return ok
}

func TestPreviewMinerUExtensionsRespectEnableSwitch(t *testing.T) {
	if got := previewMinerUExtensions(false, ".doc, .csv"); len(got) != 0 {
		t.Fatalf("disabled MinerU preview extensions = %#v, want empty", got)
	}
	got := previewMinerUExtensions(true, ".doc, csv")
	want := []string{".doc", ".csv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled MinerU preview extensions = %#v, want %#v", got, want)
	}
}

func TestValidateFolderTargetRequiresWorkspaceAndKnowledgeBase(t *testing.T) {
	tests := []struct {
		name string
		req  CreateFolderRequest
		want string
	}{
		{name: "name missing", req: CreateFolderRequest{LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请输入任务名称"},
		{name: "name whitespace", req: CreateFolderRequest{Name: "  ", LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请输入任务名称"},
		{name: "local folder missing", req: CreateFolderRequest{Name: "Docs", WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请选择本地文件夹"},
		{name: "local folder whitespace", req: CreateFolderRequest{Name: "Docs", LocalPath: "  ", WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请选择本地文件夹"},
		{name: "workspace missing", req: CreateFolderRequest{Name: "Docs", LocalPath: "/tmp/docs", KBId: "kb-1"}, want: "请选择目标工作区"},
		{name: "workspace whitespace", req: CreateFolderRequest{Name: "Docs", LocalPath: "/tmp/docs", WorkspaceId: "  ", KBId: "kb-1"}, want: "请选择目标工作区"},
		{name: "knowledge base missing", req: CreateFolderRequest{Name: "Docs", LocalPath: "/tmp/docs", WorkspaceId: "ws-1"}, want: "请选择知识库"},
		{name: "knowledge base whitespace", req: CreateFolderRequest{Name: "Docs", LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "\t"}, want: "请选择知识库"},
		{name: "all present", req: CreateFolderRequest{Name: "Docs", LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "kb-1"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFolderTarget(tt.req)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateFolderTarget() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("validateFolderTarget() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateFolderNameAvailable(t *testing.T) {
	repo := &folderNameRepositoryStub{folders: []*repository.SyncFolder{
		{FolderID: "folder-1", Name: "Test"},
		{FolderID: "folder-2", Name: "知识库同步"},
	}}

	if err := validateFolderNameAvailable(context.Background(), repo, " test ", ""); err == nil {
		t.Fatal("case-insensitive duplicate name must be rejected")
	}
	if err := validateFolderNameAvailable(context.Background(), repo, "知识库同步", "folder-2"); err != nil {
		t.Fatalf("current task name must be allowed during edit: %v", err)
	}
	if err := validateFolderNameAvailable(context.Background(), repo, "新任务", ""); err != nil {
		t.Fatalf("unique task name rejected: %v", err)
	}
}
