package adapter

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"maxkb-local-file-sync/internal/infra/credential"
)

const (
	onlineAPIPath     = "/api/v4"
	maxResponseBytes  = 2 << 20
	maxErrorBodyBytes = 64 << 10
	maxRetryDelay     = 10 * time.Minute
)

type mineruClient struct {
	cfg    MinerUConfig
	client *http.Client
	rand   *rand.Rand
	randMu sync.Mutex
}

// NewMinerUAdapter chooses the protocol-specific implementation while
// retaining the original constructor used by the application.
func NewMinerUAdapter(cfg MinerUConfig) MinerUAdapter {
	return newMinerUClient(cfg)
}

func newMinerUClient(cfg MinerUConfig) *mineruClient {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 60 * time.Minute
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 500 * time.Millisecond
	}
	if cfg.Mode == "" {
		cfg.Mode = MinerUModeOnline
	}
	return &mineruClient{
		cfg: cfg,
		client: &http.Client{
			// Request timeouts are applied per operation below. A client-wide
			// timeout would incorrectly abort large streaming uploads/downloads
			// after the ordinary-request timeout.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (c *mineruClient) SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	if req == nil {
		return nil, &MinerUError{Class: RetryClassParameter, Message: "MinerU request is nil"}
	}
	if strings.TrimSpace(req.FileName) == "" {
		return nil, &MinerUError{Class: RetryClassParameter, Message: "MinerU file name is required"}
	}
	if c.cfg.Mode == MinerUModeInternal {
		return c.submitInternal(ctx, req)
	}
	return c.submitOnline(ctx, req)
}

func (c *mineruClient) submitOnline(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	// The online protocol is the v4 file-url flow: create one batch per file,
	// PUT the source to the presigned URL, then persist the batch/file identity.
	model := req.ModelVersion
	if model == "" {
		if strings.EqualFold(filepath.Ext(req.FileName), ".html") || strings.EqualFold(filepath.Ext(req.FileName), ".htm") {
			model = "MinerU-HTML"
		} else {
			model = "vlm"
		}
	}
	dataID := req.AttemptID
	if dataID == "" {
		dataID = stableAttemptID(req.FileName, req.FileSize, req.FileContent)
	}
	payload := map[string]any{
		"files":         []map[string]string{{"name": req.FileName, "data_id": dataID}},
		"model_version": model,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal MinerU upload request: %w", err)
	}
	respBody, statusCode, err := c.doJSON(ctx, http.MethodPost, c.onlineURL("/file-urls/batch"), body, true)
	if err != nil {
		return nil, err
	}
	var result onlineBatchResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, protocolError(statusCode, "decode online MinerU batch response", err)
	}
	if result.Code != 0 {
		return nil, mineruBusinessError(statusCode, strconv.Itoa(result.Code), firstNonEmpty(result.Message, result.MessageAlt), "")
	}
	if strings.TrimSpace(result.Data.BatchID) == "" || len(result.Data.FileURLs) != 1 {
		return nil, &MinerUError{StatusCode: statusCode, Class: RetryClassProtocol, Message: "online MinerU response must contain one batch_id and exactly one file URL"}
	}
	uploadURL, err := validateHTTPURL(result.Data.FileURLs[0], "online MinerU presigned upload URL")
	if err != nil {
		return nil, err
	}
	reader, size, closeReader, err := requestSource(req)
	if err != nil {
		return nil, err
	}
	defer closeReader()
	if err := c.uploadPresigned(ctx, uploadURL, reader, size); err != nil {
		return nil, fmt.Errorf("upload source to online MinerU: %w", err)
	}
	return &SubmitTaskResponse{
		TaskID:      result.Data.BatchID,
		BatchID:     result.Data.BatchID,
		Status:      "pending",
		SubmittedAt: time.Now().UTC(),
	}, nil
}

func (c *mineruClient) submitInternal(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error) {
	reader, size, closeReader, err := requestSource(req)
	if err != nil {
		return nil, err
	}
	defer closeReader()
	options := mergeInternalOptions(c.cfg.Internal, req.Options)
	if strings.HasSuffix(strings.ToLower(options.Backend), "-http-client") && strings.TrimSpace(options.ServerURL) == "" {
		return nil, &MinerUError{Class: RetryClassParameter, Message: "server_url is required when backend ends with -http-client"}
	}
	fileName, err := safeMultipartFileName(req.FileName)
	if err != nil {
		return nil, err
	}
	response, statusCode, err := c.doMultipartStream(ctx, c.internalURL("/tasks"), reader, size, fileName, internalFormFields(options, req.OutputFormat), true, "")
	if err != nil {
		return nil, err
	}
	var result internalTaskResponse
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, protocolError(statusCode, "decode internal MinerU task response", err)
	}
	if statusCode != http.StatusAccepted {
		return nil, mineruBusinessError(statusCode, "", firstNonEmpty(result.Message, result.ErrorMessage), "")
	}
	if result.TaskID == "" || result.StatusURL == "" || result.ResultURL == "" {
		return nil, &MinerUError{StatusCode: statusCode, Class: RetryClassProtocol, Message: "internal MinerU response missing task_id, status_url or result_url"}
	}
	statusURL, err := c.validateInternalURL(result.StatusURL, "internal MinerU status URL")
	if err != nil {
		return nil, err
	}
	resultURL, err := c.validateInternalURL(result.ResultURL, "internal MinerU result URL")
	if err != nil {
		return nil, err
	}
	return &SubmitTaskResponse{TaskID: result.TaskID, Status: normalizeStatus(result.Status), SubmittedAt: time.Now().UTC(), StatusURL: statusURL, ResultURL: resultURL}, nil
}

func (c *mineruClient) QueryTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, &MinerUError{Class: RetryClassParameter, Message: "MinerU task ID is required"}
	}
	if c.cfg.Mode == MinerUModeInternal {
		// Internal task IDs are accepted as durable identifiers. The status URL
		// can be supplied by recovery-aware callers through QueryTaskStatusAt.
		return c.queryInternal(ctx, taskID, "")
	}
	return c.queryOnline(ctx, taskID)
}

// QueryTaskStatusAt is used by recovery code when the service returned an
// absolute status URL. It avoids reconstructing a URL from a task ID.
func (c *mineruClient) QueryTaskStatusAt(ctx context.Context, taskID, statusURL string) (*TaskStatusResponse, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, &MinerUError{Class: RetryClassParameter, Message: "MinerU task ID is required"}
	}
	if c.cfg.Mode == MinerUModeInternal {
		return c.queryInternal(ctx, taskID, statusURL)
	}
	return c.queryOnline(ctx, taskID)
}

func (c *mineruClient) queryOnline(ctx context.Context, batchID string) (*TaskStatusResponse, error) {
	body, statusCode, err := c.doJSON(ctx, http.MethodGet, c.onlineURL("/extract-results/batch/"+url.PathEscape(batchID)), nil, true)
	if err != nil {
		return nil, err
	}
	var result onlineExtractResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, protocolError(statusCode, "decode online MinerU status response", err)
	}
	if result.Code != 0 {
		return nil, mineruBusinessError(statusCode, strconv.Itoa(result.Code), firstNonEmpty(result.Message, result.MessageAlt), batchID)
	}
	extractResults := result.Data.ExtractResults
	if len(extractResults) == 0 {
		extractResults = result.Data.ExtractResultsAlt
	}
	for _, item := range extractResults {
		if item.BatchID == "" || item.BatchID == batchID {
			status := normalizeStatus(firstNonEmpty(item.Status, item.State))
			resultURL := strings.TrimSpace(item.FullZipURL)
			if resultURL != "" {
				var err error
				resultURL, err = validateHTTPURL(resultURL, "online MinerU result URL")
				if err != nil {
					return nil, err
				}
			}
			if err := validateMinerUTaskStatusForTask(status, batchID); err != nil {
				return nil, err
			}
			return &TaskStatusResponse{TaskID: batchID, Status: status, Progress: item.Progress, ResultURL: resultURL, ErrorMessage: firstNonEmpty(item.ErrorMessage, item.Message), UpdatedAt: parseTime(item.UpdateTime)}, nil
		}
	}
	return &TaskStatusResponse{TaskID: batchID, Status: "pending"}, nil
}

func (c *mineruClient) queryInternal(ctx context.Context, taskID, statusURL string) (*TaskStatusResponse, error) {
	if statusURL == "" {
		statusURL = c.internalURL("/tasks/" + url.PathEscape(taskID))
	}
	statusURL, err := c.validateInternalURL(statusURL, "internal MinerU status URL")
	if err != nil {
		return nil, err
	}
	body, statusCode, err := c.doJSON(ctx, http.MethodGet, statusURL, nil, c.authAllowedForURL(statusURL))
	if err != nil {
		return nil, err
	}
	var result internalTaskResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, protocolError(statusCode, "decode internal MinerU status response", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, mineruBusinessError(statusCode, "", result.Message, taskID)
	}
	status := normalizeStatus(result.Status)
	if err := validateMinerUTaskStatusForTask(status, taskID); err != nil {
		return nil, err
	}
	resultURL := strings.TrimSpace(result.ResultURL)
	if resultURL != "" {
		resultURL, err = c.validateInternalURL(resultURL, "internal MinerU result URL")
		if err != nil {
			return nil, err
		}
	}
	return &TaskStatusResponse{TaskID: firstNonEmpty(result.TaskID, taskID), Status: status, StatusURL: statusURL, Progress: result.Progress, ResultURL: resultURL, ErrorMessage: firstNonEmpty(result.ErrorMessage, result.Message), UpdatedAt: parseTime(result.UpdateTime), QueuedAhead: result.QueuedAhead}, nil
}

func (c *mineruClient) PollTask(ctx context.Context, taskID string, opts PollOptions) (*TaskStatusResponse, error) {
	return c.PollTaskAt(ctx, taskID, "", opts)
}

// PollTaskAt is the recovery-aware variant of PollTask. For the internal
// protocol statusURL is the exact URL returned by task creation; retaining it
// avoids guessing an endpoint after a restart.
func (c *mineruClient) PollTaskAt(ctx context.Context, taskID, statusURL string, opts PollOptions) (*TaskStatusResponse, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = c.cfg.PollInterval
	}
	deadline := opts.Timeout
	if deadline <= 0 {
		deadline = c.cfg.TaskTimeout
	}
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		status, err := c.QueryTaskStatusAt(pollCtx, taskID, statusURL)
		if err != nil {
			return nil, err
		}
		switch normalizeStatus(status.Status) {
		case "completed", "success", "done":
			return status, nil
		case "failed", "failure", "error", "cancelled", "canceled":
			if status.ErrorMessage == "" {
				status.ErrorMessage = "MinerU task failed"
			}
			return status, &MinerUError{StatusCode: http.StatusUnprocessableEntity, Class: RetryClassNone, Message: credential.Sanitize(status.ErrorMessage), TaskID: taskID}
		case "waiting-file", "uploading", "pending", "processing", "queued", "running", "converting":
			// These are documented MinerU in-progress states. Keep polling while
			// preserving the raw state for diagnostics and UI display.
		default:
			return nil, &MinerUError{Class: RetryClassProtocol, Message: fmt.Sprintf("MinerU returned an unsupported task status: %q", normalizeStatus(status.Status)), TaskID: taskID}
		}
		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			if errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("poll MinerU task %s: %w", taskID, pollCtx.Err())
			}
			return nil, pollCtx.Err()
		case <-timer.C:
		}
	}
}

func (c *mineruClient) DownloadResult(ctx context.Context, taskID string) ([]byte, error) {
	var output bytes.Buffer
	if err := c.DownloadResultTo(ctx, taskID, &output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (c *mineruClient) DownloadResultTo(ctx context.Context, taskID string, dst io.Writer) error {
	return c.DownloadResultToAt(ctx, taskID, "", "", dst)
}

// DownloadResultToAt uses persisted status/result URLs when available. A
// resultURL supplied by a completed status response takes precedence over a
// stale creation-time URL.
func (c *mineruClient) DownloadResultToAt(ctx context.Context, taskID, statusURL, resultURL string, dst io.Writer) error {
	if dst == nil {
		return &MinerUError{Class: RetryClassParameter, Message: "download destination is nil"}
	}
	status, err := c.QueryTaskStatusAt(ctx, taskID, statusURL)
	if err != nil {
		return err
	}
	if !isCompletedStatus(status.Status) {
		return &MinerUError{Class: RetryClassNone, Message: fmt.Sprintf("MinerU task is not completed: %s", status.Status), TaskID: taskID}
	}
	resultURL = firstNonEmpty(status.ResultURL, resultURL)
	if resultURL == "" {
		return &MinerUError{Class: RetryClassProtocol, Message: "MinerU completed task did not include a result URL", TaskID: taskID}
	}
	withAuth := c.cfg.Mode == MinerUModeInternal && c.cfg.APIKey != "" && c.authAllowedForURL(resultURL)
	return c.downloadURL(ctx, resultURL, dst, withAuth, taskID)
}

func (c *mineruClient) CancelTask(ctx context.Context, taskID string) error {
	// The locked internal protocol explicitly does not define cancellation. Do
	// not invent DELETE behavior that could cancel a task on an unrelated API.
	if c.cfg.Mode == MinerUModeInternal || c.cfg.Mode == MinerUModeOnline {
		return ErrUnsupportedCancellation
	}
	return nil
}

func (c *mineruClient) Ping(ctx context.Context) error {
	_, err := c.Health(ctx)
	return err
}

func (c *mineruClient) Health(ctx context.Context) (*HealthResult, error) {
	if c.cfg.Mode == MinerUModeInternal {
		body, statusCode, err := c.doJSON(ctx, http.MethodGet, c.internalURL("/health"), nil, c.cfg.APIKey != "")
		if err != nil {
			return nil, err
		}
		var response struct {
			Status                string `json:"status"`
			ProtocolVersion       string `json:"protocol_version"`
			MaxConcurrentRequests int    `json:"max_concurrent_requests"`
			ProcessingWindowSize  int    `json:"processing_window_size"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, protocolError(statusCode, "decode internal MinerU health response", err)
		}
		if statusCode < 200 || statusCode >= 300 || !strings.EqualFold(response.Status, "healthy") {
			return nil, &MinerUError{StatusCode: statusCode, Class: RetryClassNone, Message: "internal MinerU health check is not healthy"}
		}
		return &HealthResult{Healthy: true, ProtocolVersion: response.ProtocolVersion, MaxConcurrent: response.MaxConcurrentRequests, WindowSize: response.ProcessingWindowSize}, nil
	}
	// The online API does not define a dedicated health endpoint. Probe the
	// upload-url endpoint with one syntactically valid file entry, but do not
	// upload the returned presigned URL. An empty files array is rejected by
	// the service before authentication/reachability can be established.
	probeID := stableAttemptID("connection-probe.pdf", 0, []byte(time.Now().UTC().Format(time.RFC3339Nano)))
	probePayload, err := json.Marshal(map[string]any{
		"files":         []map[string]string{{"name": "connection-probe.pdf", "data_id": probeID}},
		"model_version": "vlm",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal online MinerU health probe: %w", err)
	}
	body, statusCode, err := c.doJSON(ctx, http.MethodPost, c.onlineURL("/file-urls/batch"), probePayload, true)
	if err != nil {
		return nil, err
	}
	var response onlineBatchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, protocolError(statusCode, "decode online MinerU health response", err)
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return nil, mineruBusinessError(statusCode, "", response.Message, "")
	}
	if statusCode < 200 || statusCode >= 300 || response.Code != 0 {
		return nil, mineruBusinessError(statusCode, strconv.Itoa(response.Code), firstNonEmpty(response.Message, response.MessageAlt), "")
	}
	return &HealthResult{Healthy: true}, nil
}

func (c *mineruClient) uploadPresigned(ctx context.Context, target string, reader io.Reader, size int64) error {
	return c.doStream(ctx, http.MethodPut, target, reader, size, "", false, "")
}

func (c *mineruClient) downloadURL(ctx context.Context, target string, dst io.Writer, withAuth bool, taskID string) error {
	return c.doStream(ctx, http.MethodGet, target, nil, 0, "", withAuth, taskID, dst)
}

// doMultipartStream writes the multipart envelope through an io.Pipe. The
// source file is never assembled into a second in-memory buffer. A retry is
// possible only when the caller supplied a seekable reader; otherwise the
// first transient failure is returned as non-replayable.
func (c *mineruClient) doMultipartStream(ctx context.Context, target string, reader io.Reader, size int64, fileName string, fields map[string]string, withAuth bool, taskID string) ([]byte, int, error) {
	if _, err := validateHTTPURL(target, "MinerU multipart URL"); err != nil {
		return nil, 0, err
	}
	streamCtx, cancel := c.streamContext(ctx)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			seeker, ok := reader.(io.Seeker)
			if !ok {
				return nil, errorStatus(lastErr), nonReplayableStreamError(lastErr)
			}
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, 0, &MinerUError{Class: RetryClassNone, Message: "rewind MinerU multipart source: " + credential.Sanitize(err.Error()), TaskID: taskID}
			}
		}

		pipeReader, pipeWriter := io.Pipe()
		multipartWriter := multipart.NewWriter(pipeWriter)
		writeDone := make(chan error, 1)
		go func() {
			part, err := multipartWriter.CreateFormFile("file", fileName)
			if err == nil {
				_, err = io.Copy(part, reader)
			}
			if err == nil {
				for key, value := range fields {
					if err = multipartWriter.WriteField(key, value); err != nil {
						break
					}
				}
			}
			if err == nil {
				err = multipartWriter.Close()
			} else {
				_ = pipeWriter.CloseWithError(err)
			}
			if err == nil {
				err = pipeWriter.Close()
			}
			writeDone <- err
		}()

		req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, target, pipeReader)
		if err != nil {
			_ = pipeReader.CloseWithError(err)
			return nil, 0, fmt.Errorf("create internal MinerU multipart request: %w", err)
		}
		req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
		req.Header.Set("Accept", "application/json")
		if withAuth && c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
		if size >= 0 {
			// The multipart envelope adds a variable boundary/header overhead;
			// leave ContentLength unknown so net/http uses chunked framing.
			req.ContentLength = -1
		}

		resp, requestErr := c.client.Do(req)
		if requestErr != nil {
			_ = pipeReader.CloseWithError(requestErr)
			_ = <-writeDone
			lastErr = classifyRequestTransportError(requestErr, streamCtx, taskID)
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			responseBody, readErr := readLimitedAndClose(resp.Body, maxResponseBytes)
			_ = pipeReader.Close()
			writerErr := <-writeDone
			if readErr != nil {
				lastErr = classifyRequestTransportError(readErr, streamCtx, taskID)
			} else if writerErr != nil {
				lastErr = classifyRequestTransportError(writerErr, streamCtx, taskID)
			} else {
				return responseBody, resp.StatusCode, nil
			}
		} else {
			responseBody, readErr := readTruncatedAndClose(resp.Body, maxErrorBodyBytes)
			_ = pipeReader.Close()
			writerErr := <-writeDone
			if readErr != nil {
				lastErr = classifyRequestTransportError(readErr, streamCtx, taskID)
			} else if writerErr != nil {
				lastErr = classifyRequestTransportError(writerErr, streamCtx, taskID)
			} else {
				lastErr = mineruHTTPError(resp.StatusCode, responseBody, taskID)
				if me, ok := lastErr.(*MinerUError); ok {
					me.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
				}
			}
		}
		if !shouldRetry(lastErr) || attempt == c.cfg.MaxRetries-1 {
			break
		}
		if _, ok := reader.(io.Seeker); !ok {
			return nil, errorStatus(lastErr), nonReplayableStreamError(lastErr)
		}
		if err := c.sleepBackoff(ctx, attempt, lastErr); err != nil {
			return nil, 0, err
		}
	}
	return nil, errorStatus(lastErr), lastErr
}

func nonReplayableStreamError(err error) error {
	message := "MinerU streaming request cannot be replayed after a transient failure"
	if err != nil {
		message += ": " + safeTransportErrorMessage(err)
	}
	return &MinerUError{Class: RetryClassNone, Message: message}
}

// doStream retries only before a response is received or for a response that
// is known to have failed. A failed PUT body is recreated by the caller only
// when the source is seekable; non-seekable streams are intentionally not
// replayed after a response/error to avoid corrupting an upload.
func (c *mineruClient) doStream(ctx context.Context, method, target string, reader io.Reader, size int64, contentType string, withAuth bool, taskID string, destinations ...io.Writer) error {
	if _, err := validateHTTPURL(target, "MinerU streaming URL"); err != nil {
		return err
	}
	streamCtx, cancel := c.streamContext(ctx)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		if attempt > 0 && reader != nil {
			seeker, ok := reader.(io.Seeker)
			if !ok {
				return nonReplayableStreamError(lastErr)
			}
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("rewind MinerU stream: %w", err)
			}
		}
		req, err := http.NewRequestWithContext(streamCtx, method, target, reader)
		if err != nil {
			return fmt.Errorf("create MinerU streaming request: %w", err)
		}
		if size >= 0 {
			req.ContentLength = size
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if withAuth && c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = classifyRequestTransportError(err, ctx, taskID)
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(destinations) == 0 {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				return nil
			}
			_, copyErr := io.Copy(destinations[0], resp.Body)
			closeErr := resp.Body.Close()
			if copyErr != nil {
				return &MinerUError{Class: RetryClassNone, Message: "stream MinerU result: " + credential.Sanitize(copyErr.Error()), TaskID: taskID}
			}
			if closeErr != nil {
				return &MinerUError{Class: RetryClassNone, Message: "close MinerU result: " + credential.Sanitize(closeErr.Error()), TaskID: taskID}
			}
			return nil
		} else {
			responseBody, readErr := readTruncatedAndClose(resp.Body, maxErrorBodyBytes)
			if readErr != nil {
				lastErr = classifyRequestTransportError(readErr, streamCtx, taskID)
			} else {
				lastErr = mineruHTTPError(resp.StatusCode, responseBody, taskID)
				if me, ok := lastErr.(*MinerUError); ok {
					me.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
				}
			}
		}
		if !shouldRetry(lastErr) || attempt == c.cfg.MaxRetries-1 {
			break
		}
		if err := c.sleepBackoff(ctx, attempt, lastErr); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *mineruClient) doJSON(ctx context.Context, method, target string, payload []byte, withAuth bool) ([]byte, int, error) {
	return c.doBytes(ctx, method, target, payload, "application/json", withAuth)
}

func (c *mineruClient) doBytes(ctx context.Context, method, target string, payload []byte, contentType string, withAuth bool) ([]byte, int, error) {
	if _, err := validateHTTPURL(target, "MinerU request URL"); err != nil {
		return nil, 0, err
	}
	var lastErr error
	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		requestCtx, cancel := c.requestContext(ctx)
		req, err := http.NewRequestWithContext(requestCtx, method, target, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return nil, 0, fmt.Errorf("create MinerU request: %w", err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Accept", "application/json")
		if withAuth && c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			lastErr = classifyRequestTransportError(err, ctx, "")
		} else {
			body, readErr := readLimitedAndClose(resp.Body, maxResponseBytes)
			cancel()
			if readErr != nil {
				lastErr = classifyRequestTransportError(readErr, ctx, "")
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return body, resp.StatusCode, nil
			} else {
				lastErr = mineruHTTPError(resp.StatusCode, body, "")
				if me, ok := lastErr.(*MinerUError); ok {
					me.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
				}
			}
		}
		if !shouldRetry(lastErr) || attempt == c.cfg.MaxRetries-1 {
			break
		}
		if err := c.sleepBackoff(ctx, attempt, lastErr); err != nil {
			return nil, 0, err
		}
	}
	return nil, errorStatus(lastErr), lastErr
}

func (c *mineruClient) sleepBackoff(ctx context.Context, attempt int, err error) error {
	delay := c.cfg.RetryBaseDelay
	for i := 0; i < attempt && delay < maxRetryDelay/2; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	if me, ok := err.(*MinerUError); ok && me.RetryAfter > delay {
		delay = minDuration(me.RetryAfter, maxRetryDelay)
	}
	c.randMu.Lock()
	jitter := time.Duration(c.rand.Int63n(int64(maxDuration(delay/4, time.Millisecond))))
	c.randMu.Unlock()
	timer := time.NewTimer(delay + jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *mineruClient) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.cfg.Timeout)
}

func (c *mineruClient) streamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.cfg.TaskTimeout)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (c *mineruClient) onlineURL(endpoint string) string {
	return c.cfg.BaseURL + onlineAPIPath + endpoint
}
func (c *mineruClient) internalURL(endpoint string) string { return c.cfg.BaseURL + endpoint }

func requestSource(req *SubmitTaskRequest) (io.Reader, int64, func(), error) {
	if req == nil {
		return nil, 0, func() {}, &MinerUError{Class: RetryClassParameter, Message: "MinerU request is nil"}
	}
	if strings.TrimSpace(req.FilePath) != "" {
		f, err := os.Open(req.FilePath)
		if err != nil {
			return nil, 0, func() {}, fmt.Errorf("open MinerU source: %w", err)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, 0, func() {}, fmt.Errorf("stat MinerU source: %w", err)
		}
		if !info.Mode().IsRegular() {
			_ = f.Close()
			return nil, 0, func() {}, &MinerUError{Class: RetryClassParameter, Message: "MinerU source is not a regular file"}
		}
		size := req.FileSize
		if size < 0 {
			size = info.Size()
		}
		return f, size, func() { _ = f.Close() }, nil
	}
	if req.FileReader != nil {
		size := req.FileSize
		if size < 0 {
			size = -1
		}
		return req.FileReader, size, func() {}, nil
	}
	return bytes.NewReader(req.FileContent), int64(len(req.FileContent)), func() {}, nil
}

// requestReader is retained for package-local compatibility with older tests.
func requestReader(req *SubmitTaskRequest) (io.Reader, int64, error) {
	r, size, closeReader, err := requestSource(req)
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(req.FilePath) != "" {
		closeReader()
		return nil, 0, fmt.Errorf("file path requires requestSource")
	}
	return r, size, nil
}

func stableAttemptID(name string, size int64, content []byte) string {
	// For legacy []byte callers the content hash makes retries stable without
	// persisting document contents. Streaming callers should provide AttemptID
	// because the reader is intentionally not buffered here.
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%s-%d-%s", filepath.Base(name), size, hex.EncodeToString(hash[:8]))
}

type onlineBatchResponse struct {
	Code       int    `json:"code"`
	Message    string `json:"msg"`
	MessageAlt string `json:"message"`
	Data       struct {
		BatchID  string   `json:"batch_id"`
		FileURLs []string `json:"file_urls"`
	} `json:"data"`
}

type onlineExtractResponse struct {
	Code       int    `json:"code"`
	Message    string `json:"msg"`
	MessageAlt string `json:"message"`
	Data       struct {
		ExtractResults []struct {
			BatchID      string `json:"batch_id"`
			Status       string `json:"status"`
			State        string `json:"state"`
			Progress     int    `json:"progress"`
			FullZipURL   string `json:"full_zip_url"`
			ErrorMessage string `json:"error_message"`
			Message      string `json:"message"`
			UpdateTime   string `json:"update_time"`
		} `json:"extract_result"`
		ExtractResultsAlt []struct {
			BatchID      string `json:"batch_id"`
			Status       string `json:"status"`
			State        string `json:"state"`
			Progress     int    `json:"progress"`
			FullZipURL   string `json:"full_zip_url"`
			ErrorMessage string `json:"error_message"`
			Message      string `json:"message"`
			UpdateTime   string `json:"update_time"`
		} `json:"extract_results"`
	} `json:"data"`
}

type internalTaskResponse struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	StatusURL    string `json:"status_url"`
	ResultURL    string `json:"result_url"`
	ErrorMessage string `json:"error_message"`
	Message      string `json:"message"`
	UpdateTime   string `json:"update_time"`
	QueuedAhead  *int   `json:"queued_ahead"`
}

func mergeInternalOptions(base, override InternalMinerUOptions) InternalMinerUOptions {
	result := base
	if override.Backend != "" {
		result.Backend = override.Backend
	}
	if override.Effort != "" {
		result.Effort = override.Effort
	}
	if override.ParseMethod != "" {
		result.ParseMethod = override.ParseMethod
	}
	if override.Language != "" {
		result.Language = override.Language
	}
	if override.ServerURL != "" {
		result.ServerURL = override.ServerURL
	}
	if override.StartPageID != 0 {
		result.StartPageID = override.StartPageID
	}
	if override.FormulaEnable != nil {
		result.FormulaEnable = override.FormulaEnable
	}
	if override.TableEnable != nil {
		result.TableEnable = override.TableEnable
	}
	if override.ImageAnalysis != nil {
		result.ImageAnalysis = override.ImageAnalysis
	}
	if override.ReturnMD != nil {
		result.ReturnMD = override.ReturnMD
	}
	if override.ReturnMiddleJSON != nil {
		result.ReturnMiddleJSON = override.ReturnMiddleJSON
	}
	if override.ReturnModelOutput != nil {
		result.ReturnModelOutput = override.ReturnModelOutput
	}
	if override.ReturnContentList != nil {
		result.ReturnContentList = override.ReturnContentList
	}
	if override.ReturnImages != nil {
		result.ReturnImages = override.ReturnImages
	}
	if override.ResponseFormatZIP != nil {
		result.ResponseFormatZIP = override.ResponseFormatZIP
	}
	if override.ReturnOriginalFile != nil {
		result.ReturnOriginalFile = override.ReturnOriginalFile
	}
	if result.Backend == "" {
		result.Backend = "hybrid-engine"
	}
	if result.Effort == "" {
		result.Effort = "medium"
	}
	if result.ParseMethod == "" {
		result.ParseMethod = "auto"
	}
	if result.Language == "" {
		result.Language = "ch"
	}
	result.FormulaEnable = boolPtrDefault(result.FormulaEnable, true)
	result.TableEnable = boolPtrDefault(result.TableEnable, true)
	result.ImageAnalysis = boolPtrDefault(result.ImageAnalysis, true)
	result.ReturnMD = boolPtrDefault(result.ReturnMD, true)
	result.ReturnMiddleJSON = boolPtrDefault(result.ReturnMiddleJSON, false)
	result.ReturnModelOutput = boolPtrDefault(result.ReturnModelOutput, false)
	result.ReturnContentList = boolPtrDefault(result.ReturnContentList, false)
	result.ReturnImages = boolPtrDefault(result.ReturnImages, true)
	result.ResponseFormatZIP = boolPtrDefault(result.ResponseFormatZIP, true)
	result.ReturnOriginalFile = boolPtrDefault(result.ReturnOriginalFile, false)
	return result
}

func boolPtrDefault(v *bool, d bool) *bool {
	if v != nil {
		return v
	}
	return &d
}

func internalFormFields(o InternalMinerUOptions, outputFormat string) map[string]string {
	fields := map[string]string{
		"backend": o.Backend, "effort": o.Effort, "parse_method": o.ParseMethod,
		"language": o.Language, "formula_enable": strconv.FormatBool(*o.FormulaEnable),
		"table_enable": strconv.FormatBool(*o.TableEnable), "image_analysis": strconv.FormatBool(*o.ImageAnalysis),
		"start_page_id": strconv.Itoa(o.StartPageID), "return_md": strconv.FormatBool(*o.ReturnMD),
		"return_middle_json": strconv.FormatBool(*o.ReturnMiddleJSON), "return_model_output": strconv.FormatBool(*o.ReturnModelOutput),
		"return_content_list": strconv.FormatBool(*o.ReturnContentList), "return_images": strconv.FormatBool(*o.ReturnImages),
		"response_format_zip": strconv.FormatBool(*o.ResponseFormatZIP), "return_original_file": strconv.FormatBool(*o.ReturnOriginalFile),
	}
	if outputFormat != "" {
		fields["output_format"] = outputFormat
	}
	if o.ServerURL != "" {
		fields["server_url"] = o.ServerURL
	}
	return fields
}

func normalizeStatus(status string) string { return strings.ToLower(strings.TrimSpace(status)) }

func validateMinerUTaskStatus(status string) error {
	return validateMinerUTaskStatusForTask(status, "")
}

func validateMinerUTaskStatusForTask(status, taskID string) error {
	normalized := normalizeStatus(status)
	switch normalized {
	case "waiting-file", "uploading", "pending", "processing", "queued", "running", "converting",
		"completed", "success", "done", "failed", "failure", "error", "cancelled", "canceled":
		return nil
	case "":
		return &MinerUError{Class: RetryClassProtocol, Message: "MinerU response did not contain a task status", TaskID: taskID}
	default:
		return &MinerUError{Class: RetryClassProtocol, Message: fmt.Sprintf("MinerU returned an unsupported task status: %q", normalized), TaskID: taskID}
	}
}

func isCompletedStatus(status string) bool {
	switch normalizeStatus(status) {
	case "completed", "success", "done":
		return true
	}
	return false
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func readLimitedAndClose(r io.ReadCloser, max int64) ([]byte, error) {
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, &MinerUError{Class: RetryClassProtocol, Message: "MinerU response exceeds the adapter limit"}
	}
	return data, nil
}

func readTruncatedAndClose(r io.ReadCloser, max int64) ([]byte, error) {
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, max))
}

func validateHTTPURL(raw, label string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", &MinerUError{Class: RetryClassProtocol, Message: label + " is invalid"}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return "", &MinerUError{Class: RetryClassProtocol, Message: label + " must be an absolute HTTP(S) URL without credentials or fragments"}
	}
	return u.String(), nil
}

func safeMultipartFileName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(name)
	if name == "" || base == "." || base == ".." || base == "" || strings.IndexFunc(base, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", &MinerUError{Class: RetryClassParameter, Message: "MinerU file name is invalid"}
	}
	return base, nil
}

func (c *mineruClient) validateInternalURL(raw, label string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		base, err := url.Parse(c.cfg.BaseURL)
		if err != nil || base.Scheme == "" || base.Host == "" {
			return "", &MinerUError{Class: RetryClassProtocol, Message: "internal MinerU base URL is invalid"}
		}
		raw = (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: raw}).String()
	}
	return validateHTTPURL(raw, label)
}

func (c *mineruClient) authAllowedForURL(target string) bool {
	if c.cfg.APIKey == "" {
		return false
	}
	targetURL, err := url.Parse(target)
	baseURL, baseErr := url.Parse(c.cfg.BaseURL)
	if err != nil || baseErr != nil || targetURL.Scheme == "" || baseURL.Scheme == "" {
		return false
	}
	return strings.EqualFold(targetURL.Scheme, baseURL.Scheme) && strings.EqualFold(targetURL.Host, baseURL.Host) && targetURL.User == nil
}

func classifyRequestTransportError(err error, ctx context.Context, taskID string) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &MinerUError{Class: RetryClassTransient, Message: "MinerU request timed out", TaskID: taskID}
	}
	return classifyTransportError(err, taskID)
}

func protocolError(status int, prefix string, err error) error {
	return &MinerUError{StatusCode: status, Class: RetryClassProtocol, Message: fmt.Sprintf("%s: %v", prefix, err)}
}
func mineruBusinessError(status int, code, message, taskID string) error {
	if message == "" {
		message = http.StatusText(status)
	}
	return &MinerUError{StatusCode: status, Code: code, Class: classifyStatus(status), Message: credential.Sanitize(message), TaskID: taskID}
}
func mineruHTTPError(status int, body []byte, taskID string) error {
	message := strings.TrimSpace(string(body))
	if len(message) > 1024 {
		message = message[:1024]
	}
	return &MinerUError{StatusCode: status, Class: classifyStatus(status), Message: credential.Sanitize(message), TaskID: taskID, RetryAfter: retryAfter(body)}
}
func classifyTransportError(err error, taskID string) error {
	if err == nil {
		return nil
	}
	class := RetryClassNone
	if isTransientTransportError(err) {
		class = RetryClassTransient
	}
	return &MinerUError{Class: class, Message: safeTransportErrorMessage(err), TaskID: taskID}
}

func safeTransportErrorMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return credential.Sanitize(urlErr.Err.Error())
	}
	return credential.Sanitize(err.Error())
}

func isTransientTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
func classifyStatus(status int) RetryClass {
	switch {
	case status == 401:
		return RetryClassAuth
	case status == 403:
		return RetryClassPermission
	case status == 400 || status == 422:
		return RetryClassParameter
	case status == 415:
		return RetryClassUnsupported
	case status == 408 || status == 425 || status == 429 || status >= 500:
		return RetryClassTransient
	default:
		return RetryClassNone
	}
}
func shouldRetry(err error) bool { var me *MinerUError; return errors.As(err, &me) && me.IsRetryable() }
func errorStatus(err error) int {
	var me *MinerUError
	if errors.As(err, &me) {
		return me.StatusCode
	}
	return 0
}
func retryAfter(body []byte) time.Duration {
	var response struct {
		RetryAfter json.RawMessage `json:"retry_after"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.RetryAfter) == 0 {
		return 0
	}
	var seconds float64
	if json.Unmarshal(response.RetryAfter, &seconds) == nil && seconds >= 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	var value string
	if json.Unmarshal(response.RetryAfter, &value) == nil {
		if duration, err := time.ParseDuration(value); err == nil && duration >= 0 {
			return duration
		}
		return parseRetryAfter(value)
	}
	return 0
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

// ExtractZIP safely extracts a MinerU result archive. It rejects absolute paths,
// drive-letter paths, traversal components, symlink entries, and files that
// escape the destination after cleaning.
func ExtractZIP(r io.ReaderAt, size int64, destination string) error {
	if r == nil {
		return errors.New("ZIP reader is required")
	}
	if destination == "" || size < 0 {
		return errors.New("ZIP destination or size is invalid")
	}
	base, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve ZIP destination: %w", err)
	}
	if err := ensureSafeRoot(base); err != nil {
		return fmt.Errorf("create ZIP destination: %w", err)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("open MinerU ZIP: %w", err)
	}
	baseWithSep := filepath.Clean(base) + string(os.PathSeparator)
	for _, file := range zr.File {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		if err := validateZIPEntryName(name); err != nil {
			return fmt.Errorf("unsafe ZIP entry path %q: %w", file.Name, err)
		}
		if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || isWindowsAbsoluteZIPPath(name) {
			return fmt.Errorf("unsafe ZIP entry path: %q", file.Name)
		}
		clean := path.Clean(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("ZIP path traversal detected: %q", file.Name)
		}
		if err := validateZIPComponents(name); err != nil {
			return fmt.Errorf("unsafe ZIP entry path %q: %w", file.Name, err)
		}
		target := filepath.Join(base, filepath.FromSlash(clean))
		absTarget, err := filepath.Abs(target)
		if err != nil || (absTarget != base && !strings.HasPrefix(absTarget, baseWithSep)) {
			return fmt.Errorf("ZIP entry escapes destination: %q", file.Name)
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink entries are not allowed in MinerU ZIP: %q", file.Name)
		}
		if file.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if err := ensureSafeDirectory(base, absTarget); err != nil {
				return err
			}
			continue
		}
		if err := ensureSafeDirectory(base, filepath.Dir(absTarget)); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return fmt.Errorf("open ZIP entry %q: %w", file.Name, err)
		}
		out, err := os.OpenFile(absTarget, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			in.Close()
			return fmt.Errorf("create ZIP output %q: %w", file.Name, err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := errors.Join(in.Close(), out.Close())
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("extract ZIP entry %q: %w", file.Name, firstError(copyErr, closeErr))
		}
	}
	return nil
}

func isWindowsAbsoluteZIPPath(name string) bool {
	// Reject both absolute (C:\foo) and drive-relative (C:foo) paths. The
	// latter is not absolute on Windows, but it is still outside the ZIP
	// extraction namespace and is unsafe to interpret cross-platform.
	return len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
}

func ensureSafeDirectory(base, directory string) error {
	base = filepath.Clean(base)
	directory = filepath.Clean(directory)
	rel, err := filepath.Rel(base, directory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("directory escapes ZIP destination: %q", directory)
	}
	current := base
	parts := []string{}
	if rel != "." {
		parts = strings.Split(rel, string(os.PathSeparator))
	}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create ZIP directory %q: %w", current, err)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return fmt.Errorf("inspect ZIP directory %q: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink directory is not allowed in ZIP destination: %q", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("ZIP destination component is not a directory: %q", current)
		}
	}
	return nil
}

func ensureSafeRoot(base string) error {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(base)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("ZIP destination must not be a symlink")
	}
	if !info.IsDir() {
		return errors.New("ZIP destination is not a directory")
	}
	return nil
}

func validateZIPEntryName(name string) error {
	if strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("control characters are not allowed")
	}
	return validateZIPComponents(name)
}

func validateZIPComponents(name string) error {
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return errors.New("parent path component is not allowed")
		}
	}
	return nil
}

func firstError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

// FindMarkdownCandidate returns the unique Markdown main document. Common
// metadata/asset markdown files are ignored; multiple remaining candidates are
// rejected rather than guessed.
func FindMarkdownCandidate(root string) (string, error) {
	var candidates []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		lower := strings.ToLower(filepath.ToSlash(p))
		if strings.Contains(lower, "/images/") || strings.Contains(lower, "/assets/") || strings.Contains(lower, "/_meta/") {
			return nil
		}
		candidates = append(candidates, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("expected exactly one Markdown candidate, found %d", len(candidates))
	}
	return candidates[0], nil
}

// ProcessMarkdownImages parses Markdown image destinations without replacing
// arbitrary text. upload is called only for local references and must return
// the remote OSS path. HTTP(S), data URLs, and already-processed OSS paths are
// left intact. The returned Markdown keeps alt text and optional titles.
func ProcessMarkdownImages(content, markdownPath string, upload func(path string) (string, error)) (string, error) {
	if upload == nil {
		return "", errors.New("Markdown image uploader is required")
	}
	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		processed, err := processMarkdownLine(line, markdownPath, upload)
		if err != nil {
			return "", err
		}
		lines[i] = processed
	}
	return strings.Join(lines, ""), nil
}

func processMarkdownLine(line, markdownPath string, upload func(string) (string, error)) (string, error) {
	var out strings.Builder
	for pos := 0; pos < len(line); {
		start := strings.Index(line[pos:], "![")
		if start < 0 {
			out.WriteString(line[pos:])
			break
		}
		start += pos
		out.WriteString(line[pos:start])
		closeAlt := strings.IndexByte(line[start+2:], ']')
		if closeAlt < 0 {
			out.WriteString(line[start:])
			break
		}
		openDest := start + 2 + closeAlt + 1
		if openDest >= len(line) || line[openDest] != '(' {
			out.WriteString(line[start:openDest])
			pos = openDest
			continue
		}
		closeDest := findMarkdownDestinationEnd(line, openDest+1)
		if closeDest < 0 {
			out.WriteString(line[start:])
			break
		}
		raw := line[openDest+1 : closeDest]
		destination, suffix := splitMarkdownDestination(raw)
		if shouldPreserveImageDestination(destination) {
			out.WriteString(line[start : closeDest+1])
			pos = closeDest + 1
			continue
		}
		localPath, err := resolveMarkdownImagePath(markdownPath, destination)
		if err != nil {
			return "", err
		}
		remote, err := upload(localPath)
		if err != nil {
			return "", fmt.Errorf("upload Markdown image %q: %w", destination, err)
		}
		out.WriteString(line[start : openDest+1])
		out.WriteString("<")
		out.WriteString(remote)
		out.WriteString(">")
		out.WriteString(suffix)
		out.WriteByte(')')
		pos = closeDest + 1
	}
	return out.String(), nil
}

func findMarkdownDestinationEnd(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}
func splitMarkdownDestination(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "<") {
		if end := strings.Index(raw, ">"); end >= 0 {
			return raw[1:end], raw[end+1:]
		}
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", ""
	}
	dest := fields[0]
	suffix := strings.TrimSpace(raw[len(dest):])
	return strings.Trim(dest, "<>"), suffix
}
func shouldPreserveImageDestination(destination string) bool {
	lower := strings.ToLower(strings.TrimSpace(destination))
	return lower == "" || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "./oss/file/")
}

func resolveMarkdownImagePath(markdownPath, destination string) (string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "", &MinerUError{Class: RetryClassParameter, Message: "Markdown image path is empty"}
	}
	decoded, err := url.PathUnescape(destination)
	if err != nil {
		return "", &MinerUError{Class: RetryClassParameter, Message: "Markdown image path is not valid URL encoding"}
	}
	destination = decoded
	if hasURIPathScheme(destination) {
		return "", &MinerUError{Class: RetryClassParameter, Message: "Markdown image URI scheme is not allowed for local upload"}
	}
	destination = strings.ReplaceAll(destination, "\\", string(os.PathSeparator))
	if filepath.IsAbs(destination) || isWindowsAbsolutePath(destination) || strings.HasPrefix(destination, "//") {
		return "", &MinerUError{Class: RetryClassParameter, Message: "absolute Markdown image paths are not allowed"}
	}
	root, err := filepath.Abs(filepath.Dir(markdownPath))
	if err != nil {
		return "", fmt.Errorf("resolve Markdown image root: %w", err)
	}
	resolved, err := filepath.Abs(filepath.Join(root, destination))
	if err != nil {
		return "", fmt.Errorf("resolve Markdown image path: %w", err)
	}
	cleanRoot := filepath.Clean(root)
	cleanResolved := filepath.Clean(resolved)
	if cleanResolved != cleanRoot && !strings.HasPrefix(cleanResolved, cleanRoot+string(os.PathSeparator)) {
		return "", &MinerUError{Class: RetryClassParameter, Message: "Markdown image path escapes the result directory"}
	}
	if err := rejectSymlinkEscape(cleanRoot, cleanResolved); err != nil {
		return "", err
	}
	return cleanResolved, nil
}

func hasURIPathScheme(value string) bool {
	colon := strings.IndexByte(value, ':')
	if colon <= 0 {
		return false
	}
	for i, r := range value[:colon] {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
			continue
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func isWindowsAbsolutePath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func rejectSymlinkEscape(root, target string) error {
	current := target
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return &MinerUError{Class: RetryClassParameter, Message: "Markdown image path must not use a symlink"}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Markdown image path: %w", err)
		}
		if current == root {
			return nil
		}
		next := filepath.Dir(current)
		if next == current || (next != root && !strings.HasPrefix(next, root+string(os.PathSeparator))) {
			return &MinerUError{Class: RetryClassParameter, Message: "Markdown image path escapes the result directory"}
		}
		current = next
	}
}
