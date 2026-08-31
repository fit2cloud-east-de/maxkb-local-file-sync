package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fakeMaxKBToken = "fake-maxkb-token"

func newMaxKBTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP listener unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func newMaxKBTestClient(t *testing.T, baseURL string) *maxkbClient {
	t.Helper()
	adapter := NewMaxKBAdapter(MaxKBConfig{
		BaseURL:    baseURL,
		APIKey:     fakeMaxKBToken,
		Timeout:    time.Second,
		MaxRetries: 1,
	})
	client, ok := adapter.(*maxkbClient)
	if !ok {
		t.Fatalf("unexpected adapter type %T", adapter)
	}
	return client
}

func writeMaxKBEnvelope(w http.ResponseWriter, code any, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "data": data})
}

func assertMaxKBHeaders(t *testing.T, r *http.Request, jsonRequest bool) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+fakeMaxKBToken {
		t.Errorf("Authorization = %q", got)
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	for _, name := range []string{"Cookie", "Referer", "Origin", "User-Agent", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "sec-ch-ua"} {
		if got := r.Header.Get(name); got != "" {
			t.Errorf("forbidden browser header %s leaked: %q", name, got)
		}
	}
	if jsonRequest && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
	}
}

func TestMaxKBBaseURLAndDynamicLinksEscapeIDs(t *testing.T) {
	client := newMaxKBTestClient(t, "http://127.0.0.1:12345/maxkb///")
	if got := client.cfg.BaseURL; got != "http://127.0.0.1:12345/maxkb" {
		t.Fatalf("normalized BaseURL = %q", got)
	}
	endpoint, err := client.endpoint("api", "workspace", pathSegment("ws/a"), "knowledge", pathSegment("kb?x"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Path; got != "/maxkb/admin/api/workspace/ws/a/knowledge/kb?x" {
		t.Fatalf("decoded endpoint path = %q", got)
	}
	if got := u.EscapedPath(); got != "/maxkb/admin/api/workspace/ws%2Fa/knowledge/kb%3Fx" {
		t.Fatalf("escaped endpoint path = %q", got)
	}
	kbLink, err := client.KnowledgeBaseLink("ws/a", "kb?x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kbLink, "/maxkb/admin/knowledge/kb%3Fx/ws%2Fa/0/document") {
		t.Fatalf("knowledge link = %q", kbLink)
	}
	docLink, err := client.DocumentLink("ws/a", "kb?x", "doc/1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(docLink, "/maxkb/admin/paragraph/kb%3Fx/doc%2F1?") || !strings.Contains(docLink, "from=workspace") || !strings.Contains(docLink, "isShared=false") {
		t.Fatalf("document link = %q", docLink)
	}
}

func TestMaxKBValidateConnectionRequiresExactProfileContract(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantType    MaxKBErrorType
		wantOK      bool
		wantDisplay string
	}{
		{name: "valid", body: `{"code":200,"data":{"license_is_valid":true,"version":"v2.10.4-lts (build fake)"}}`, wantOK: true, wantDisplay: "v2.10.4-lts"},
		{name: "code zero is not profile success", body: `{"code":0,"data":{"license_is_valid":true,"version":"v2.10.4-lts"}}`, wantType: MaxKBErrorIncompatible},
		{name: "license invalid", body: `{"code":200,"data":{"license_is_valid":false,"version":"v2.10.4-lts"}}`, wantType: MaxKBErrorLicenseInvalid},
		{name: "missing version", body: `{"code":200,"data":{"license_is_valid":true}}`, wantType: MaxKBErrorIncompatible},
		{name: "malformed json", body: `{not-json`, wantType: MaxKBErrorIncompatible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/admin/api/profile" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				assertMaxKBHeaders(t, r, false)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			client := newMaxKBTestClient(t, server.URL)
			result, err := client.ValidateConnection(context.Background())
			if tt.wantOK {
				if err != nil || result == nil || !result.Success || result.VersionDisplay != tt.wantDisplay {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				return
			}
			if err == nil || result == nil || result.Success {
				t.Fatalf("expected validation failure, result=%#v err=%v", result, err)
			}
			var maxErr *MaxKBError
			if !errors.As(err, &maxErr) || maxErr.Type != tt.wantType || result.ErrorType != tt.wantType {
				t.Fatalf("error=%#v result=%#v", maxErr, result)
			}
		})
	}
}

func TestMaxKBDiscoveryAndKnowledgePagination(t *testing.T) {
	var pageCalls int32
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMaxKBHeaders(t, r, false)
		switch r.URL.Path {
		case "/admin/api/user/profile":
			writeMaxKBEnvelope(w, 200, map[string]any{
				"id": "user-1", "username": "fake-user",
				"workspace_list": []any{map[string]any{"id": "ws-1", "name": "Workspace", "create_time": "2026-01-01T00:00:00Z"}},
			})
		case "/admin/api/workspace/ws-1/KNOWLEDGE/folder":
			writeMaxKBEnvelope(w, 200, []any{map[string]any{"id": "folder-1", "name": "Root", "children": []any{map[string]any{"id": "child-1", "name": "Child"}}}})
		case "/admin/api/workspace/ws-1/knowledge/1/2":
			atomic.AddInt32(&pageCalls, 1)
			if r.URL.Query().Get("folder_id") != "folder-1" || r.URL.Query().Get("scope") != "WORKSPACE" {
				t.Errorf("query = %v", r.URL.Query())
			}
			writeMaxKBEnvelope(w, 200, map[string]any{
				"total": 2, "current": 1, "size": 2,
				"records": []any{
					map[string]any{"id": "kb-0", "name": "Filtered", "type": 1},
					map[string]any{"id": "kb-1", "name": "Allowed", "type": 0, "folder_id": "folder-1", "file_size_limit": 99, "file_count_limit": 7, "document_count": 2},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMaxKBTestClient(t, server.URL)
	profile, err := client.GetUserProfile(context.Background())
	if err != nil || len(profile.Workspaces) != 1 || profile.Workspaces[0].ID != "ws-1" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	folders, err := client.ListKnowledgeFolders(context.Background(), "ws-1")
	if err != nil || len(folders) != 1 || len(folders[0].Children) != 1 {
		t.Fatalf("folders=%#v err=%v", folders, err)
	}
	page, err := client.ListKnowledgeBasesPage(context.Background(), &ListKnowledgeBasesRequest{WorkspaceID: "ws-1", FolderID: "folder-1", Page: 1, Size: 2})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "kb-1" || page.FetchedCount != 2 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if page.Items[0].FileSizeLimit != 99 || page.Items[0].FileCountLimit != 7 || page.Items[0].DocumentCount != 2 {
		t.Fatalf("knowledge limits not mapped: %#v", page.Items[0])
	}
	if atomic.LoadInt32(&pageCalls) != 1 {
		t.Fatalf("page calls=%d", pageCalls)
	}
}

func TestMaxKBListAllKnowledgeUsesFolderIDAndDoesNotStopOnFilteredRecords(t *testing.T) {
	var calls int32
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/api/workspace/ws/KNOWLEDGE/folder":
			writeMaxKBEnvelope(w, 200, []any{map[string]any{"id": "folder-1", "name": "Root"}})
		case "/admin/api/workspace/ws/knowledge/1/100", "/admin/api/workspace/ws/knowledge/2/100":
			if r.URL.Query().Get("folder_id") != "folder-1" || r.URL.Query().Get("scope") != "WORKSPACE" {
				t.Fatalf("query = %v", r.URL.Query())
			}
			page := atomic.AddInt32(&calls, 1)
			records := make([]any, 100)
			for i := range records {
				records[i] = map[string]any{"id": fmt.Sprintf("ignored-%d", i), "type": 1}
			}
			if page == 1 {
				writeMaxKBEnvelope(w, 200, map[string]any{"total": 101, "current": 1, "size": 100, "records": records})
				return
			}
			writeMaxKBEnvelope(w, 200, map[string]any{"total": 101, "current": 2, "size": 100, "records": []any{map[string]any{"id": "kb-final", "type": 0, "folder_id": "folder-1"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMaxKBTestClient(t, server.URL)
	items, err := client.ListKnowledgeBases(context.Background(), "ws")
	if err != nil || len(items) != 1 || items[0].ID != "kb-final" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("items=%#v calls=%d err=%v", items, calls, err)
	}
}

func TestMaxKBListAllKnowledgeTraversesFoldersAndDeduplicates(t *testing.T) {
	var folderQueries []string
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/api/workspace/ws/KNOWLEDGE/folder":
			writeMaxKBEnvelope(w, 200, []any{
				map[string]any{"id": "folder-1", "name": "Root", "children": []any{map[string]any{"id": "folder-2", "name": "Child"}}},
			})
		case "/admin/api/workspace/ws/knowledge/1/100":
			folderID := r.URL.Query().Get("folder_id")
			folderQueries = append(folderQueries, folderID)
			if r.URL.Query().Get("scope") != "WORKSPACE" {
				t.Fatalf("scope query = %v", r.URL.Query())
			}
			writeMaxKBEnvelope(w, 200, map[string]any{
				"total": 1, "current": 1, "size": 100,
				"records": []any{map[string]any{"id": "kb-shared", "name": "Knowledge", "type": 0, "folder_id": folderID}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMaxKBTestClient(t, server.URL)
	items, err := client.ListKnowledgeBases(context.Background(), "ws")
	if err != nil || len(items) != 1 || items[0].ID != "kb-shared" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if !reflect.DeepEqual(folderQueries, []string{"folder-1", "folder-2"}) {
		t.Fatalf("folder queries=%v", folderQueries)
	}
}

func TestMaxKBListAllKnowledgeWithNoFoldersDoesNotRequestKnowledge(t *testing.T) {
	var knowledgeCalls int32
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/workspace/ws/KNOWLEDGE/folder" {
			writeMaxKBEnvelope(w, 200, []any{})
			return
		}
		atomic.AddInt32(&knowledgeCalls, 1)
		http.NotFound(w, r)
	}))
	client := newMaxKBTestClient(t, server.URL)
	items, err := client.ListKnowledgeBases(context.Background(), "ws")
	if err != nil || len(items) != 0 || atomic.LoadInt32(&knowledgeCalls) != 0 {
		t.Fatalf("items=%#v knowledgeCalls=%d err=%v", items, knowledgeCalls, err)
	}
}

func TestMaxKBEmbeddingModelsMergeFilterAndDeduplicate(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/api/workspace/ws/model_list" || r.URL.Query().Get("model_type") != "EMBEDDING" {
			t.Fatalf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		writeMaxKBEnvelope(w, 200, map[string]any{
			"shared_model": []any{
				map[string]any{"id": "m-1", "name": "Shared", "status": "AVAILABLE"},
				map[string]any{"id": "m-disabled", "name": "Disabled", "status": "DISABLED"},
			},
			"model": []any{
				map[string]any{"id": "m-1", "name": "Duplicate", "status": "ACTIVE"},
				map[string]any{"id": "m-2", "name": "Active", "status": "SUCCESS"},
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	models, err := client.ListEmbeddingModels(context.Background(), "ws")
	if err != nil || len(models) != 2 || models[0].ID != "m-1" || models[1].ID != "m-2" {
		t.Fatalf("models=%#v err=%v", models, err)
	}
}

func TestMaxKBCreateKnowledgeBaseUsesContractAndRequiresID(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/api/workspace/ws/knowledge/base" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		assertMaxKBHeaders(t, r, true)
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"folder_id": "folder", "name": "New KB", "desc": "Description", "embedding_model_id": "model"}
		if len(payload) != len(want) {
			t.Fatalf("payload=%v", payload)
		}
		for key, value := range want {
			if payload[key] != value {
				t.Errorf("payload[%q]=%q, want %q", key, payload[key], value)
			}
		}
		writeMaxKBEnvelope(w, 200, map[string]any{"id": "kb-new", "name": "New KB", "type": 0})
	}))
	client := newMaxKBTestClient(t, server.URL)
	kb, err := client.CreateKnowledgeBase(context.Background(), &CreateKnowledgeBaseRequest{WorkspaceID: "ws", FolderID: "folder", Name: "New KB", Description: "Description", EmbeddingModelID: "model"})
	if err != nil || kb.ID != "kb-new" || kb.Type != 0 {
		t.Fatalf("kb=%#v err=%v", kb, err)
	}

	missingIDServer := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, map[string]any{"name": "No ID"})
	}))
	missingIDClient := newMaxKBTestClient(t, missingIDServer.URL)
	_, err = missingIDClient.CreateKnowledgeBase(context.Background(), &CreateKnowledgeBaseRequest{WorkspaceID: "ws", Name: "No ID"})
	var maxErr *MaxKBError
	if !errors.As(err, &maxErr) || maxErr.Type != MaxKBErrorIncompatible {
		t.Fatalf("missing id error=%v", err)
	}
}

func TestMaxKBOSSAndSmartSplitKeepSourceFileIDDistinct(t *testing.T) {
	var ossCalls, splitCalls int32
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMaxKBHeaders(t, r, false)
		switch r.URL.Path {
		case "/admin/api/oss/file":
			atomic.AddInt32(&ossCalls, 1)
			if r.ContentLength <= 0 || len(r.TransferEncoding) != 0 {
				t.Fatalf("OSS request must use a fixed content length: content_length=%d transfer_encoding=%v", r.ContentLength, r.TransferEncoding)
			}
			if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("OSS request = %s %s", r.Method, r.Header.Get("Content-Type"))
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("source_id") != "TEMPORARY_120_MINUTE" || r.FormValue("source_type") != "TEMPORARY_120_MINUTE" {
				t.Fatalf("OSS fields = %v", r.MultipartForm.Value)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, _ := io.ReadAll(file)
			if header.Filename != "source.md" || string(content) != "source-content" {
				t.Fatalf("file=%q content=%q", header.Filename, content)
			}
			writeMaxKBEnvelope(w, 200, map[string]any{"file_id": "source-1", "url": "./oss/file/source-1"})
		case "/admin/api/workspace/ws/knowledge/kb/document/split":
			atomic.AddInt32(&splitCalls, 1)
			if r.ContentLength <= 0 || len(r.TransferEncoding) != 0 {
				t.Fatalf("split request must use a fixed content length: content_length=%d transfer_encoding=%v", r.ContentLength, r.TransferEncoding)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, _ := io.ReadAll(file)
			if header.Filename != "source.md" || string(content) != "split-content" {
				t.Fatalf("split file=%q content=%q", header.Filename, content)
			}
			writeMaxKBEnvelope(w, 200, []any{map[string]any{"title": "Title", "content": "Body", "source_file_id": "source-2"}})
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMaxKBTestClient(t, server.URL)
	oss, err := client.UploadToOSS(context.Background(), strings.NewReader("source-content"), "source.md", 14)
	if err != nil || oss.FileID != "source-1" || oss.FileURL != "./oss/file/source-1" {
		t.Fatalf("OSS=%#v err=%v", oss, err)
	}
	if got := (&UploadDocumentResponse{SourceFileID: oss.FileID}).DocumentID; got != "" {
		t.Fatalf("source file was exposed as document ID: %q", got)
	}
	split, err := client.SmartSplit(context.Background(), &SmartSplitRequest{WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader("split-content"), FileName: "source.md", FileSize: 13})
	if err != nil || split.SourceFileID != "source-2" || len(split.Paragraphs) != 1 || split.Paragraphs[0].Content != "Body" {
		t.Fatalf("split=%#v err=%v", split, err)
	}
	if ossCalls != 1 || splitCalls != 1 {
		t.Fatalf("calls oss=%d split=%d", ossCalls, splitCalls)
	}
}

func TestMaxKBBatchCreateParsesMaxKBResponseAndPreservesPayload(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/admin/api/workspace/ws/knowledge/kb/document/batch_create" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		assertMaxKBHeaders(t, r, true)
		var payload []struct {
			Name         string      `json:"name"`
			SourceFileID string      `json:"source_file_id"`
			Paragraphs   []Paragraph `json:"paragraphs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 || payload[0].Name != "doc.md" || payload[0].SourceFileID != "source-1" || len(payload[0].Paragraphs) != 1 || payload[0].Paragraphs[0].Content != "body" {
			t.Fatalf("payload=%#v", payload)
		}
		// MaxKB v2.10.4-lts serializes batch_save's tuple result as
		// [document_records, knowledge_id, workspace_id].
		writeMaxKBEnvelope(w, 200, []any{
			[]any{map[string]any{"id": "document-1", "name": "doc.md"}},
			"kb",
			"ws",
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{WorkspaceID: "ws", KnowledgeID: "kb", Documents: []DocumentToCreate{{Name: "doc.md", SourceFileID: "source-1", Paragraphs: []Paragraph{{Title: "title", Content: "body"}}}}})
	if err != nil || len(result.DocumentIDs) != 1 || result.DocumentIDs[0] != "document-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestMaxKBBatchCreateAcceptsDirectDocumentRecordArray(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/admin/api/workspace/ws/knowledge/kb/document/batch_create" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		assertMaxKBHeaders(t, r, true)
		writeMaxKBEnvelope(w, 200, []any{
			map[string]any{
				"id":           "document-1",
				"name":         "doc.csv",
				"status":       "PENDING",
				"knowledge_id": "kb",
				"user_id":      "user-1",
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{
		WorkspaceID: "ws",
		KnowledgeID: "kb",
		Documents:   []DocumentToCreate{{Name: "doc.csv", SourceFileID: "source-1"}},
	})
	if err != nil {
		t.Fatalf("CreateDocuments() error = %v", err)
	}
	if result == nil || !reflect.DeepEqual(result.DocumentIDs, []string{"document-1"}) {
		t.Fatalf("result = %#v, want document-1", result)
	}
}

func TestMaxKBBatchCreateAcceptsDirectDocumentRecordArrayOfThree(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, []any{
			map[string]any{"id": "document-a", "name": "a.md"},
			map[string]any{"id": "document-b", "name": "b.md"},
			map[string]any{"id": "document-c", "name": "c.md"},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{
		WorkspaceID: "ws",
		KnowledgeID: "kb",
		Documents: []DocumentToCreate{
			{Name: "a.md"},
			{Name: "b.md"},
			{Name: "c.md"},
		},
	})
	if err != nil {
		t.Fatalf("CreateDocuments() error = %v", err)
	}
	want := []string{"document-a", "document-b", "document-c"}
	if result == nil || !reflect.DeepEqual(result.DocumentIDs, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestMaxKBBatchCreateRejectsUnsupportedResponses(t *testing.T) {
	tests := []struct {
		name string
		data any
	}{
		{name: "legacy document_ids object", data: map[string]any{"document_ids": []string{"document-1"}}},
		{name: "missing record id", data: []any{[]any{map[string]any{"name": "doc.md"}}, "kb", "ws"}},
		{name: "wrong record count", data: []any{[]any{}, "kb", "ws"}},
		{name: "duplicate record ids", data: []any{[]any{map[string]any{"id": "document-1"}, map[string]any{"id": "document-1"}}, "kb", "ws"}},
		{name: "wrong tuple length", data: []any{[]any{map[string]any{"id": "document-1"}}, "kb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeMaxKBEnvelope(w, 200, tt.data)
			}))
			client := newMaxKBTestClient(t, server.URL)
			_, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{
				WorkspaceID: "ws",
				KnowledgeID: "kb",
				Documents:   []DocumentToCreate{{Name: "doc.md"}},
			})
			var maxErr *MaxKBError
			if !errors.As(err, &maxErr) || maxErr.Type != MaxKBErrorIncompatible {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMaxKBBatchCreateThreeRecordTupleContract(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/admin/api/workspace/ws/knowledge/kb/document/batch_create" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		assertMaxKBHeaders(t, r, true)

		var payload []struct {
			Name         string `json:"name"`
			SourceFileID string `json:"source_file_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 3 {
			t.Fatalf("payload length = %d, want 3", len(payload))
		}
		for i, want := range []struct {
			name   string
			source string
		}{{"a.md", "source-a"}, {"b.md", "source-b"}, {"c.md", "source-c"}} {
			if payload[i].Name != want.name || payload[i].SourceFileID != want.source {
				t.Fatalf("payload[%d] = %#v, want name=%q source=%q", i, payload[i], want.name, want.source)
			}
		}

		// The target contract is a three-element tuple:
		// [document_records, knowledge_id, workspace_id].
		writeMaxKBEnvelope(w, 200, []any{
			[]any{
				map[string]any{"id": "document-a", "name": "a.md"},
				map[string]any{"id": "document-b", "name": "b.md"},
				map[string]any{"id": "document-c", "name": "c.md"},
			},
			"kb",
			"ws",
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{
		WorkspaceID: "ws",
		KnowledgeID: "kb",
		Documents: []DocumentToCreate{
			{Name: "a.md", SourceFileID: "source-a"},
			{Name: "b.md", SourceFileID: "source-b"},
			{Name: "c.md", SourceFileID: "source-c"},
		},
	})
	if err != nil {
		t.Fatalf("CreateDocuments() error = %v", err)
	}
	if result == nil {
		t.Fatal("CreateDocuments() returned nil result")
	}
	wantIDs := []string{"document-a", "document-b", "document-c"}
	if !reflect.DeepEqual(result.DocumentIDs, wantIDs) {
		t.Fatalf("DocumentIDs = %#v, want %#v", result.DocumentIDs, wantIDs)
	}
}

func assertMaxKBIncompatibleDiagnostic(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an incompatible-response error")
	}
	var maxErr *MaxKBError
	if !errors.As(err, &maxErr) {
		t.Fatalf("error type = %T, want *MaxKBError: %v", err, err)
	}
	if maxErr.Type != MaxKBErrorIncompatible {
		t.Fatalf("error classification = %q, want %q; error=%v", maxErr.Type, MaxKBErrorIncompatible, err)
	}
	for _, want := range []string{"operation=", "method=", "http_status=", "data_type=", "data_bytes=", "expected="} {
		if !strings.Contains(maxErr.Diagnostic, want) {
			t.Fatalf("diagnostic=%q, missing safe shape field %q", maxErr.Diagnostic, want)
		}
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("diagnostic leaked forbidden value %q: %q", value, err.Error())
		}
	}
}

func TestMaxKBBatchCreateResponseShapeDiagnosticsAreSafe(t *testing.T) {
	const (
		fakeResponseSecret = "fake-response-secret-should-not-leak"
		fakeDocumentBody   = "FAKE_DOCUMENT_BODY_SHOULD_NOT_LEAK"
	)

	tests := []struct {
		name      string
		data      any
		documents int
		wantPart  string
	}{
		{
			name:      "top-level data empty array",
			data:      []any{},
			documents: 3,
			wantPart:  "returned 0 document records",
		},
		{
			name:      "top-level data object",
			data:      map[string]any{"records": []any{}},
			documents: 1,
			wantPart:  "unsupported data shape",
		},
		{
			name:      "tuple first element has wrong type",
			data:      []any{"records-not-an-array", "kb", "ws"},
			documents: 1,
			wantPart:  "document records have an unsupported shape",
		},
		{
			name: "missing id with sensitive fields",
			data: []any{
				[]any{map[string]any{
					"name":    "private-document.md",
					"content": fakeDocumentBody,
					"token":   fakeResponseSecret,
				}},
				"kb",
				"ws",
			},
			documents: 1,
			wantPart:  "document id",
		},
		{
			name: "mixed invalid records",
			data: []any{
				[]any{
					map[string]any{"id": "document-a"},
					map[string]any{"name": "missing-id", "content": fakeDocumentBody},
					map[string]any{"id": " ", "token": fakeResponseSecret},
				},
				"kb",
				"ws",
			},
			documents: 3,
			wantPart:  "document id",
		},
		{
			name: "record id has invalid type",
			data: []any{
				[]any{
					map[string]any{"id": "document-a"},
					map[string]any{"id": 12345, "content": fakeDocumentBody},
				},
				"kb",
				"ws",
			},
			documents: 2,
			wantPart:  "document records have an unsupported shape",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeMaxKBEnvelope(w, 200, tc.data)
			}))
			client := newMaxKBTestClient(t, server.URL)
			documents := make([]DocumentToCreate, tc.documents)
			for i := range documents {
				documents[i] = DocumentToCreate{Name: fmt.Sprintf("file-%d.md", i), SourceFileID: fmt.Sprintf("source-%d", i)}
			}

			result, err := client.CreateDocuments(context.Background(), &CreateDocumentsRequest{
				WorkspaceID: "ws",
				KnowledgeID: "kb",
				Documents:   documents,
			})
			if result != nil {
				t.Fatalf("incompatible response returned a partial result: %#v", result)
			}
			assertMaxKBIncompatibleDiagnostic(t, err, fakeResponseSecret, fakeDocumentBody)
			var maxErr *MaxKBError
			if !errors.As(err, &maxErr) || !strings.Contains(maxErr.Diagnostic, "operation=batch_create") {
				t.Fatalf("diagnostic=%q, want operation=batch_create", maxErr.Diagnostic)
			}
			if tc.wantPart != "" && !strings.Contains(err.Error(), tc.wantPart) {
				t.Fatalf("diagnostic = %q, want substring %q", err.Error(), tc.wantPart)
			}
		})
	}
}

func TestMaxKBListDocumentsReadsNestedMetaSourceFileID(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/api/workspace/ws/knowledge/kb/document/1/100" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		writeMaxKBEnvelope(w, 200, map[string]any{
			"total": 2, "current": 1, "size": 100,
			"records": []any{
				map[string]any{
					"id": "document-meta", "name": "from meta", "status": "SUCCESS",
					"meta": map[string]any{"source_file_id": "source-from-meta"},
				},
				map[string]any{
					"id": "document-top-level", "name": "top level wins", "status": "PENDING",
					"source_file_id": "source-from-top-level",
					"meta":           map[string]any{"source_file_id": "source-from-meta-but-not-used"},
				},
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.ListDocuments(context.Background(), &ListDocumentsRequest{
		WorkspaceID: "ws", KnowledgeID: "kb", Page: 1, Size: 100,
	})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if result == nil || len(result.Items) != 2 {
		t.Fatalf("result = %#v, want two documents", result)
	}
	if got := result.Items[0].SourceFileID; got != "source-from-meta" {
		t.Errorf("nested SourceFileID = %q, want %q", got, "source-from-meta")
	}
	if got := result.Items[1].SourceFileID; got != "source-from-top-level" {
		t.Errorf("top-level SourceFileID = %q, want %q", got, "source-from-top-level")
	}
}

func TestMaxKBDocumentPaginationStatusAndDelete(t *testing.T) {
	var listCalls int32
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/workspace/ws/knowledge/kb/document/1/100" || r.URL.Path == "/admin/api/workspace/ws/knowledge/kb/document/2/100" {
			call := atomic.AddInt32(&listCalls, 1)
			if call == 1 {
				writeMaxKBEnvelope(w, 200, map[string]any{"total": 101, "current": 1, "size": 100, "records": makeDocuments(100)})
				return
			}
			writeMaxKBEnvelope(w, 200, map[string]any{"total": 101, "current": 2, "size": 100, "records": []any{map[string]any{"id": "document-target", "name": "target", "status": "SUCCESS", "source_file_id": "source-1"}}})
			return
		}
		if r.URL.Path == "/admin/api/workspace/ws/knowledge/kb/document/document-target" {
			if r.URL.RawQuery != "" {
				t.Errorf("delete query = %q", r.URL.RawQuery)
			}
			if r.Method != http.MethodDelete {
				t.Errorf("delete method = %s", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	client := newMaxKBTestClient(t, server.URL)
	status, err := client.GetDocumentStatus(context.Background(), "ws", "kb", "document-target")
	if err != nil || status.ID != "document-target" || status.StatusMapped != MaxKBDocStatusSuccess {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if listCalls != 2 {
		t.Fatalf("list calls=%d", listCalls)
	}
	if err := client.DeleteDocument(context.Background(), &DeleteDocumentRequest{WorkspaceID: "ws", KBId: "kb", DocumentID: "document-target"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	missingServer := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":404,"message":"not found"}`)
	}))
	missingClient := newMaxKBTestClient(t, missingServer.URL)
	if err := missingClient.DeleteDocument(context.Background(), &DeleteDocumentRequest{WorkspaceID: "ws", KBId: "kb", DocumentID: "document-target"}); err != nil {
		t.Fatalf("404 delete should be idempotent: %v", err)
	}
}

func makeDocuments(count int) []any {
	result := make([]any, count)
	for i := range result {
		result[i] = map[string]any{"id": "document-" + filepath.Base(string(rune('a'+(i%26)))), "status": "PENDING"}
	}
	return result
}

func TestMaxKBDocumentStatusMapsValidatedAggregateState(t *testing.T) {
	for _, raw := range []string{"nnnn", "2nnn", "2n2n", "NNNN"} {
		if got := mapDocumentStatus(raw); got != MaxKBDocStatusSuccess {
			t.Fatalf("raw status %q = %s, want success", raw, got)
		}
	}
	for _, raw := range []string{"queued", "done", "embedding", "service-specific-state", "1nnn", "3nnn", "nnn"} {
		if got := mapDocumentStatus(raw); got == MaxKBDocStatusSuccess {
			t.Fatalf("raw status %q was guessed as success", raw)
		}
	}
}

func TestMaxKBErrorClassificationAndRetryPolicy(t *testing.T) {
	cases := []struct {
		status   int
		wantType MaxKBErrorType
		retry    bool
	}{
		{http.StatusUnauthorized, MaxKBErrorInvalidAPIKey, false},
		{http.StatusForbidden, MaxKBErrorPermissionDenied, false},
		{http.StatusNotFound, MaxKBErrorBusiness, false},
		{http.StatusTooManyRequests, MaxKBErrorBusiness, true},
		{http.StatusBadGateway, MaxKBErrorBusiness, true},
		{http.StatusServiceUnavailable, MaxKBErrorBusiness, true},
		{http.StatusGatewayTimeout, MaxKBErrorBusiness, true},
		{http.StatusInternalServerError, MaxKBErrorBusiness, false},
	}
	for _, tc := range cases {
		headers := make(http.Header)
		headers.Set("X-Request-ID", "request-fake")
		err := (&maxkbClient{}).errorFromHTTP(tc.status, headers, []byte(`{"code":500,"message":"fake token should be sanitized if present"}`))
		var maxErr *MaxKBError
		if !errors.As(err, &maxErr) || maxErr.Type != tc.wantType || maxErr.IsRetryable() != tc.retry || maxErr.StatusCode != tc.status || maxErr.RequestID != "request-fake" {
			t.Fatalf("status %d error=%#v", tc.status, maxErr)
		}
	}
}

func TestMaxKBQueryBatchStatusIsExplicitLegacyIsolation(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/api/workspace/ws/dataset/kb/document/batch/task-1" || r.URL.RawQuery != "" {
			t.Fatalf("legacy request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		writeMaxKBEnvelope(w, 200, map[string]any{"task_id": "task-1", "status": "processing", "total_count": 1, "processed_count": 0, "updated_at": "2026-01-01T00:00:00Z"})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.QueryBatchStatus(context.Background(), &QueryBatchStatusRequest{WorkspaceID: "ws", KBId: "kb", TaskID: "task-1"})
	if err != nil || result.TaskID != "task-1" || result.Status != "processing" || result.TotalCount != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestMaxKBInvalidBaseURLAndMissingCredential(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.test", "https://user:pass@example.test", "https://example.test?query=1", "https://example.test#fragment"} {
		if _, err := normalizeBaseURL(raw); err == nil {
			t.Fatalf("normalizeBaseURL(%q) unexpectedly succeeded", raw)
		}
	}
	client := newMaxKBTestClient(t, "http://127.0.0.1:1")
	client.cfg.APIKey = ""
	_, err := client.GetUserProfile(context.Background())
	var maxErr *MaxKBError
	if !errors.As(err, &maxErr) || maxErr.Type != MaxKBErrorInvalidAPIKey {
		t.Fatalf("missing credential error=%v", err)
	}
}

func TestMaxKBMultipartReaderErrorIsReturned(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		writeMaxKBEnvelope(w, 200, map[string]any{"file_id": "source-1"})
	}))
	client := newMaxKBTestClient(t, server.URL)
	_, err := client.UploadToOSS(context.Background(), &errorReader{}, "file.txt", -1)
	if err == nil || !strings.Contains(err.Error(), "stream MaxKB multipart file") {
		t.Fatalf("error=%v", err)
	}
}

type errorReader struct{}

func (*errorReader) Read([]byte) (int, error) { return 0, errors.New("fake reader failure") }

func TestMaxKBUploadToOSSAcceptsNativeStringData(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, "./oss/file/native-source-1")
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.UploadToOSS(context.Background(), strings.NewReader("fake-file-content"), "fake.md", int64(len("fake-file-content")))
	if err != nil {
		t.Fatalf("UploadToOSS: %v", err)
	}
	if result.FileID != "native-source-1" || result.FileURL != "./oss/file/native-source-1" {
		t.Fatalf("result=%#v", result)
	}
}

func TestMaxKBUploadToOSSRejectsUnsafeStringWithShapeDiagnostic(t *testing.T) {
	const fakeSecret = "fake-upload-response-secret"
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, fakeSecret)
	}))
	client := newMaxKBTestClient(t, server.URL)
	_, err := client.UploadToOSS(context.Background(), strings.NewReader("fake-file-content"), "fake.md", 17)
	assertMaxKBIncompatibleDiagnostic(t, err, fakeSecret, "fake-file-content")
	for _, want := range []string{"operation=upload_oss", "http_status=200", "data_type=string", "content_type=application/json"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic=%q, missing %q", err.Error(), want)
		}
	}
}

func TestMaxKBSmartSplitAcceptsWrappedParagraphsAndFillsSourceID(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, map[string]any{
			"name":           "fake.md",
			"source_file_id": "source-wrapped-1",
			"paragraphs": []any{
				map[string]any{"title": "T1", "content": "C1"},
				map[string]any{"title": "T2", "content": "C2", "source_file_id": "source-wrapped-1"},
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.SmartSplit(context.Background(), &SmartSplitRequest{WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader("fake-file-content"), FileName: "fake.md", FileSize: 17})
	if err != nil {
		t.Fatalf("SmartSplit: %v", err)
	}
	if result.SourceFileID != "source-wrapped-1" || len(result.Paragraphs) != 2 || result.Paragraphs[1].Content != "C2" {
		t.Fatalf("result=%#v", result)
	}
}

func TestMaxKBSmartSplitAcceptsV2104SingleDocumentContentShape(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MaxKB v2.10.4-lts split handlers return a list containing the
		// handler result. CSV/text handlers use content[] on that result.
		writeMaxKBEnvelope(w, 200, map[string]any{
			"name":           "dataease-permissions.csv",
			"source_file_id": "native-source-single-1",
			"content": []any{
				map[string]any{"title": "", "content": "| a | b |\\n| --- | --- |\\n| 1 | 2 |"},
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	splitContent := "a,b\\n1,2\\n"
	result, err := client.SmartSplit(context.Background(), &SmartSplitRequest{
		WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader(splitContent),
		FileName: "dataease-permissions.csv", FileSize: int64(len(splitContent)),
	})
	if err != nil {
		t.Fatalf("SmartSplit: %v", err)
	}
	if result.Name != "dataease-permissions.csv" || result.SourceFileID != "native-source-single-1" || len(result.Paragraphs) != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestMaxKBSmartSplitShapeDiagnosticDoesNotIncludeResponseBody(t *testing.T) {
	const fakeBody = "FAKE_PARAGRAPH_BODY_SHOULD_NOT_LEAK"
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, map[string]any{"unexpected": fakeBody})
	}))
	client := newMaxKBTestClient(t, server.URL)
	_, err := client.SmartSplit(context.Background(), &SmartSplitRequest{WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader("fake-file-content"), FileName: "fake.md", FileSize: 17})
	assertMaxKBIncompatibleDiagnostic(t, err, fakeBody, "fake-file-content")
	for _, want := range []string{"operation=smart_split", "http_status=200", "data_type=object", "keys=unexpected"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic=%q, missing %q", err.Error(), want)
		}
	}
}

func TestMaxKBSmartSplitParsesNativeDocumentContentShape(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, []any{
			map[string]any{
				"name":           "dataease-permissions.csv",
				"source_file_id": "native-source-1",
				"content": []any{
					map[string]any{"title": "表头", "content": "| a | b |"},
					map[string]any{"title": "", "content": "| 1 | 2 |"},
				},
			},
		})
	}))
	client := newMaxKBTestClient(t, server.URL)
	result, err := client.SmartSplit(context.Background(), &SmartSplitRequest{
		WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader("a,b\n1,2\n"),
		FileName: "dataease-permissions.csv", FileSize: 8,
	})
	if err != nil {
		t.Fatalf("SmartSplit: %v", err)
	}
	if result.Name != "dataease-permissions.csv" || result.SourceFileID != "native-source-1" || len(result.Paragraphs) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.Paragraphs[0].Title != "表头" || result.Paragraphs[1].Content != "| 1 | 2 |" {
		t.Fatalf("paragraphs=%#v", result.Paragraphs)
	}
}

func TestMaxKBUploadToOSSRejectsEncodedPathSeparator(t *testing.T) {
	if got := extractOSSFileID("./oss/file/id%2Fchild"); got != "" {
		t.Fatalf("extractOSSFileID accepted encoded path separator: %q", got)
	}
}

func TestMaxKBSmartSplitDoesNotTreatSourceOnlyItemsAsDocuments(t *testing.T) {
	server := newMaxKBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMaxKBEnvelope(w, 200, []any{map[string]any{"source_file_id": "source-only"}})
	}))
	client := newMaxKBTestClient(t, server.URL)
	_, err := client.SmartSplit(context.Background(), &SmartSplitRequest{
		WorkspaceID: "ws", KnowledgeID: "kb", File: strings.NewReader("fake-file-content"), FileName: "fake.md", FileSize: 17,
	})
	assertMaxKBIncompatibleDiagnostic(t, err, "source-only", "fake-file-content")
	if !strings.Contains(err.Error(), "data_type=array") {
		t.Fatalf("diagnostic=%q", err.Error())
	}
}
