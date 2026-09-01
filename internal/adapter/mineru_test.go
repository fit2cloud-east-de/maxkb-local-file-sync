package adapter

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
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

func testClient(t *testing.T, mode string, handler http.Handler) (*mineruClient, *httptest.Server) {
	t.Helper()
	server := newTestServer(t, handler)
	client := newMinerUClient(MinerUConfig{BaseURL: server.URL, Mode: mode, APIKey: "fake-token", MaxRetries: 1, Timeout: time.Second, RetryBaseDelay: time.Millisecond})
	return client, server
}

func TestOnlineFlow(t *testing.T) {
	var uploaded []byte
	var batchCalls, uploadCalls int32
	var server *httptest.Server
	server = newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/file-urls/batch":
			atomic.AddInt32(&batchCalls, 1)
			if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("authorization=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"batch_id":"batch-1","file_urls":["`+server.URL+`/presigned"]}}`)
		case "/presigned":
			atomic.AddInt32(&uploadCalls, 1)
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read upload: %v", err)
			}
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("presigned upload leaked auth: %q", got)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMinerUClient(MinerUConfig{BaseURL: server.URL, Mode: MinerUModeOnline, APIKey: "fake-token", MaxRetries: 1, Timeout: time.Second})
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{FileName: "doc.pdf", FileContent: []byte("abc"), AttemptID: "attempt-1"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.BatchID != "batch-1" || atomic.LoadInt32(&batchCalls) != 1 || atomic.LoadInt32(&uploadCalls) != 1 {
		t.Fatalf("unexpected response/calls: %#v %d %d", resp, batchCalls, uploadCalls)
	}
	if string(uploaded) != "abc" {
		t.Fatalf("uploaded %q", uploaded)
	}
}

func TestOnlineQueryAcceptsDocumentedIntermediateStatuses(t *testing.T) {
	statuses := []string{"waiting-file", "pending", "running", "converting", "done"}
	var calls int32
	client, _ := testClient(t, MinerUModeOnline, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/extract-results/batch/batch-status" {
			http.NotFound(w, r)
			return
		}
		i := int(atomic.AddInt32(&calls, 1)) - 1
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"extract_result":[{"batch_id":"batch-status","state":"`+statuses[i]+`"}]}}`)
	}))

	for _, want := range statuses {
		got, err := client.QueryTaskStatus(context.Background(), "batch-status")
		if err != nil {
			t.Fatalf("status %q returned error: %v", want, err)
		}
		if got == nil || got.Status != want {
			t.Fatalf("status %q returned %#v", want, got)
		}
	}
}

func TestOnlineQueryUnsupportedStatusIncludesActualValue(t *testing.T) {
	client, _ := testClient(t, MinerUModeOnline, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"extract_result":[{"batch_id":"batch-unknown","status":"new-status"}]}}`)
	}))
	_, err := client.QueryTaskStatus(context.Background(), "batch-unknown")
	if err == nil || !strings.Contains(err.Error(), `"new-status"`) {
		t.Fatalf("error=%v, expected actual status", err)
	}
	var mineruErr *MinerUError
	if !errors.As(err, &mineruErr) || mineruErr.Class != RetryClassProtocol || mineruErr.TaskID != "batch-unknown" {
		t.Fatalf("typed error=%#v", mineruErr)
	}
}

func TestOnlineQueryMissingStatusIsExplicit(t *testing.T) {
	client, _ := testClient(t, MinerUModeOnline, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"extract_result":[{"batch_id":"batch-empty"}]}}`)
	}))
	_, err := client.QueryTaskStatus(context.Background(), "batch-empty")
	if err == nil || !strings.Contains(err.Error(), "did not contain a task status") {
		t.Fatalf("error=%v, expected missing-status diagnostic", err)
	}
}

func TestOnlineFlowStreamsFilePath(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source-*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("file-path-content"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	var uploaded []byte
	var server *httptest.Server
	server = newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/file-urls/batch":
			_, _ = io.WriteString(w, `{"code":0,"data":{"batch_id":"batch-file","file_urls":["`+server.URL+`/presigned-file"]}}`)
		case "/presigned-file":
			var readErr error
			uploaded, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				t.Errorf("read upload: %v", readErr)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMinerUClient(MinerUConfig{BaseURL: server.URL, Mode: MinerUModeOnline, APIKey: "fake-token", MaxRetries: 1, Timeout: time.Second})
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{FileName: "doc.pdf", FilePath: file.Name(), FileSize: -1, AttemptID: "attempt-file"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.BatchID != "batch-file" || string(uploaded) != "file-path-content" {
		t.Fatalf("unexpected response/upload: %#v %q", resp, uploaded)
	}
}

func TestOnlineHealthUsesValidNonEmptyProbe(t *testing.T) {
	var gotFiles []map[string]string
	var gotModel string
	client, _ := testClient(t, MinerUModeOnline, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/file-urls/batch" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
			t.Errorf("authorization=%q", got)
		}
		var payload struct {
			Files        []map[string]string `json:"files"`
			ModelVersion string              `json:"model_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode probe: %v", err)
		}
		gotFiles = payload.Files
		gotModel = payload.ModelVersion
		if len(payload.Files) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":1001,"msg":"file list is empty"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"batch_id":"probe-batch","file_urls":["https://upload.invalid/probe"]}}`)
	}))

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("health probe failed: %v", err)
	}
	if health == nil || !health.Healthy {
		t.Fatalf("unexpected health result: %#v", health)
	}
	if len(gotFiles) != 1 || gotFiles[0]["name"] != "connection-probe.pdf" || strings.TrimSpace(gotFiles[0]["data_id"]) == "" {
		t.Fatalf("unexpected probe files: %#v", gotFiles)
	}
	if gotModel != "vlm" {
		t.Fatalf("model_version=%q", gotModel)
	}
}

func TestInternalHealthAndTaskProtocol(t *testing.T) {
	var taskBody []byte
	var server *httptest.Server
	server = newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"healthy","version":"3.4.5","protocol_version":2,"queued_tasks":0,"processing_tasks":0,"completed_tasks":2,"failed_tasks":0,"max_concurrent_requests":1,"processing_window_size":16}`)
		case "/tasks":
			var err error
			taskBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read task: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"task_id":"task-1","status":"pending","backend":"pipeline","file_names":["a"],"created_at":"2026-09-01T09:30:23.275210+00:00","started_at":null,"completed_at":null,"error":null,"status_url":"`+server.URL+`/status/task-1","result_url":"`+server.URL+`/result/task-1","queued_ahead":0,"message":"Task submitted successfully"}`)
		case "/status/task-1":
			_, _ = io.WriteString(w, `{"task_id":"task-1","status":"completed","backend":"pipeline","file_names":["a"],"created_at":"2026-09-01T09:30:23.275210+00:00","started_at":"2026-09-01T09:30:23.275676+00:00","completed_at":"2026-09-01T09:30:23.280613+00:00","error":null,"status_url":"`+server.URL+`/status/task-1","result_url":"`+server.URL+`/result/task-1","queued_ahead":0}`)
		case "/result/task-1":
			_, _ = io.WriteString(w, "zip-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	client := newMinerUClient(MinerUConfig{BaseURL: server.URL, Mode: MinerUModeInternal, APIKey: "fake-token", MaxRetries: 1, Timeout: time.Second})
	health, err := client.Health(context.Background())
	if err != nil || !health.Healthy || health.Version != "3.4.5" || health.ProtocolVersion != "2" || health.MaxConcurrent != 1 || health.WindowSize != 16 || health.CompletedTasks != 2 {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	resp, err := client.SubmitTask(context.Background(), &SubmitTaskRequest{FileName: "a.pdf", FileContent: []byte("source")})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.TaskID != "task-1" || !bytes.Contains(taskBody, []byte(`name="files"`)) || !bytes.Contains(taskBody, []byte("pipeline")) || !bytes.Contains(taskBody, []byte("response_format_zip")) {
		t.Fatalf("unexpected task response/body: %#v %s", resp, taskBody)
	}
	status, err := client.QueryTaskStatusAt(context.Background(), resp.TaskID, resp.StatusURL)
	if err != nil || status.Status != "completed" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	var out bytes.Buffer
	if err := client.downloadURL(context.Background(), resp.ResultURL, &out, false, resp.TaskID); err != nil || out.String() != "zip-bytes" {
		t.Fatalf("download=%q err=%v", out.String(), err)
	}
}

func TestRetryClassification(t *testing.T) {
	var calls int32
	client, _ := testClient(t, MinerUModeInternal, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"message":"try later"}`)
			return
		}
		http.NotFound(w, r)
	}))
	client.cfg.MaxRetries = 2
	_, err := client.Health(context.Background())
	if err == nil || atomic.LoadInt32(&calls) != 2 || !IsRetryableMinerUError(err) {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	var me *MinerUError
	if !errors.As(err, &me) || me.Class != RetryClassTransient {
		t.Fatalf("classification=%#v", me)
	}
}

func TestExtractZIPRejectsTraversalAndSymlinks(t *testing.T) {
	makeZip := func(name string, mode os.FileMode) *bytes.Reader {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("payload"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(b.Bytes())
	}
	dir := t.TempDir()
	traversal := makeZip("../../escape.txt", 0o600)
	if err := ExtractZIP(traversal, int64(traversal.Len()), filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected traversal rejection")
	}
	symlink := makeZip("link", os.ModeSymlink|0o777)
	if err := ExtractZIP(symlink, int64(symlink.Len()), filepath.Join(dir, "out2")); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestFindMarkdownCandidateAndProcessImages(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "doc.md")
	img := filepath.Join(dir, "images", "a b.png")
	if err := os.MkdirAll(filepath.Dir(img), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "![alt](images/a%20b.png)\n![remote](https://example.test/a.png)\n![oss](./oss/file/1)\n"
	if err := os.WriteFile(md, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := FindMarkdownCandidate(dir)
	if err != nil || candidate != md {
		t.Fatalf("candidate=%q err=%v", candidate, err)
	}
	var uploaded []string
	processed, err := ProcessMarkdownImages(content, md, func(p string) (string, error) {
		uploaded = append(uploaded, p)
		return "./oss/file/remote", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(uploaded) != 1 || uploaded[0] != img {
		t.Fatalf("uploaded=%v", uploaded)
	}
	if !strings.Contains(processed, "![alt](<./oss/file/remote>)") || !strings.Contains(processed, "https://example.test/a.png") {
		t.Fatalf("processed=%s", processed)
	}
}

func TestPollTaskStopsOnContext(t *testing.T) {
	client, _ := testClient(t, MinerUModeInternal, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"task_id":"t","status":"processing"}`)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.PollTask(ctx, "t", PollOptions{Interval: time.Millisecond, Timeout: time.Second})
	if err == nil {
		t.Fatal("expected poll timeout")
	}
}

func TestInternalHealthAcceptsStringProtocolVersion(t *testing.T) {
	var response internalHealthResponse
	if err := json.Unmarshal([]byte(`{"status":"healthy","version":"3.4.5","protocol_version":"v1"}`), &response); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if string(response.ProtocolVersion) != "v1" {
		t.Fatalf("protocol_version=%q", response.ProtocolVersion)
	}
}
