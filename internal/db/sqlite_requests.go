package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateRequest inserts a new request into the database
func (s *SQLiteAdapter) CreateRequest(ctx context.Context, req *Request) error {
	query := `
		INSERT INTO requests (
			id, idempotency_key, source, priority, method, destination_url,
			headers, payload, status, attempts
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		req.ID, req.IdempotencyKey, req.Source, req.Priority, req.Method,
		req.DestinationURL, req.Headers, req.Payload, req.Status, req.Attempts,
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	return nil
}

// GetRequest retrieves a request by ID
func (s *SQLiteAdapter) GetRequest(ctx context.Context, id string) (*Request, error) {
	query := `
		SELECT id, idempotency_key, source, priority, method, destination_url,
			   headers, payload, status, assigned_key_id, attempts, last_error,
			   response_status, response_body, created_at, updated_at, completed_at
		FROM requests WHERE id = ?
	`
	req, err := scanRequest(s.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("request not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get request: %w", err)
	}
	return req, nil
}

// GetRequestByIdempotencyKey retrieves a request by its idempotency key
// Returns nil, nil if not found (not an error — used for deduplication check)
func (s *SQLiteAdapter) GetRequestByIdempotencyKey(ctx context.Context, idempotencyKey string) (*Request, error) {
	query := `
		SELECT id, idempotency_key, source, priority, method, destination_url,
			   headers, payload, status, assigned_key_id, attempts, last_error,
			   response_status, response_body, created_at, updated_at, completed_at
		FROM requests WHERE idempotency_key = ?
	`
	req, err := scanRequest(s.db.QueryRowContext(ctx, query, idempotencyKey))
	if err == sql.ErrNoRows {
		return nil, nil // Not found is expected for idempotency check
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get request by idempotency key: %w", err)
	}
	return req, nil
}

// UpdateRequestStatus updates the processing state of a request
func (s *SQLiteAdapter) UpdateRequestStatus(ctx context.Context, id string, status string, assignedKeyID string, attempts int) error {
	query := `UPDATE requests SET status = ?, assigned_key_id = ?, attempts = ?, updated_at = datetime('now') WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, status, assignedKeyID, attempts, id)
	if err != nil {
		return fmt.Errorf("failed to update request status: %w", err)
	}
	return nil
}

// UpdateRequestResult updates the final result of a request
func (s *SQLiteAdapter) UpdateRequestResult(ctx context.Context, id string, status string, responseStatus int, responseBody, lastError string, completedAt time.Time) error {
	query := `
		UPDATE requests 
		SET status = ?, response_status = ?, response_body = ?, last_error = ?, 
		    completed_at = ?, updated_at = datetime('now')
		WHERE id = ?
	`
	_, err := s.db.ExecContext(ctx, query, status, responseStatus, responseBody, lastError, completedAt, id)
	if err != nil {
		return fmt.Errorf("failed to update request result: %w", err)
	}
	return nil
}

// GetPendingRequests retrieves unfinished requests for queue rebuild on startup
func (s *SQLiteAdapter) GetPendingRequests(ctx context.Context, limit int) ([]*Request, error) {
	query := `
		SELECT id, idempotency_key, source, priority, method, destination_url,
			   headers, payload, status, assigned_key_id, attempts, last_error,
			   response_status, response_body, created_at, updated_at, completed_at
		FROM requests 
		WHERE status IN (?, ?)
		ORDER BY priority ASC, created_at ASC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, RequestStatusPending, RequestStatusQueued, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending requests: %w", err)
	}
	defer rows.Close()

	var requests []*Request
	for rows.Next() {
		req, err := scanRequestFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan request: %w", err)
		}
		requests = append(requests, req)
	}
	return requests, rows.Err()
}

// scanRequest scans a single sql.Row into a Request struct
func scanRequest(row *sql.Row) (*Request, error) {
	req := &Request{}
	var (
		idempotencyKey, assignedKeyID, lastError, responseBody sql.NullString
		headers, payload                                       sql.NullString
		responseStatus                                         sql.NullInt64
		completedAt                                            sql.NullTime
	)

	err := row.Scan(
		&req.ID, &idempotencyKey, &req.Source, &req.Priority, &req.Method,
		&req.DestinationURL, &headers, &payload, &req.Status,
		&assignedKeyID, &req.Attempts, &lastError, &responseStatus,
		&responseBody, &req.CreatedAt, &req.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	applyNullableFields(req, idempotencyKey, assignedKeyID, lastError, responseBody, headers, payload, responseStatus, completedAt)
	return req, nil
}

// scanRequestFromRows scans a single row from sql.Rows into a Request struct
func scanRequestFromRows(rows *sql.Rows) (*Request, error) {
	req := &Request{}
	var (
		idempotencyKey, assignedKeyID, lastError, responseBody sql.NullString
		headers, payload                                       sql.NullString
		responseStatus                                         sql.NullInt64
		completedAt                                            sql.NullTime
	)

	err := rows.Scan(
		&req.ID, &idempotencyKey, &req.Source, &req.Priority, &req.Method,
		&req.DestinationURL, &headers, &payload, &req.Status,
		&assignedKeyID, &req.Attempts, &lastError, &responseStatus,
		&responseBody, &req.CreatedAt, &req.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	applyNullableFields(req, idempotencyKey, assignedKeyID, lastError, responseBody, headers, payload, responseStatus, completedAt)
	return req, nil
}

// applyNullableFields maps sql.Null* types to Request pointer fields
func applyNullableFields(req *Request,
	idempotencyKey, assignedKeyID, lastError, responseBody sql.NullString,
	headers, payload sql.NullString,
	responseStatus sql.NullInt64,
	completedAt sql.NullTime,
) {
	if idempotencyKey.Valid {
		req.IdempotencyKey = &idempotencyKey.String
	}
	if assignedKeyID.Valid {
		req.AssignedKeyID = &assignedKeyID.String
	}
	if lastError.Valid {
		req.LastError = &lastError.String
	}
	if responseBody.Valid {
		req.ResponseBody = &responseBody.String
	}
	if headers.Valid {
		req.Headers = &headers.String
	}
	if payload.Valid {
		req.Payload = &payload.String
	}
	if responseStatus.Valid {
		status := int(responseStatus.Int64)
		req.ResponseStatus = &status
	}
	if completedAt.Valid {
		req.CompletedAt = &completedAt.Time
	}
}
