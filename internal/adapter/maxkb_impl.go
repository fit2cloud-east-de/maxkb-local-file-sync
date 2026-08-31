package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"maxkb-local-file-sync/internal/infra/credential"
)

const (
	defaultMaxKBPageSize = 100
	defaultMaxKBTimeout  = 30 * time.Second
	defaultMaxKBRetries  = 3
)

type maxkbClient struct {
	cfg    MaxKBConfig
	base   *url.URL
	client *http.Client
}

func NewMaxKBAdapter(cfg MaxKBConfig) MaxKBAdapter {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultMaxKBTimeout
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxKBRetries
	}
	base, err := normalizeBaseURL(cfg.BaseURL)
	if err == nil {
		cfg.BaseURL = base.String()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The MaxKB contract only permits the application headers set in
	// makeRequest. In particular, do not let net/http add its automatic
	// Accept-Encoding header; compression is not needed for these API calls
	// and would make the wire contract less explicit.
	transport.DisableCompression = true
	return &maxkbClient{
		cfg:  cfg,
		base: base,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}
}

func normalizeBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("MaxKB base URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid MaxKB base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("MaxKB base URL must use http or https")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("MaxKB base URL must contain only scheme, host and path")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func (c *maxkbClient) adminBase() string {
	if c.base == nil {
		return strings.TrimRight(c.cfg.BaseURL, "/") + "/admin"
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/admin"
	return strings.TrimRight(u.String(), "/")
}

func (c *maxkbClient) endpoint(parts ...string) (string, error) {
	if c.base == nil {
		return "", fmt.Errorf("invalid MaxKB base URL: %s", c.cfg.BaseURL)
	}
	u := *c.base
	escaped := make([]string, 0, len(parts)+1)
	escaped = append(escaped, "admin")
	escaped = append(escaped, parts...)
	setEscapedPath(&u, escaped)
	return u.String(), nil
}

func (c *maxkbClient) makeRequest(ctx context.Context, method, endpoint string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create MaxKB request: %w", err)
	}
	// The contract explicitly permits only these application headers. Do not
	// copy browser cookies, referer, origin, user-agent or sec-fetch headers.
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	// An empty value suppresses net/http's default Go-http-client User-Agent.
	// User-Agent is explicitly outside the MaxKB request contract.
	req.Header.Set("User-Agent", "")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func (c *maxkbClient) do(ctx context.Context, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return nil, &MaxKBError{Type: MaxKBErrorInvalidAPIKey, Message: "MaxKB API key is missing"}
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read MaxKB request body: %w", err)
		}
	}
	var lastErr error
	attempts := c.cfg.MaxRetries
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		var requestBody io.Reader
		if payload != nil {
			requestBody = bytes.NewReader(payload)
		}
		req, err := c.makeRequest(ctx, method, endpoint, requestBody, contentType)
		if err != nil {
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = maxkbClassifyTransportError(err)
			if !isRetryableError(lastErr) || attempt == attempts-1 {
				return nil, lastErr
			}
			if err := waitBackoff(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if shouldRetryStatus(resp.StatusCode) {
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read MaxKB error response: %w", readErr)
			} else {
				lastErr = c.errorFromHTTP(resp.StatusCode, resp.Header, bodyBytes)
			}
			if attempt < attempts-1 {
				if err := waitBackoff(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		return resp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("MaxKB request failed for %s", credential.SanitizeURL(endpoint))
}

func (c *maxkbClient) errorFromHTTP(status int, headers http.Header, body []byte) error {
	code, message := parseErrorPayload(body)
	errType := classifyHTTPError(status, code, message)
	return &MaxKBError{
		StatusCode: status,
		Code:       code,
		Message:    credential.Sanitize(message),
		RequestID:  firstHeader(headers, "X-Request-ID", "X-Request-Id", "Request-Id"),
		Type:       errType,
		Retryable:  shouldRetryStatus(status),
	}
}

func maxkbClassifyTransportError(err error) error {
	if err == nil {
		return nil
	}
	msg := credential.Sanitize(err.Error())
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &MaxKBError{Type: MaxKBErrorTimeout, Message: msg, Retryable: true}
	}
	var tlsErr *tls.CertificateVerificationError
	if errors.As(err, &tlsErr) {
		return &MaxKBError{Type: MaxKBErrorTLS, Message: msg}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && strings.Contains(strings.ToLower(urlErr.Error()), "tls") {
		return &MaxKBError{Type: MaxKBErrorTLS, Message: msg}
	}
	return &MaxKBError{Type: MaxKBErrorUnreachable, Message: msg, Retryable: true}
}

func classifyHTTPError(status int, code, message string) MaxKBErrorType {
	switch status {
	case http.StatusUnauthorized:
		return MaxKBErrorInvalidAPIKey
	case http.StatusForbidden:
		return MaxKBErrorPermissionDenied
	}
	if strings.Contains(strings.ToLower(message), "license") {
		return MaxKBErrorLicenseInvalid
	}
	// A successful HTTP response with a non-success MaxKB code and no
	// diagnostic message does not provide enough information to classify it as
	// a business failure. Treat it as an incompatible response rather than
	// guessing the server's undocumented code semantics.
	if status >= http.StatusOK && status < http.StatusMultipleChoices && strings.TrimSpace(message) == "" {
		return MaxKBErrorIncompatible
	}
	if status >= 400 {
		return MaxKBErrorBusiness
	}
	if code == "" {
		return MaxKBErrorIncompatible
	}
	return MaxKBErrorBusiness
}

func isRetryableError(err error) bool {
	var maxErr *MaxKBError
	return errors.As(err, &maxErr) && maxErr.IsRetryable()
}

func shouldRetryStatus(status int) bool {
	// Retry only statuses explicitly identified as transient by the contract.
	// A generic 500 is not retried blindly because it may represent a stable
	// server-side business failure.
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func waitBackoff(ctx context.Context, attempt int) error {
	delay := time.Duration(1<<min(attempt, 5)) * 100 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := headers.Get(name); value != "" {
			return credential.Sanitize(value)
		}
	}
	return ""
}

func parseErrorPayload(body []byte) (string, string) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", ""
	}
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		code := strings.Trim(string(envelope.Code), `"`)
		message := envelope.Message
		if message == "" {
			message = envelope.Msg
		}
		if message == "" {
			message = envelope.Error
		}
		return code, message
	}
	// Never copy an HTML/proxy/error body into an adapter error: it may contain
	// business content or reflected credentials.
	return "", "non-json error response"
}

func decodeEnvelope(resp *http.Response, target any) error {
	_, err := decodeEnvelopeCode(resp, target)
	return err
}

func decodeEnvelopeCode(resp *http.Response, target any) (string, error) {
	code, data, err := decodeEnvelopeRaw(resp, "maxkb_request")
	if err != nil {
		return "", err
	}
	if len(data) == 0 || string(data) == "null" {
		return code, nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return "", incompatibleResponseError(resp, code, "maxkb_request", "response data", data)
	}
	return code, nil
}

// decodeEnvelopeRaw validates the common MaxKB envelope while keeping the
// operation-specific data shape inside the adapter. It deliberately returns
// only raw JSON bytes to callers; diagnostics summarize shape metadata and do
// not include the response body.
func decodeEnvelopeRaw(resp *http.Response, operation string) (string, json.RawMessage, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read MaxKB response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil, (&maxkbClient{}).errorFromHTTP(resp.StatusCode, resp.Header, body)
	}
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", nil, &MaxKBError{
			Type:       MaxKBErrorIncompatible,
			StatusCode: resp.StatusCode,
			Message:    "MaxKB response is not valid JSON",
			Diagnostic: responseDiagnostic(resp, "", operation, "valid JSON envelope", "invalid-json", len(bytes.TrimSpace(body)), ""),
		}
	}
	code := strings.Trim(string(envelope.Code), `"`)
	if !isSuccessCode(code) {
		message := envelope.Message
		if message == "" {
			message = envelope.Msg
		}
		return "", nil, &MaxKBError{Type: classifyHTTPError(resp.StatusCode, code, message), StatusCode: resp.StatusCode, Code: code, Message: credential.Sanitize(message)}
	}
	return code, envelope.Data, nil
}

func incompatibleResponseError(resp *http.Response, code, operation, expected string, data json.RawMessage) error {
	return &MaxKBError{
		Type:       MaxKBErrorIncompatible,
		StatusCode: responseStatus(resp),
		Code:       code,
		Message:    "MaxKB response data has an incompatible shape",
		Diagnostic: responseDiagnostic(resp, code, operation, expected, jsonShape(data), len(bytes.TrimSpace(data)), jsonObjectKeys(data)),
	}
}

func incompatibleBatchCreateError(resp *http.Response, code, message, expected string, data json.RawMessage, extra ...string) error {
	diagnostic := responseDiagnostic(resp, code, "batch_create", expected, jsonShape(data), len(bytes.TrimSpace(data)), jsonObjectKeys(data))
	for _, item := range extra {
		if item = safeDiagnosticValue(item); item != "" {
			diagnostic += " " + item
		}
	}
	return &MaxKBError{
		Type:       MaxKBErrorIncompatible,
		StatusCode: responseStatus(resp),
		Code:       code,
		Message:    message,
		Diagnostic: diagnostic,
	}
}

func jsonArraySummary(raw json.RawMessage) string {
	if jsonShape(raw) != "array" {
		return ""
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	parts := []string{fmt.Sprintf("array_len=%d", len(items))}
	if len(items) > 0 {
		parts = append(parts, "first_item_type="+jsonShape(items[0]))
		if keys := jsonObjectKeys(items[0]); keys != "" {
			parts = append(parts, "first_item_keys="+keys)
		}
	}
	return strings.Join(parts, " ")
}

func responseStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func responseDiagnostic(resp *http.Response, code, operation, expected, dataType string, dataBytes int, keys string) string {
	status, contentType, method, path, requestID := 0, "", "", "", ""
	if resp != nil {
		status = resp.StatusCode
		contentType = strings.TrimSpace(resp.Header.Get("Content-Type"))
		requestID = firstHeader(resp.Header, "X-Request-ID", "X-Request-Id", "Request-Id")
		if resp.Request != nil {
			method = resp.Request.Method
			if resp.Request.URL != nil {
				path = resp.Request.URL.Path
			}
		}
	}
	parts := []string{fmt.Sprintf("operation=%s", operation), fmt.Sprintf("method=%s", method), fmt.Sprintf("http_status=%d", status), fmt.Sprintf("content_type=%s", safeDiagnosticValue(contentType)), fmt.Sprintf("data_type=%s", dataType), fmt.Sprintf("data_bytes=%d", dataBytes), fmt.Sprintf("expected=%s", expected)}
	if path != "" {
		parts = append(parts, "path="+safeDiagnosticValue(path))
	}
	if code != "" {
		parts = append(parts, "code="+safeDiagnosticValue(code))
	}
	if keys != "" {
		parts = append(parts, "keys="+keys)
	}
	if requestID != "" {
		parts = append(parts, "request_id="+safeDiagnosticValue(requestID))
	}
	return strings.Join(parts, " ")
}

func safeDiagnosticValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func jsonShape(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "empty"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

func jsonObjectKeys(raw json.RawMessage) string {
	if jsonShape(raw) != "object" {
		return ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		// Keys are metadata only, but they are still response-controlled input.
		// Normalize separators and control characters before putting them in a
		// single-line diagnostic; never include the corresponding values.
		key = safeDiagnosticValue(key)
		key = strings.ReplaceAll(key, ",", "_")
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 32 {
		keys = append(keys[:32], "<more>")
	}
	return strings.Join(keys, ",")
}

func extractOSSFileID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	path := value
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return ""
		}
		path = parsed.Path
	}
	path = strings.TrimPrefix(path, "./")
	if !strings.HasPrefix(path, "oss/file/") {
		return ""
	}
	id := strings.TrimPrefix(path, "oss/file/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	decoded, err := url.PathUnescape(id)
	if err != nil {
		return ""
	}
	id = decoded
	if id == "" || id == "." || id == ".." || strings.Contains(id, "/") || strings.ContainsRune(id, 0) {
		return ""
	}
	return id
}

func isSuccessCode(code string) bool {
	return code == "200" || code == "200.0"
}

func maxkbParseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func pathSegment(value string) string {
	return url.PathEscape(value)
}

func intValue(raw json.RawMessage) int {
	var number int
	if json.Unmarshal(raw, &number) == nil {
		return number
	}
	var floatValue float64
	if json.Unmarshal(raw, &floatValue) == nil {
		return int(floatValue)
	}
	return 0
}

// UploadDocument is retained for the existing executor. MaxKB's documented
// OSS endpoint returns a source file id; it does not create a knowledge-base
// document by itself.
func (c *maxkbClient) UploadDocument(ctx context.Context, req *UploadDocumentRequest) (*UploadDocumentResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("UploadDocument request is nil")
	}
	result, err := c.UploadToOSS(ctx, bytes.NewReader(req.FileContent), req.FileName, req.FileSize)
	if err != nil {
		return nil, err
	}
	return &UploadDocumentResponse{SourceFileID: result.FileID, FileName: req.FileName, FileSize: req.FileSize, UploadedAt: time.Now().UTC()}, nil
}

func (c *maxkbClient) UploadToOSS(ctx context.Context, file io.Reader, fileName string, fileSize int64) (*OSSUploadResult, error) {
	if file == nil {
		return nil, fmt.Errorf("OSS upload file is nil")
	}
	endpoint, err := c.endpoint("api", "oss", "file")
	if err != nil {
		return nil, err
	}
	resp, err := c.doMultipart(ctx, http.MethodPost, endpoint, file, fileName, fileSize, map[string]string{
		"source_id":   "TEMPORARY_120_MINUTE",
		"source_type": "TEMPORARY_120_MINUTE",
	})
	if err != nil {
		return nil, err
	}
	code, raw, err := decodeEnvelopeRaw(resp, "upload_oss")
	if err != nil {
		return nil, err
	}
	// MaxKB v2.10.4-lts returns data as the relative OSS path string
	// "./oss/file/<id>". Keep compatibility with object-shaped responses
	// seen in other deployments without exposing the server shape upstream.
	var pathValue string
	if json.Unmarshal(raw, &pathValue) == nil {
		fileID := extractOSSFileID(pathValue)
		if fileID == "" {
			return nil, incompatibleResponseError(resp, code, "upload_oss", "string OSS path or object{file_id,url,path}", raw)
		}
		return &OSSUploadResult{FileID: fileID, FileURL: pathValue}, nil
	}
	var data struct {
		FileID string `json:"file_id"`
		URL    string `json:"url"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, incompatibleResponseError(resp, code, "upload_oss", "string OSS path or object{file_id,url,path}", raw)
	}
	fileID := strings.TrimSpace(data.FileID)
	fileURL := maxkbFirstNonEmpty(data.URL, data.Path)
	if fileID == "" {
		fileID = extractOSSFileID(fileURL)
	}
	if fileID == "" && fileURL == "" {
		return nil, incompatibleResponseError(resp, code, "upload_oss", "string OSS path or object{file_id,url,path}", raw)
	}
	return &OSSUploadResult{FileID: fileID, FileURL: fileURL}, nil
}

type smartSplitParagraphWire struct {
	Title        string `json:"title"`
	Content      string `json:"content"`
	SourceFileID string `json:"source_file_id"`
}

type smartSplitDocumentWire struct {
	Name         string                    `json:"name"`
	Content      []smartSplitParagraphWire `json:"content"`
	Paragraphs   []smartSplitParagraphWire `json:"paragraphs"`
	SourceFileID string                    `json:"source_file_id"`
}

func (c *maxkbClient) SmartSplit(ctx context.Context, req *SmartSplitRequest) (*SmartSplitResult, error) {
	if req == nil || req.File == nil {
		return nil, fmt.Errorf("SmartSplit request/file is nil")
	}
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "knowledge", pathSegment(req.KnowledgeID), "document", "split")
	if err != nil {
		return nil, err
	}
	resp, err := c.doMultipart(ctx, http.MethodPost, endpoint, req.File, req.FileName, req.FileSize, nil)
	if err != nil {
		return nil, err
	}
	code, raw, err := decodeEnvelopeRaw(resp, "smart_split")
	if err != nil {
		return nil, err
	}

	// MaxKB v2.10.4-lts returns one item per parsed document. The item shape is
	// {name, content:[{title,content}], source_file_id}; source_file_id belongs
	// to the document item, not to each paragraph.
	var documents []smartSplitDocumentWire
	if err := json.Unmarshal(raw, &documents); err == nil && isDocumentSplitArray(documents) {
		result, normalizeErr := normalizeDocumentSplit(req.FileName, documents)
		if normalizeErr != nil {
			return nil, incompatibleResponseError(resp, code, "smart_split", "array of {name,content[],source_file_id}", raw)
		}
		return result, nil
	}

	// Keep compatibility with the earlier adapter contract used by some
	// gateways, where data is a flat paragraph array carrying source ids.
	var paragraphs []smartSplitParagraphWire
	if err := json.Unmarshal(raw, &paragraphs); err == nil && len(paragraphs) > 0 && isParagraphArray(paragraphs) {
		return normalizeParagraphSplit(req.FileName, paragraphs, raw, resp, code)
	}

	// A few deployments expose a single document object. Accept it only when
	// its fields are explicit; unknown objects still fail structurally.
	var wrapped smartSplitDocumentWire
	if err := json.Unmarshal(raw, &wrapped); err == nil && (wrapped.Name != "" || wrapped.Content != nil || wrapped.Paragraphs != nil || wrapped.SourceFileID != "") {
		result, normalizeErr := normalizeDocumentSplit(req.FileName, []smartSplitDocumentWire{wrapped})
		if normalizeErr != nil {
			return nil, incompatibleResponseError(resp, code, "smart_split", "object {name,content[],source_file_id}", raw)
		}
		return result, nil
	}
	return nil, incompatibleResponseError(resp, code, "smart_split", "array of {name,content[],source_file_id}", raw)
}

func isDocumentSplitArray(documents []smartSplitDocumentWire) bool {
	if len(documents) == 0 {
		return false
	}
	for _, document := range documents {
		// A document item must expose an actual paragraph container. A flat
		// paragraph item may still have source_file_id, so accepting that field
		// alone here would select the wrong normalization branch.
		if document.Content == nil && document.Paragraphs == nil {
			return false
		}
	}
	return true
}

func isParagraphArray(paragraphs []smartSplitParagraphWire) bool {
	for _, paragraph := range paragraphs {
		// source_file_id alone is not a paragraph. Requiring content prevents
		// malformed document records from being accepted by this fallback path.
		if strings.TrimSpace(paragraph.Content) == "" {
			return false
		}
	}
	return true
}

func normalizeDocumentSplit(fallbackName string, documents []smartSplitDocumentWire) (*SmartSplitResult, error) {
	if len(documents) == 0 {
		return nil, errors.New("empty document split result")
	}
	result := &SmartSplitResult{Name: fallbackName}
	for _, document := range documents {
		if strings.TrimSpace(result.Name) == "" && strings.TrimSpace(document.Name) != "" {
			result.Name = document.Name
		}
		if document.Content != nil && document.Paragraphs != nil {
			return nil, errors.New("ambiguous paragraph containers")
		}
		paragraphs := document.Content
		if paragraphs == nil {
			paragraphs = document.Paragraphs
		}
		documentID := strings.TrimSpace(document.SourceFileID)
		for _, paragraph := range paragraphs {
			id := strings.TrimSpace(paragraph.SourceFileID)
			if documentID == "" {
				documentID = id
			}
			if id != "" && id != documentID {
				return nil, errors.New("inconsistent source_file_id")
			}
			result.Paragraphs = append(result.Paragraphs, Paragraph{Title: paragraph.Title, Content: paragraph.Content})
		}
		if documentID == "" {
			return nil, errors.New("missing source_file_id")
		}
		if result.SourceFileID == "" {
			result.SourceFileID = documentID
		} else if result.SourceFileID != documentID {
			return nil, errors.New("inconsistent source_file_id")
		}
	}
	if strings.TrimSpace(result.Name) == "" {
		return nil, errors.New("missing document name")
	}
	if result.SourceFileID == "" || len(result.Paragraphs) == 0 {
		return nil, errors.New("missing source_file_id or paragraphs")
	}
	return result, nil
}

func normalizeParagraphSplit(fallbackName string, paragraphs []smartSplitParagraphWire, raw json.RawMessage, resp *http.Response, code string) (*SmartSplitResult, error) {
	result := &SmartSplitResult{Name: fallbackName}
	for _, paragraph := range paragraphs {
		id := strings.TrimSpace(paragraph.SourceFileID)
		if id == "" || (result.SourceFileID != "" && result.SourceFileID != id) {
			return nil, incompatibleResponseError(resp, code, "smart_split", "paragraphs with one source_file_id", raw)
		}
		result.SourceFileID = id
		result.Paragraphs = append(result.Paragraphs, Paragraph{Title: paragraph.Title, Content: paragraph.Content})
	}
	return result, nil
}

func (c *maxkbClient) CreateDocuments(ctx context.Context, req *CreateDocumentsRequest) (*CreateDocumentsResult, error) {
	if req == nil {
		return nil, fmt.Errorf("CreateDocuments request is nil")
	}
	payload := make([]map[string]any, 0, len(req.Documents))
	for _, document := range req.Documents {
		paragraphs := make([]map[string]string, 0, len(document.Paragraphs))
		for _, paragraph := range document.Paragraphs {
			paragraphs = append(paragraphs, map[string]string{"title": paragraph.Title, "content": paragraph.Content})
		}
		payload = append(payload, map[string]any{
			"name":           document.Name,
			"paragraphs":     paragraphs,
			"source_file_id": document.SourceFileID,
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal MaxKB batch_create request: %w", err)
	}
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "knowledge", pathSegment(req.KnowledgeID), "document", "batch_create")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPut, endpoint, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	// MaxKB deployments have returned two compatible shapes for batch_create:
	// a direct array of created document records, and the older three-item
	// tuple [document_records, knowledge_id, workspace_id]. Keep this
	// deployment-specific parsing inside the adapter.
	code, rawData, err := decodeEnvelopeRaw(resp, "batch_create")
	if err != nil {
		return nil, err
	}
	var data []json.RawMessage
	if err := json.Unmarshal(rawData, &data); err != nil {
		return nil, incompatibleBatchCreateError(resp, code, "MaxKB batch_create response has an unsupported data shape", "data document record array or tuple [records, knowledge_id, workspace_id]", rawData)
	}

	// The legacy tuple is identified by its first element being an array. Do
	// not identify it by length alone: a direct response can contain three
	// document records as well.
	recordsData := data
	recordsPath := "data"
	if len(data) == 3 && jsonShape(data[0]) == "array" {
		recordsData = nil
		if err := json.Unmarshal(data[0], &recordsData); err != nil {
			return nil, incompatibleBatchCreateError(resp, code, "MaxKB batch_create response document records have an unsupported shape", "data[0] array of records with string id", data[0], jsonArraySummary(data[0]))
		}
		recordsPath = "data[0]"
	}

	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawJSONArray(recordsData), &records); err != nil {
		return nil, incompatibleBatchCreateError(resp, code, "MaxKB batch_create response document records have an unsupported shape", recordsPath+" array of records with string id", rawData, jsonArraySummary(rawData))
	}
	if len(records) != len(req.Documents) {
		return nil, incompatibleBatchCreateError(resp, code, fmt.Sprintf("MaxKB batch_create returned %d document records for %d requested documents", len(records), len(req.Documents)), recordsPath+" record count must equal request document count", rawData, jsonArraySummary(rawData), fmt.Sprintf("requested_count=%d", len(req.Documents)))
	}
	documentIDs := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for index, record := range records {
		id := strings.TrimSpace(record.ID)
		if id == "" {
			return nil, incompatibleBatchCreateError(resp, code, "MaxKB batch_create response did not contain a document id", "each "+recordsPath+" record must contain a non-empty string id", rawData, jsonArraySummary(rawData), fmt.Sprintf("record_index=%d missing_required_fields=id", index))
		}
		if _, ok := seen[id]; ok {
			return nil, incompatibleBatchCreateError(resp, code, "MaxKB batch_create response contained duplicate document ids", recordsPath+" record ids must be unique", rawData, jsonArraySummary(rawData), fmt.Sprintf("record_index=%d duplicate_id=true", index))
		}
		seen[id] = struct{}{}
		documentIDs = append(documentIDs, id)
	}
	return &CreateDocumentsResult{DocumentIDs: documentIDs}, nil
}

// rawJSONArray re-encodes a slice of raw JSON values as a JSON array without
// interpreting any deployment-specific fields. It is used only to validate
// batch_create record arrays.
func rawJSONArray(items []json.RawMessage) json.RawMessage {
	if items == nil {
		return json.RawMessage("null")
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// QueryBatchStatus is a compatibility adapter for the legacy executor. The
// current MaxKB knowledge/batch_create contract does not define a separate
// batch-status endpoint. This method therefore keeps the endpoint used by the
// existing executor isolated here; its response fields remain best-effort and
// must be verified against the target MaxKB deployment before relying on them.
func (c *maxkbClient) QueryBatchStatus(ctx context.Context, req *QueryBatchStatusRequest) (*QueryBatchStatusResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("QueryBatchStatus request is nil")
	}
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "dataset", pathSegment(req.KBId), "document", "batch", pathSegment(req.TaskID))
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	var data struct {
		TaskID         string `json:"task_id"`
		ID             string `json:"id"`
		Status         string `json:"status"`
		TotalCount     int    `json:"total_count"`
		ProcessedCount int    `json:"processed_count"`
		SuccessCount   int    `json:"success_count"`
		FailedCount    int    `json:"failed_count"`
		ErrorMessage   string `json:"error_message"`
		UpdatedAt      string `json:"updated_at"`
	}
	if err := decodeEnvelope(resp, &data); err != nil {
		return nil, err
	}
	if data.TaskID == "" {
		data.TaskID = data.ID
	}
	if data.TaskID == "" {
		data.TaskID = req.TaskID
	}
	return &QueryBatchStatusResponse{
		TaskID:         data.TaskID,
		Status:         data.Status,
		TotalCount:     data.TotalCount,
		ProcessedCount: data.ProcessedCount,
		SuccessCount:   data.SuccessCount,
		FailedCount:    data.FailedCount,
		ErrorMessage:   credential.Sanitize(data.ErrorMessage),
		UpdatedAt:      maxkbParseTime(data.UpdatedAt),
	}, nil
}

func (c *maxkbClient) BatchCreateDocuments(ctx context.Context, req *BatchCreateRequest) (*BatchCreateResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("BatchCreateDocuments request is nil")
	}
	docs := make([]DocumentToCreate, 0, len(req.DocumentIDs))
	for _, id := range req.DocumentIDs {
		docs = append(docs, DocumentToCreate{Name: id, SourceFileID: id})
	}
	result, err := c.CreateDocuments(ctx, &CreateDocumentsRequest{WorkspaceID: req.WorkspaceID, KnowledgeID: req.KBId, Documents: docs})
	if err != nil {
		return nil, err
	}
	return &BatchCreateResponse{TaskID: firstNonEmptySlice(result.DocumentIDs), TotalCount: len(req.DocumentIDs), Status: "submitted", CreatedAt: time.Now().UTC()}, nil
}

func (c *maxkbClient) ListDocuments(ctx context.Context, req *ListDocumentsRequest) (*PagedDocuments, error) {
	if req == nil {
		return nil, fmt.Errorf("ListDocuments request is nil")
	}
	page, size := normalizePage(req.Page, req.Size)
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "knowledge", pathSegment(req.KnowledgeID), "document", strconv.Itoa(page), strconv.Itoa(size))
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	var raw documentPageWire
	if err := decodeEnvelope(resp, &raw); err != nil {
		return nil, err
	}
	return raw.toDomain(), nil
}

func (c *maxkbClient) ListAllDocuments(ctx context.Context, workspaceID, knowledgeID string) ([]*Document, error) {
	page, size := 1, defaultMaxKBPageSize
	var result []*Document
	for {
		current, err := c.ListDocuments(ctx, &ListDocumentsRequest{WorkspaceID: workspaceID, KnowledgeID: knowledgeID, Page: page, Size: size})
		if err != nil {
			return nil, err
		}
		result = append(result, current.Items...)
		if pageDone(page, size, len(current.Items), current.Total, current.Current) {
			return result, nil
		}
		page++
	}
}

func (c *maxkbClient) GetDocumentStatus(ctx context.Context, workspaceID, knowledgeID, documentID string) (*DocumentStatus, error) {
	documents, err := c.ListAllDocuments(ctx, workspaceID, knowledgeID)
	if err != nil {
		return nil, err
	}
	for _, document := range documents {
		if document.ID == documentID {
			return &DocumentStatus{ID: document.ID, Status: document.Status, StatusMapped: document.StatusMapped}, nil
		}
	}
	return nil, &MaxKBError{StatusCode: http.StatusNotFound, Type: MaxKBErrorBusiness, Code: "404", Message: "MaxKB document not found"}
}

func (c *maxkbClient) DeleteDocument(ctx context.Context, req *DeleteDocumentRequest) error {
	if req == nil {
		return fmt.Errorf("DeleteDocument request is nil")
	}
	return c.deleteDocument(ctx, req.WorkspaceID, req.KBId, req.DocumentID)
}

func (c *maxkbClient) deleteDocument(ctx context.Context, workspaceID, knowledgeID, documentID string) error {
	endpoint, err := c.endpoint("api", "workspace", pathSegment(workspaceID), "knowledge", pathSegment(knowledgeID), "document", pathSegment(documentID))
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodDelete, endpoint, nil, "")
	if err != nil {
		var maxErr *MaxKBError
		if errors.As(err, &maxErr) && maxErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	// Deleting a document that is already absent is idempotent. MaxKB may
	// return 404 as a normal response (rather than a transport error), so this
	// check must happen before decoding the error payload below.
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return c.errorFromHTTP(resp.StatusCode, resp.Header, body)
	}
	return nil
}

func (c *maxkbClient) GetUserProfile(ctx context.Context) (*UserProfile, error) {
	endpoint, err := c.endpoint("api", "user", "profile")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	var data struct {
		ID            string          `json:"id"`
		Username      string          `json:"username"`
		WorkspaceList []WorkspaceWire `json:"workspace_list"`
	}
	if err := decodeEnvelope(resp, &data); err != nil {
		return nil, err
	}
	return &UserProfile{ID: data.ID, Username: data.Username, Workspaces: workspaceWiresToDomain(data.WorkspaceList)}, nil
}

func (c *maxkbClient) ListWorkspaces(ctx context.Context) ([]*Workspace, error) {
	profile, err := c.GetUserProfile(ctx)
	if err != nil {
		return nil, err
	}
	return profile.Workspaces, nil
}

func (c *maxkbClient) ListKnowledgeFolders(ctx context.Context, workspaceID string) ([]*KnowledgeFolder, error) {
	endpoint, err := c.endpoint("api", "workspace", pathSegment(workspaceID), "KNOWLEDGE", "folder")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	var data json.RawMessage
	if err := decodeEnvelope(resp, &data); err != nil {
		return nil, err
	}
	return decodeFolders(data)
}

func (c *maxkbClient) ListKnowledgeBasesPage(ctx context.Context, req *ListKnowledgeBasesRequest) (*PagedKnowledgeBases, error) {
	if req == nil {
		return nil, fmt.Errorf("ListKnowledgeBases request is nil")
	}
	page, size := normalizePage(req.Page, req.Size)
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "knowledge", strconv.Itoa(page), strconv.Itoa(size))
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	if req.FolderID != "" {
		query.Set("folder_id", req.FolderID)
	}
	query.Set("scope", "WORKSPACE")
	u.RawQuery = query.Encode()
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil, "")
	if err != nil {
		return nil, err
	}
	var raw knowledgePageWire
	if err := decodeEnvelope(resp, &raw); err != nil {
		return nil, err
	}
	records := raw.records()
	items := make([]*KnowledgeBase, 0, len(records))
	for _, record := range records {
		if record.Type != 0 {
			continue
		}
		items = append(items, record.toDomain(req.WorkspaceID))
	}
	return &PagedKnowledgeBases{
		Items:        items,
		Total:        raw.Total,
		Current:      firstPositive(raw.Current, page),
		Size:         firstPositive(raw.Size, size),
		FetchedCount: len(records),
	}, nil
}

func (c *maxkbClient) ListKnowledgeBases(ctx context.Context, workspaceID string) ([]*KnowledgeBase, error) {
	// MaxKB v2.10.4-lts requires a valid folder_id when listing knowledge
	// bases. Resolve the folder tree first, then paginate each folder rather
	// than sending the invalid workspace-only request.
	folders, err := c.ListKnowledgeFolders(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	folderIDs := flattenKnowledgeFolderIDs(folders)
	if len(folderIDs) == 0 {
		return []*KnowledgeBase{}, nil
	}

	result := make([]*KnowledgeBase, 0)
	seen := make(map[string]struct{})
	for _, folderID := range folderIDs {
		for page, size := 1, defaultMaxKBPageSize; ; page++ {
			current, err := c.ListKnowledgeBasesPage(ctx, &ListKnowledgeBasesRequest{
				WorkspaceID: workspaceID,
				FolderID:    folderID,
				Page:        page,
				Size:        size,
			})
			if err != nil {
				return nil, err
			}
			for _, item := range current.Items {
				if item == nil || item.ID == "" {
					continue
				}
				if _, exists := seen[item.ID]; exists {
					continue
				}
				seen[item.ID] = struct{}{}
				result = append(result, item)
			}
			if pageDone(page, size, current.FetchedCount, current.Total, current.Current) {
				break
			}
		}
	}
	return result, nil
}

func flattenKnowledgeFolderIDs(folders []*KnowledgeFolder) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	var walk func([]*KnowledgeFolder)
	walk = func(items []*KnowledgeFolder) {
		for _, item := range items {
			if item == nil {
				continue
			}
			if item.ID != "" {
				if _, exists := seen[item.ID]; !exists {
					seen[item.ID] = struct{}{}
					result = append(result, item.ID)
				}
			}
			walk(item.Children)
		}
	}
	walk(folders)
	return result
}

func (c *maxkbClient) GetKnowledgeBase(ctx context.Context, workspaceID, kbID string) (*KnowledgeBase, error) {
	// The documented contract does not define a dedicated detail endpoint.
	// Resolve by the folder-aware paginated list and exact ID; do not guess a URL.
	items, err := c.ListKnowledgeBases(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, kb := range items {
		if kb != nil && kb.ID == kbID {
			return kb, nil
		}
	}
	return nil, &MaxKBError{StatusCode: http.StatusNotFound, Type: MaxKBErrorBusiness, Code: "404", Message: "MaxKB knowledge base not found"}
}

func (c *maxkbClient) CreateKnowledgeBase(ctx context.Context, req *CreateKnowledgeBaseRequest) (*KnowledgeBase, error) {
	if req == nil {
		return nil, fmt.Errorf("CreateKnowledgeBase request is nil")
	}
	payload := map[string]string{"folder_id": req.FolderID, "name": req.Name, "desc": req.Description, "embedding_model_id": req.EmbeddingModelID}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal knowledge base request: %w", err)
	}
	endpoint, err := c.endpoint("api", "workspace", pathSegment(req.WorkspaceID), "knowledge", "base")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, endpoint, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	var raw KnowledgeWire
	if err := decodeEnvelope(resp, &raw); err != nil {
		return nil, err
	}
	if raw.ID == "" {
		return nil, &MaxKBError{Type: MaxKBErrorIncompatible, Message: "MaxKB create knowledge response did not include an id"}
	}
	return raw.toDomain(req.WorkspaceID), nil
}

func (c *maxkbClient) ListEmbeddingModels(ctx context.Context, workspaceID string) ([]*EmbeddingModel, error) {
	endpoint, err := c.endpoint("api", "workspace", pathSegment(workspaceID), "model_list")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("model_type", "EMBEDDING")
	u.RawQuery = query.Encode()
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil, "")
	if err != nil {
		return nil, err
	}
	var data struct {
		SharedModel []EmbeddingModelWire `json:"shared_model"`
		Model       []EmbeddingModelWire `json:"model"`
	}
	if err := decodeEnvelope(resp, &data); err != nil {
		return nil, err
	}
	models := make([]*EmbeddingModel, 0, len(data.SharedModel)+len(data.Model))
	seen := map[string]bool{}
	for _, item := range append(data.SharedModel, data.Model...) {
		if item.ID == "" || seen[item.ID] || !isAvailableModel(item.Status) {
			continue
		}
		seen[item.ID] = true
		models = append(models, item.toDomain())
	}
	return models, nil
}

func isAvailableModel(status string) bool {
	// SUCCESS is the documented/observed design value. Empty or unknown
	// statuses are not treated as usable because doing so would guess.
	return strings.EqualFold(status, "SUCCESS") || strings.EqualFold(status, "AVAILABLE") || strings.EqualFold(status, "ACTIVE")
}

func (c *maxkbClient) Ping(ctx context.Context) (*ProfileInfo, error) {
	endpoint, err := c.endpoint("api", "profile")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	var data struct {
		Version        string `json:"version"`
		Edition        string `json:"edition"`
		LicenseIsValid *bool  `json:"license_is_valid"`
	}
	code, err := decodeEnvelopeCode(resp, &data)
	if err != nil {
		return nil, err
	}
	if code != "200" {
		return nil, &MaxKBError{Type: MaxKBErrorIncompatible, Code: code, Message: "MaxKB profile response code is not 200"}
	}
	if data.LicenseIsValid == nil || strings.TrimSpace(data.Version) == "" {
		return nil, &MaxKBError{Type: MaxKBErrorIncompatible, Message: "MaxKB profile response is missing version or license_is_valid"}
	}
	if !*data.LicenseIsValid {
		return nil, &MaxKBError{Type: MaxKBErrorLicenseInvalid, Message: "MaxKB license is invalid"}
	}
	return &ProfileInfo{Version: data.Version, VersionDisplay: displayVersion(data.Version), Edition: data.Edition, LicenseIsValid: *data.LicenseIsValid}, nil
}

func (c *maxkbClient) ValidateConnection(ctx context.Context) (*ValidationResult, error) {
	profile, err := c.Ping(ctx)
	if err != nil {
		result := &ValidationResult{Success: false, ErrorMessage: credential.Sanitize(err.Error())}
		var maxErr *MaxKBError
		if errors.As(err, &maxErr) {
			result.ErrorType = maxErr.Type
		}
		return result, err
	}
	return &ValidationResult{Success: true, Version: profile.Version, VersionDisplay: profile.VersionDisplay, LicenseValid: profile.LicenseIsValid}, nil
}

func displayVersion(version string) string {
	if index := strings.Index(version, "("); index >= 0 {
		version = strings.TrimSpace(version[:index])
	}
	return version
}

func (c *maxkbClient) KnowledgeBaseLink(workspaceID, knowledgeID string) (string, error) {
	return c.link("admin", "knowledge", pathSegment(knowledgeID), pathSegment(workspaceID), "0", "document")
}

func (c *maxkbClient) DocumentLink(workspaceID, knowledgeID, documentID string) (string, error) {
	endpoint, err := c.link("admin", "paragraph", pathSegment(knowledgeID), pathSegment(documentID))
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("from", "workspace")
	query.Set("isShared", "false")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (c *maxkbClient) link(parts ...string) (string, error) {
	if c.base == nil {
		return "", fmt.Errorf("invalid MaxKB base URL: %s", c.cfg.BaseURL)
	}
	u := *c.base
	setEscapedPath(&u, parts)
	return u.String(), nil
}

// setEscapedPath joins already escaped path segments without double-escaping
// IDs such as "knowledge/a". URL.Path contains the decoded form while
// URL.RawPath preserves the escaped segment boundaries.
func setEscapedPath(u *url.URL, escapedParts []string) {
	rawBase := strings.Trim(u.EscapedPath(), "/")
	rawSegments := make([]string, 0, len(escapedParts)+4)
	if rawBase != "" {
		rawSegments = append(rawSegments, strings.Split(rawBase, "/")...)
	}
	rawSegments = append(rawSegments, escapedParts...)
	decodedSegments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			// pathSegment only produces valid escapes. Keep the original value
			// rather than silently changing an endpoint if a future caller passes
			// an invalid escaped segment.
			decoded = segment
		}
		decodedSegments = append(decodedSegments, decoded)
	}
	u.Path = "/" + strings.Join(decodedSegments, "/")
	u.RawPath = "/" + strings.Join(rawSegments, "/")
}

// Wire types and tolerant envelope helpers are kept private to this adapter.
// They accept the documented field names plus harmless aliases seen in
// deployments, but never infer success from an unknown response shape.
type WorkspaceWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreateTime  string `json:"create_time"`
	UpdateTime  string `json:"update_time"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func workspaceWiresToDomain(items []WorkspaceWire) []*Workspace {
	result := make([]*Workspace, 0, len(items))
	for _, item := range items {
		result = append(result, &Workspace{ID: item.ID, Name: item.Name, Description: item.Description,
			CreatedAt: maxkbParseTime(maxkbFirstNonEmpty(item.CreateTime, item.CreatedAt)),
			UpdatedAt: maxkbParseTime(maxkbFirstNonEmpty(item.UpdateTime, item.UpdatedAt))})
	}
	return result
}

type KnowledgeFolderWire struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Children []KnowledgeFolderWire `json:"children"`
}

func decodeFolders(raw json.RawMessage) ([]*KnowledgeFolder, error) {
	var items []KnowledgeFolderWire
	if err := json.Unmarshal(raw, &items); err != nil {
		var envelope struct {
			Records []KnowledgeFolderWire `json:"records"`
			List    []KnowledgeFolderWire `json:"list"`
			Data    []KnowledgeFolderWire `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &envelope); err2 != nil {
			return nil, &MaxKBError{Type: MaxKBErrorIncompatible, Message: "MaxKB knowledge folder response has an incompatible shape"}
		}
		items = envelope.Records
		if len(items) == 0 {
			items = envelope.List
		}
		if len(items) == 0 {
			items = envelope.Data
		}
	}
	var convert func([]KnowledgeFolderWire) []*KnowledgeFolder
	convert = func(values []KnowledgeFolderWire) []*KnowledgeFolder {
		out := make([]*KnowledgeFolder, 0, len(values))
		for _, value := range values {
			out = append(out, &KnowledgeFolder{ID: value.ID, Name: value.Name, Children: convert(value.Children)})
		}
		return out
	}
	return convert(items), nil
}

type EmbeddingModelWire struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	ModelType string `json:"model_type"`
	Status    string `json:"status"`
}

func (w EmbeddingModelWire) toDomain() *EmbeddingModel {
	return &EmbeddingModel{ID: w.ID, Name: w.Name, Provider: w.Provider, ModelType: w.ModelType, Status: w.Status}
}

type KnowledgeWire struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Desc               string `json:"desc"`
	WorkspaceID        string `json:"workspace_id"`
	Type               int    `json:"type"`
	FolderID           string `json:"folder_id"`
	EmbeddingModelID   string `json:"embedding_model_id"`
	EmbeddingModelName string `json:"embedding_model_name"`
	FileSizeLimit      int64  `json:"file_size_limit"`
	FileCountLimit     int    `json:"file_count_limit"`
	DocumentCount      int    `json:"document_count"`
	CreateTime         string `json:"create_time"`
	UpdateTime         string `json:"update_time"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func (w KnowledgeWire) toDomain(workspaceID string) *KnowledgeBase {
	description := w.Description
	if description == "" {
		description = w.Desc
	}
	return &KnowledgeBase{ID: w.ID, Name: w.Name, Description: description,
		WorkspaceID: maxkbFirstNonEmpty(w.WorkspaceID, workspaceID), Type: w.Type,
		FolderID: w.FolderID, EmbeddingModelID: w.EmbeddingModelID,
		EmbeddingModelName: w.EmbeddingModelName, FileSizeLimit: w.FileSizeLimit,
		FileCountLimit: w.FileCountLimit, DocumentCount: w.DocumentCount,
		CreatedAt: maxkbParseTime(maxkbFirstNonEmpty(w.CreateTime, w.CreatedAt)),
		UpdatedAt: maxkbParseTime(maxkbFirstNonEmpty(w.UpdateTime, w.UpdatedAt))}
}

type knowledgePageWire struct {
	Total   int             `json:"total"`
	Current int             `json:"current"`
	Size    int             `json:"size"`
	Records []KnowledgeWire `json:"records"`
	List    []KnowledgeWire `json:"list"`
	Data    []KnowledgeWire `json:"data"`
}

func (w knowledgePageWire) records() []KnowledgeWire {
	if len(w.Records) > 0 {
		return w.Records
	}
	if len(w.List) > 0 {
		return w.List
	}
	return w.Data
}

type ParagraphWire struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type ParagraphWires []ParagraphWire

func (w ParagraphWires) toDomain() ([]Paragraph, error) {
	result := make([]Paragraph, 0, len(w))
	for _, item := range w {
		result = append(result, Paragraph{Title: item.Title, Content: item.Content})
	}
	return result, nil
}

type documentMetaWire struct {
	SourceFileID string `json:"source_file_id"`
}

type documentWire struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Status       string           `json:"status"`
	SourceFileID string           `json:"source_file_id"`
	Meta         documentMetaWire `json:"meta"`
	CreatedAt    string           `json:"created_at"`
	CreateTime   string           `json:"create_time"`
}

func (w documentWire) sourceFileID() string {
	// MaxKB deployments have returned this identifier both at the document
	// level and inside meta. Prefer the explicit top-level field when present,
	// while accepting the nested shape used by the document list API.
	return maxkbFirstNonEmpty(w.SourceFileID, w.Meta.SourceFileID)
}

type documentPageWire struct {
	Total   int            `json:"total"`
	Current int            `json:"current"`
	Size    int            `json:"size"`
	Records []documentWire `json:"records"`
	List    []documentWire `json:"list"`
	Data    []documentWire `json:"data"`
}

func (w documentPageWire) toDomain() *PagedDocuments {
	records := w.Records
	if len(records) == 0 {
		records = w.List
	}
	if len(records) == 0 {
		records = w.Data
	}
	items := make([]*Document, 0, len(records))
	for _, item := range records {
		items = append(items, &Document{ID: item.ID, Name: item.Name, Status: item.Status,
			StatusMapped: mapDocumentStatus(item.Status), SourceFileID: item.sourceFileID(),
			CreatedAt: maxkbFirstNonEmpty(item.CreatedAt, item.CreateTime)})
	}
	return &PagedDocuments{Items: items, Total: w.Total, Current: w.Current, Size: w.Size}
}

func mapDocumentStatus(raw string) MaxKBDocStatusMapped {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "PENDING", "QUEUED":
		return MaxKBDocStatusPending
	case "PROCESSING", "RUNNING", "EMBEDDING":
		return MaxKBDocStatusProcessing
	case "SUCCESS", "COMPLETED":
		return MaxKBDocStatusSuccess
	case "FAILED", "ERROR":
		return MaxKBDocStatusFailed
	}
	// MaxKB v2.10.4-lts stores the aggregate document status as one
	// character per task type. The server's State.SUCCESS is "2" and its
	// State.IGNORED is "n"; the document list API treats an aggregate made
	// only of those states as successful. Keep this exact, validated shape in
	// the adapter instead of treating arbitrary undocumented strings as success.
	if isMaxKBAggregateSuccessStatus(normalized) {
		return MaxKBDocStatusSuccess
	}
	return MaxKBDocStatusUnknown
}

func isMaxKBAggregateSuccessStatus(status string) bool {
	if len(status) != 4 {
		return false
	}
	for _, char := range status {
		if char != '2' && char != 'N' {
			return false
		}
	}
	return true
}

func maxkbFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func firstNonEmptySlice(values []string) string { return maxkbFirstNonEmpty(values...) }
func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultMaxKBPageSize
	}
	return page, size
}
func pageDone(page, size, fetched, total, current int) bool {
	if fetched == 0 {
		return true
	}
	if total > 0 && page*size >= total {
		return true
	}
	if current > 0 && current*size >= total && total > 0 {
		return true
	}
	return fetched < size
}

func sortedMultipartFieldKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// multipartContentLength calculates the exact multipart body length without
// reading or buffering the source file. The probe contains the same headers,
// fields, boundary and closing delimiter as the real stream, but no file bytes.
func multipartContentLength(boundary, fileName string, fields map[string]string, fileSize int64) (int64, bool) {
	if fileSize < 0 || fileSize > int64(^uint64(0)>>1) {
		return 0, false
	}
	var probe bytes.Buffer
	writer := multipart.NewWriter(&probe)
	if err := writer.SetBoundary(boundary); err != nil {
		return 0, false
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return 0, false
	}
	_ = part
	for _, key := range sortedMultipartFieldKeys(fields) {
		if err := writer.WriteField(key, fields[key]); err != nil {
			return 0, false
		}
	}
	if err := writer.Close(); err != nil {
		return 0, false
	}
	if int64(probe.Len()) > int64(^uint64(0)>>1)-fileSize {
		return 0, false
	}
	return int64(probe.Len()) + fileSize, true
}

// doMultipart sends a multipart request without buffering the source file in
// memory. A seekable source can be replayed for transient HTTP failures;
// non-seekable sources are attempted once because replaying them would require
// buffering the file and violate the streaming contract.
func (c *maxkbClient) doMultipart(ctx context.Context, method, endpoint string, file io.Reader, fileName string, fileSize int64, fields map[string]string) (*http.Response, error) {
	if file == nil {
		return nil, fmt.Errorf("MaxKB multipart file is nil")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return nil, &MaxKBError{Type: MaxKBErrorInvalidAPIKey, Message: "MaxKB API key is missing"}
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("invalid MaxKB multipart endpoint: %w", err)
	}
	seeker, replayable := file.(io.Seeker)
	attempts := c.cfg.MaxRetries
	if attempts < 1 {
		attempts = 1
	}
	if !replayable {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if replayable {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, fmt.Errorf("rewind MaxKB multipart file: %w", err)
			}
		}
		pipeReader, pipeWriter := io.Pipe()
		multipartWriter := multipart.NewWriter(pipeWriter)
		writeDone := make(chan error, 1)
		go func() {
			part, err := multipartWriter.CreateFormFile("file", fileName)
			if err == nil {
				_, err = io.Copy(part, file)
			}
			if err == nil {
				for _, key := range sortedMultipartFieldKeys(fields) {
					if err = multipartWriter.WriteField(key, fields[key]); err != nil {
						break
					}
				}
			}
			if closeErr := multipartWriter.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				_ = pipeWriter.CloseWithError(err)
			} else {
				_ = pipeWriter.Close()
			}
			writeDone <- err
		}()

		contentType := multipartWriter.FormDataContentType()
		request, err := c.makeRequest(ctx, method, endpoint, pipeReader, contentType)
		if err != nil {
			_ = pipeReader.Close()
			<-writeDone
			return nil, err
		}
		// MaxKB is served by Django/ASGI deployments that may reject a streamed
		// multipart request using HTTP chunked transfer. Set an exact
		// Content-Length when the caller knows the source size, while keeping the
		// file bytes themselves on the pipe (no in-memory file buffering).
		if contentLength, ok := multipartContentLength(multipartWriter.Boundary(), fileName, fields, fileSize); ok {
			request.ContentLength = contentLength
		}
		resp, err := c.client.Do(request)
		if err != nil {
			_ = pipeReader.Close()
			writeErr := <-writeDone
			if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
				lastErr = fmt.Errorf("stream MaxKB multipart file: %w", writeErr)
			} else {
				lastErr = maxkbClassifyTransportError(err)
			}
			if !isRetryableError(lastErr) || attempt == attempts-1 {
				return nil, lastErr
			}
			if err := waitBackoff(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		writeErr := <-writeDone
		if writeErr != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("stream MaxKB multipart file: %w", writeErr)
		}
		if shouldRetryStatus(resp.StatusCode) {
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read MaxKB error response: %w", readErr)
			} else {
				lastErr = c.errorFromHTTP(resp.StatusCode, resp.Header, bodyBytes)
			}
			if attempt < attempts-1 {
				if err := waitBackoff(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		return resp, nil
	}
	return nil, lastErr
}
