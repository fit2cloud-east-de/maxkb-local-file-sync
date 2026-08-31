package adapter

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	MinerUModeOnline   = "online"
	MinerUModeInternal = "internal"
)

// MinerUConfig contains transport and protocol settings for a MinerU adapter.
// APIKey is only sent as a Bearer token for APIs that require it. Presigned
// result/upload URLs are deliberately requested without that header.
type MinerUConfig struct {
	BaseURL        string
	APIKey         string
	Mode           string
	Timeout        time.Duration
	MaxRetries     int // total attempts, including the first attempt
	PollInterval   time.Duration
	TaskTimeout    time.Duration
	RetryBaseDelay time.Duration
	EnableDebug    bool

	Internal InternalMinerUOptions
}

// InternalMinerUOptions mirrors the fields accepted by the internal FastAPI
// service. The defaults are applied by the internal adapter when a field is
// not supplied.
type InternalMinerUOptions struct {
	Backend            string
	Effort             string
	ParseMethod        string
	Language           string
	FormulaEnable      *bool
	TableEnable        *bool
	ImageAnalysis      *bool
	ServerURL          string
	StartPageID        int
	ReturnMD           *bool
	ReturnMiddleJSON   *bool
	ReturnModelOutput  *bool
	ReturnContentList  *bool
	ReturnImages       *bool
	ResponseFormatZIP  *bool
	ReturnOriginalFile *bool
}

// MinerUAdapter is the legacy-compatible adapter surface used by the sync
// executor. Optional capabilities are exposed through the interfaces below so
// existing callers and test doubles do not need to implement new methods.
type MinerUAdapter interface {
	SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error)
	QueryTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error)
	DownloadResult(ctx context.Context, taskID string) ([]byte, error)
	CancelTask(ctx context.Context, taskID string) error
	Ping(ctx context.Context) error
}

// StreamingMinerUAdapter adds the streaming and durable polling operations.
type StreamingMinerUAdapter interface {
	MinerUAdapter
	DownloadResultTo(ctx context.Context, taskID string, dst io.Writer) error
	PollTask(ctx context.Context, taskID string, opts PollOptions) (*TaskStatusResponse, error)
	Health(ctx context.Context) (*HealthResult, error)
}

// DurableMinerUAdapter exposes operations that retain the URLs returned by an
// asynchronous service. Callers that persist MinerU remote references should
// use these methods after restart instead of reconstructing service URLs from
// a task ID.
type DurableMinerUAdapter interface {
	StreamingMinerUAdapter
	QueryTaskStatusAt(ctx context.Context, taskID, statusURL string) (*TaskStatusResponse, error)
	PollTaskAt(ctx context.Context, taskID, statusURL string, opts PollOptions) (*TaskStatusResponse, error)
	DownloadResultToAt(ctx context.Context, taskID, statusURL, resultURL string, dst io.Writer) error
}

type SubmitTaskRequest struct {
	FileName     string
	FilePath     string // preferred for durable, repeatable streaming uploads
	FileContent  []byte // retained for compatibility; FileReader is preferred
	FileReader   io.Reader
	FileSize     int64
	AttemptID    string // stable client-side idempotency/data_id value
	OutputFormat string
	ModelVersion string
	Options      InternalMinerUOptions
}

type SubmitTaskResponse struct {
	TaskID      string
	BatchID     string
	Status      string
	SubmittedAt time.Time
	StatusURL   string
	ResultURL   string
}

type TaskStatusResponse struct {
	TaskID       string
	Status       string
	Progress     int
	ResultURL    string
	StatusURL    string
	ErrorMessage string
	UpdatedAt    time.Time
	QueuedAhead  *int
}

type PollOptions struct {
	Interval time.Duration
	Timeout  time.Duration
}

type HealthResult struct {
	Healthy         bool
	ProtocolVersion string
	MaxConcurrent   int
	WindowSize      int
}

type RetryClass string

const (
	RetryClassNone        RetryClass = "none"
	RetryClassTransient   RetryClass = "transient"
	RetryClassAuth        RetryClass = "authentication"
	RetryClassPermission  RetryClass = "permission"
	RetryClassParameter   RetryClass = "parameter"
	RetryClassUnsupported RetryClass = "unsupported"
	RetryClassProtocol    RetryClass = "protocol"
)

// MinerUError is safe to expose in UI/logs: response text is truncated and
// passed through the credential sanitizer before it is stored here.
type MinerUError struct {
	StatusCode int
	Code       string
	Message    string
	TaskID     string
	Class      RetryClass
	RetryAfter time.Duration
}

func (e *MinerUError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *MinerUError) IsRetryable() bool {
	return e != nil && e.Class == RetryClassTransient
}

var (
	ErrUnsupportedCancellation = errors.New("MinerU task cancellation is not supported by the configured protocol")
	ErrInvalidMinerUResponse   = errors.New("MinerU response format is incompatible")
)

// IsRetryableMinerUError handles typed protocol errors and the small set of
// transport errors that are safe to retry. Unknown errors are deliberately not
// treated as transient: parameter, parser, and caller errors must not be
// retried blindly.
func IsRetryableMinerUError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var me *MinerUError
	if errors.As(err, &me) {
		return me.IsRetryable()
	}
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}
