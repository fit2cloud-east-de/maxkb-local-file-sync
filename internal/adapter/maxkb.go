package adapter

import (
	"context"
	"io"
	"time"
)

// MaxKBConfig configures the MaxKB HTTP adapter.
type MaxKBConfig struct {
	BaseURL     string
	APIKey      string
	Timeout     time.Duration
	MaxRetries  int
	EnableDebug bool
}

// MaxKBErrorType is a stable, user-facing classification for adapter failures.
// The adapter deliberately does not translate undocumented business codes into
// a success state; callers can inspect Code and Message for diagnostics.
type MaxKBErrorType string

const (
	MaxKBErrorNone             MaxKBErrorType = ""
	MaxKBErrorUnreachable      MaxKBErrorType = "UNREACHABLE"
	MaxKBErrorTLS              MaxKBErrorType = "TLS_ERROR"
	MaxKBErrorTimeout          MaxKBErrorType = "TIMEOUT"
	MaxKBErrorInvalidAPIKey    MaxKBErrorType = "INVALID_API_KEY"
	MaxKBErrorPermissionDenied MaxKBErrorType = "PERMISSION_DENIED"
	MaxKBErrorLicenseInvalid   MaxKBErrorType = "LICENSE_INVALID"
	MaxKBErrorIncompatible     MaxKBErrorType = "INCOMPATIBLE_RESPONSE"
	MaxKBErrorBusiness         MaxKBErrorType = "BUSINESS_ERROR"
)

// MaxKBAdapter contains the legacy methods used by the current sync executor
// and the contract-oriented methods required by the MaxKB v2.10.4-lts design.
// The latter use streaming io.Reader inputs and the documented knowledge API.
type MaxKBAdapter interface {
	// Legacy compatibility methods. They remain available so existing services
	// compile while the complete contract API is adopted incrementally.
	UploadDocument(ctx context.Context, req *UploadDocumentRequest) (*UploadDocumentResponse, error)
	DeleteDocument(ctx context.Context, req *DeleteDocumentRequest) error
	BatchCreateDocuments(ctx context.Context, req *BatchCreateRequest) (*BatchCreateResponse, error)
	QueryBatchStatus(ctx context.Context, req *QueryBatchStatusRequest) (*QueryBatchStatusResponse, error)

	// User, workspace and knowledge-base discovery.
	GetUserProfile(ctx context.Context) (*UserProfile, error)
	ListWorkspaces(ctx context.Context) ([]*Workspace, error)
	ListKnowledgeFolders(ctx context.Context, workspaceID string) ([]*KnowledgeFolder, error)
	ListKnowledgeBasesPage(ctx context.Context, req *ListKnowledgeBasesRequest) (*PagedKnowledgeBases, error)
	ListKnowledgeBases(ctx context.Context, workspaceID string) ([]*KnowledgeBase, error)
	GetKnowledgeBase(ctx context.Context, workspaceID, kbID string) (*KnowledgeBase, error)
	CreateKnowledgeBase(ctx context.Context, req *CreateKnowledgeBaseRequest) (*KnowledgeBase, error)
	ListEmbeddingModels(ctx context.Context, workspaceID string) ([]*EmbeddingModel, error)

	// Document contract API.
	UploadToOSS(ctx context.Context, file io.Reader, fileName string, fileSize int64) (*OSSUploadResult, error)
	SmartSplit(ctx context.Context, req *SmartSplitRequest) (*SmartSplitResult, error)
	CreateDocuments(ctx context.Context, req *CreateDocumentsRequest) (*CreateDocumentsResult, error)
	ListDocuments(ctx context.Context, req *ListDocumentsRequest) (*PagedDocuments, error)
	ListAllDocuments(ctx context.Context, workspaceID, knowledgeID string) ([]*Document, error)
	GetDocumentStatus(ctx context.Context, workspaceID, knowledgeID, documentID string) (*DocumentStatus, error)

	// Connection validation and links.
	Ping(ctx context.Context) (*ProfileInfo, error)
	ValidateConnection(ctx context.Context) (*ValidationResult, error)
	KnowledgeBaseLink(workspaceID, knowledgeID string) (string, error)
	DocumentLink(workspaceID, knowledgeID, documentID string) (string, error)
}

type UploadDocumentRequest struct {
	WorkspaceID string
	KBId        string
	FileName    string
	FileContent []byte
	FileSize    int64
}

type UploadDocumentResponse struct {
	// DocumentID is deprecated. The OSS endpoint only creates a temporary
	// source file; it does not create a knowledge-base document. New callers
	// must use SourceFileID and CreateDocuments for the document ID.
	DocumentID   string
	SourceFileID string
	FileName     string
	FileSize     int64
	UploadedAt   time.Time
}

type DeleteDocumentRequest struct {
	WorkspaceID string
	KBId        string
	DocumentID  string
}

// BatchCreateRequest is retained for the pre-contract executor. New code
// should use CreateDocuments with the documented batch_create payload.
type BatchCreateRequest struct {
	WorkspaceID   string
	KBId          string
	DocumentIDs   []string
	SplitPattern  string
	EnableMinerU  bool
	MinerUTaskIDs []string
}

type BatchCreateResponse struct {
	TaskID     string
	TotalCount int
	Status     string
	CreatedAt  time.Time
}

type QueryBatchStatusRequest struct {
	WorkspaceID string
	KBId        string
	TaskID      string
}

type QueryBatchStatusResponse struct {
	TaskID         string
	Status         string
	TotalCount     int
	ProcessedCount int
	SuccessCount   int
	FailedCount    int
	ErrorMessage   string
	UpdatedAt      time.Time
}

type UserProfile struct {
	ID         string
	Username   string
	Workspaces []*Workspace
}

type Workspace struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type KnowledgeFolder struct {
	ID       string
	Name     string
	Children []*KnowledgeFolder
}

type ListKnowledgeBasesRequest struct {
	WorkspaceID string
	FolderID    string
	Page        int
	Size        int
}

type PagedKnowledgeBases struct {
	Items   []*KnowledgeBase
	Total   int
	Current int
	Size    int
	// FetchedCount is the number of records returned by the server before
	// adapter-side type=0 filtering. It is used only for safe pagination.
	FetchedCount int
}

type KnowledgeBase struct {
	ID                 string
	Name               string
	Description        string
	WorkspaceID        string
	Type               int
	FolderID           string
	EmbeddingModelID   string
	EmbeddingModelName string
	FileSizeLimit      int64
	FileCountLimit     int
	DocumentCount      int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreateKnowledgeBaseRequest struct {
	WorkspaceID      string
	FolderID         string
	Name             string
	Description      string
	EmbeddingModelID string
}

type EmbeddingModel struct {
	ID        string
	Name      string
	Provider  string
	ModelType string
	Status    string
}

type ProfileInfo struct {
	Version        string
	VersionDisplay string
	Edition        string
	LicenseIsValid bool
}

type ValidationResult struct {
	Success        bool
	Version        string
	VersionDisplay string
	LicenseValid   bool
	ErrorType      MaxKBErrorType
	ErrorMessage   string
}

type SmartSplitRequest struct {
	WorkspaceID string
	KnowledgeID string
	File        io.Reader
	FileName    string
	FileSize    int64
}

type Paragraph struct {
	Title   string
	Content string
}

type SmartSplitResult struct {
	Name         string
	SourceFileID string
	Paragraphs   []Paragraph
}

type OSSUploadResult struct {
	// FileID is the temporary OSS/source-file identifier when the server
	// returns one. It must never be treated as a knowledge-base document ID.
	FileID  string
	FileURL string
}

type DocumentToCreate struct {
	Name         string
	Paragraphs   []Paragraph
	SourceFileID string
}

type CreateDocumentsRequest struct {
	WorkspaceID string
	KnowledgeID string
	Documents   []DocumentToCreate
}

type CreateDocumentsResult struct {
	DocumentIDs []string
}

type ListDocumentsRequest struct {
	WorkspaceID string
	KnowledgeID string
	Page        int
	Size        int
}

type PagedDocuments struct {
	Items   []*Document
	Total   int
	Current int
	Size    int
}

type Document struct {
	ID           string
	Name         string
	Status       string
	StatusMapped MaxKBDocStatusMapped
	SourceFileID string
	CreatedAt    string
}

type MaxKBDocStatusMapped string

const (
	MaxKBDocStatusPending    MaxKBDocStatusMapped = "PENDING"
	MaxKBDocStatusProcessing MaxKBDocStatusMapped = "PROCESSING"
	MaxKBDocStatusSuccess    MaxKBDocStatusMapped = "SUCCESS"
	MaxKBDocStatusFailed     MaxKBDocStatusMapped = "FAILED"
	MaxKBDocStatusUnknown    MaxKBDocStatusMapped = "UNKNOWN"
)

type DocumentStatus struct {
	ID           string
	Status       string
	StatusMapped MaxKBDocStatusMapped
}

type MaxKBError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	// Diagnostic contains safe response-shape metadata only; it never includes
	// response bodies, credentials, URLs with query strings, or file content.
	Diagnostic string
	Type       MaxKBErrorType
	Retryable  bool
}

func (e *MaxKBError) Error() string {
	if e == nil {
		return ""
	}
	message := e.Message
	if message == "" {
		message = "MaxKB request failed"
	}
	if e.Diagnostic != "" {
		return message + "; " + e.Diagnostic
	}
	return message
}

func (e *MaxKBError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.Retryable || e.StatusCode == 429 || e.StatusCode == 502 || e.StatusCode == 503 || e.StatusCode == 504
}
