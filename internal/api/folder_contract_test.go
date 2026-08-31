package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
		{name: "local folder missing", req: CreateFolderRequest{WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请选择本地文件夹"},
		{name: "local folder whitespace", req: CreateFolderRequest{LocalPath: "  ", WorkspaceId: "ws-1", KBId: "kb-1"}, want: "请选择本地文件夹"},
		{name: "workspace missing", req: CreateFolderRequest{LocalPath: "/tmp/docs", KBId: "kb-1"}, want: "请选择目标工作区"},
		{name: "workspace whitespace", req: CreateFolderRequest{LocalPath: "/tmp/docs", WorkspaceId: "  ", KBId: "kb-1"}, want: "请选择目标工作区"},
		{name: "knowledge base missing", req: CreateFolderRequest{LocalPath: "/tmp/docs", WorkspaceId: "ws-1"}, want: "请选择知识库"},
		{name: "knowledge base whitespace", req: CreateFolderRequest{LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "\t"}, want: "请选择知识库"},
		{name: "all present", req: CreateFolderRequest{LocalPath: "/tmp/docs", WorkspaceId: "ws-1", KBId: "kb-1"}, want: ""},
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
