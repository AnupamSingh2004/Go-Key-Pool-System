package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"key-pool-system/internal/config"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/util"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Server holds all dependencies needed by the HTTP handlers.
type Server struct {
	DB     db.DBAdapter
	Queue  *queue.Queue
	Pool   *keypool.Manager
	HotCfg *config.HotReloadConfig
	Cfg    *config.Config
	Logger zerolog.Logger
}

// --- Public endpoints ---

// HealthCheck returns a simple OK response.
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SubmitRequest handles POST /api/requests
func (s *Server) SubmitRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		IdempotencyKey string            `json:"idempotency_key"`
		Source         string            `json:"source"`
		Priority       int               `json:"priority"`
		Method         string            `json:"method"`
		URL            string            `json:"url"`
		Headers        map[string]string `json:"headers"`
		Payload        string            `json:"payload"`
	}

	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate required fields
	if body.Method == "" || body.URL == "" {
		writeError(w, http.StatusBadRequest, "method and url are required")
		return
	}

	// Default priority
	if body.Priority == 0 {
		body.Priority = db.PriorityNormal
	}
	if body.Priority < db.PriorityHigh || body.Priority > db.PriorityLow {
		writeError(w, http.StatusBadRequest, "priority must be 1 (high), 2 (normal), or 3 (low)")
		return
	}

	// Default source
	if body.Source == "" {
		body.Source = db.SourceManual
	}

	// Idempotency check
	if body.IdempotencyKey != "" {
		ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
		defer cancel()
		existing, err := s.DB.GetRequestByIdempotencyKey(ctx, body.IdempotencyKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "database error")
			return
		}
		if existing != nil {
			writeJSON(w, http.StatusOK, requestToResponse(existing))
			return
		}
	}

	// Build request record
	req := &db.Request{
		ID:             uuid.New().String(),
		Source:         body.Source,
		Priority:       body.Priority,
		Method:         strings.ToUpper(body.Method),
		DestinationURL: body.URL,
		Status:         db.RequestStatusPending,
	}

	if body.IdempotencyKey != "" {
		req.IdempotencyKey = &body.IdempotencyKey
	}
	if body.Payload != "" {
		req.Payload = &body.Payload
	}
	if len(body.Headers) > 0 {
		h, _ := marshalJSON(body.Headers)
		req.Headers = &h
	}

	// Save to DB
	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()
	if err := s.DB.CreateRequest(ctx, req); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save request")
		return
	}

	// Enqueue
	item := &queue.Item{
		RequestID: req.ID,
		Priority:  req.Priority,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Queue.Enqueue(item); err != nil {
		writeError(w, http.StatusServiceUnavailable, "queue is full")
		return
	}

	writeJSON(w, http.StatusAccepted, requestToResponse(req))
}

// GetRequestStatus handles GET /api/requests/{id}
func (s *Server) GetRequestStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/api/requests/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "request id required")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	req, err := s.DB.GetRequest(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if req == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	writeJSON(w, http.StatusOK, requestToResponse(req))
}

// --- Admin endpoints ---

// AddKey handles POST /admin/keys
func (s *Server) AddKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name               string `json:"name"`
		Key                string `json:"key"`
		Weight             int    `json:"weight"`
		RateLimitPerMinute int    `json:"rate_limit_per_minute"`
		RateLimitPerDay    int    `json:"rate_limit_per_day"`
		ConcurrentLimit    int    `json:"concurrent_limit"`
	}

	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if body.Name == "" || body.Key == "" {
		writeError(w, http.StatusBadRequest, "name and key are required")
		return
	}

	// Defaults
	if body.Weight == 0 {
		body.Weight = 1
	}
	if body.RateLimitPerMinute == 0 {
		body.RateLimitPerMinute = 60
	}
	if body.RateLimitPerDay == 0 {
		body.RateLimitPerDay = 10000
	}
	if body.ConcurrentLimit == 0 {
		body.ConcurrentLimit = 5
	}

	// Encrypt the key
	encrypted, err := crypto.Encrypt(body.Key, s.Cfg.EncryptionKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt key")
		return
	}

	apiKey := &db.APIKey{
		ID:                 uuid.New().String(),
		Name:               body.Name,
		KeyEncrypted:       encrypted,
		Weight:             body.Weight,
		IsHealthy:          true,
		CircuitState:       db.CircuitStateClosed,
		RateLimitPerMinute: body.RateLimitPerMinute,
		RateLimitPerDay:    body.RateLimitPerDay,
		ConcurrentLimit:    body.ConcurrentLimit,
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if err := s.DB.CreateAPIKey(ctx, apiKey); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	// Log event
	s.logKeyEvent(apiKey.ID, db.EventKeyCreated, "key created via API")

	writeJSON(w, http.StatusCreated, keyToResponse(apiKey))
}

// ListKeys handles GET /admin/keys
func (s *Server) ListKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	keys, err := s.DB.GetAllAPIKeys(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	result := make([]map[string]any, len(keys))
	for i, k := range keys {
		result[i] = keyToResponse(k)
	}
	writeJSON(w, http.StatusOK, result)
}

// DeleteKey handles DELETE /admin/keys/{id}
func (s *Server) DeleteKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/admin/keys/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "key id required")
		return
	}

	// Check for /reset suffix
	if strings.HasSuffix(id, "/reset") || strings.HasSuffix(id, "/weight") {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if err := s.DB.DeleteAPIKey(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}

	s.logKeyEvent(id, db.EventKeyDeleted, "key deleted via API")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// UpdateKeyWeight handles PUT /admin/keys/{id}/weight
func (s *Server) UpdateKeyWeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Path: /admin/keys/{id}/weight
	path := strings.TrimPrefix(r.URL.Path, "/admin/keys/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "key id required")
		return
	}
	id := parts[0]

	var body struct {
		Weight int `json:"weight"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Weight <= 0 {
		writeError(w, http.StatusBadRequest, "weight must be positive")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if err := s.DB.UpdateAPIKeyWeight(ctx, id, body.Weight); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update weight")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "weight": body.Weight})
}

// ResetKeyCircuit handles POST /admin/keys/{id}/reset
func (s *Server) ResetKeyCircuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/admin/keys/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "key id required")
		return
	}
	id := parts[0]

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if err := s.DB.ResetAPIKeyCircuit(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset circuit breaker")
		return
	}

	s.logKeyEvent(id, db.EventCircuitClosed, "circuit breaker manually reset via API")
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "circuit_reset"})
}

// PoolHealth handles GET /admin/health
func (s *Server) PoolHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	statuses := s.Pool.GetHealthStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"pool_size":  s.Pool.PoolSize(),
		"queue_size": s.Queue.Size(),
		"keys":       statuses,
	})
}

// GetConfig handles GET /admin/config
func (s *Server) GetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	cfg, err := s.DB.GetSystemConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// UpdateConfig handles PUT /admin/config
func (s *Server) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	for key, value := range body {
		if err := s.DB.UpdateSystemConfig(ctx, key, value); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update config key: "+key)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Helper functions ---

func (s *Server) logKeyEvent(keyID, eventType, message string) {
	ctx, cancel := util.DBContext(nil, util.DBTimeoutShort)
	defer cancel()

	event := &db.KeyEvent{
		KeyID:     keyID,
		EventType: eventType,
		Message:   &message,
	}
	_ = s.DB.CreateKeyEvent(ctx, event)
}

func requestToResponse(req *db.Request) map[string]any {
	resp := map[string]any{
		"id":         req.ID,
		"source":     req.Source,
		"priority":   req.Priority,
		"method":     req.Method,
		"url":        req.DestinationURL,
		"status":     req.Status,
		"attempts":   req.Attempts,
		"created_at": req.CreatedAt,
		"updated_at": req.UpdatedAt,
	}
	if req.IdempotencyKey != nil {
		resp["idempotency_key"] = *req.IdempotencyKey
	}
	if req.AssignedKeyID != nil {
		resp["assigned_key_id"] = *req.AssignedKeyID
	}
	if req.ResponseStatus != nil {
		resp["response_status"] = *req.ResponseStatus
	}
	if req.ResponseBody != nil {
		resp["response_body"] = *req.ResponseBody
	}
	if req.LastError != nil {
		resp["last_error"] = *req.LastError
	}
	if req.CompletedAt != nil {
		resp["completed_at"] = *req.CompletedAt
	}
	return resp
}

func keyToResponse(k *db.APIKey) map[string]any {
	resp := map[string]any{
		"id":                    k.ID,
		"name":                  k.Name,
		"weight":                k.Weight,
		"is_healthy":            k.IsHealthy,
		"failure_count":         k.FailureCount,
		"circuit_state":         k.CircuitState,
		"rate_limit_per_minute": k.RateLimitPerMinute,
		"rate_limit_per_day":    k.RateLimitPerDay,
		"concurrent_limit":      k.ConcurrentLimit,
		"current_concurrent":    k.CurrentConcurrent,
		"created_at":            k.CreatedAt,
		"updated_at":            k.UpdatedAt,
	}
	if k.LastUsedAt != nil {
		resp["last_used_at"] = *k.LastUsedAt
	}
	return resp
}

func extractPathParam(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	// Remove any trailing segments
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
