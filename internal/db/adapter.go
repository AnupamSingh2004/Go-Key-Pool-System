package db

import (
	"context"
	"time"
)

// DBAdapter defines the interface for all database operations
// This allows for testability and potential future database swaps
type DBAdapter interface {
	// Lifecycle
	Close() error

	// API Keys
	CreateAPIKey(ctx context.Context, key *APIKey) error
	GetAPIKey(ctx context.Context, id string) (*APIKey, error)
	GetAllAPIKeys(ctx context.Context) ([]*APIKey, error)
	UpdateAPIKeyWeight(ctx context.Context, id string, weight int) error
	UpdateAPIKeyHealth(ctx context.Context, id string, isHealthy bool, failureCount int, circuitState string) error
	UpdateAPIKeyLastUsed(ctx context.Context, id string) error
	UpdateAPIKeyConcurrent(ctx context.Context, id string, delta int) error
	DeleteAPIKey(ctx context.Context, id string) error
	ResetAPIKeyCircuit(ctx context.Context, id string) error

	// Requests
	CreateRequest(ctx context.Context, req *Request) error
	GetRequest(ctx context.Context, id string) (*Request, error)
	GetRequestByIdempotencyKey(ctx context.Context, idempotencyKey string) (*Request, error)
	UpdateRequestStatus(ctx context.Context, id string, status string, assignedKeyID string, attempts int) error
	UpdateRequestResult(ctx context.Context, id string, status string, responseStatus int, responseBody, lastError string, completedAt time.Time) error
	GetPendingRequests(ctx context.Context, limit int) ([]*Request, error)

	// Key Events
	CreateKeyEvent(ctx context.Context, event *KeyEvent) error
	GetKeyEvents(ctx context.Context, keyID string, limit int) ([]*KeyEvent, error)

	// System Config
	GetSystemConfig(ctx context.Context) (map[string]string, error)
	UpdateSystemConfig(ctx context.Context, key, value string) error
}

// APIKey represents an API key in the pool
type APIKey struct {
	ID                 string
	Name               string
	KeyEncrypted       string
	Weight             int
	IsHealthy          bool
	FailureCount       int
	CircuitState       string
	LastUsedAt         *time.Time
	RateLimitPerMinute int
	RateLimitPerDay    int
	ConcurrentLimit    int
	CurrentConcurrent  int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Request represents an API request in the queue
type Request struct {
	ID             string
	IdempotencyKey *string
	Source         string
	Priority       int
	Method         string
	DestinationURL string
	Headers        *string // JSON string
	Payload        *string // JSON string
	Status         string
	AssignedKeyID  *string
	Attempts       int
	LastError      *string
	ResponseStatus *int
	ResponseBody   *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

// KeyEvent represents an event in an API key's lifecycle
type KeyEvent struct {
	ID        string
	KeyID     string
	EventType string
	Message   *string
	CreatedAt time.Time
}

// Request status constants
const (
	RequestStatusPending    = "pending"
	RequestStatusProcessing = "processing"
	RequestStatusSuccess    = "success"
	RequestStatusFailed     = "failed"
	RequestStatusQueued     = "queued"
)

// Priority constants (1=High, 2=Normal, 3=Low)
const (
	PriorityHigh   = 1
	PriorityNormal = 2
	PriorityLow    = 3
)

// Circuit breaker states
const (
	CircuitStateClosed   = "closed"
	CircuitStateOpen     = "open"
	CircuitStateHalfOpen = "half_open"
)

// Request sources
const (
	SourceCron        = "cron"
	SourceManual      = "manual"
	SourceCodeSnippet = "code_snippet"
)

// Key event types
const (
	EventKeyCreated        = "created"
	EventKeyDeleted        = "deleted"
	EventCircuitOpened     = "circuit_opened"
	EventCircuitClosed     = "circuit_closed"
	EventCircuitHalfOpened = "circuit_half_opened"
	EventRateLimited       = "rate_limited"
	EventKeyFailed         = "key_failed"
	EventKeyRecovered      = "key_recovered"
)
