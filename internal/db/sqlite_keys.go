package db

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateAPIKey inserts a new API key
func (s *SQLiteAdapter) CreateAPIKey(ctx context.Context, key *APIKey) error {
	query := `
		INSERT INTO api_keys (
			id, name, key_encrypted, weight, is_healthy, failure_count, 
			circuit_state, rate_limit_per_minute, rate_limit_per_day, 
			concurrent_limit, current_concurrent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		key.ID, key.Name, key.KeyEncrypted, key.Weight, boolToInt(key.IsHealthy),
		key.FailureCount, key.CircuitState, key.RateLimitPerMinute,
		key.RateLimitPerDay, key.ConcurrentLimit, key.CurrentConcurrent,
	)
	if err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	return nil
}

// GetAPIKey retrieves a single API key by ID
func (s *SQLiteAdapter) GetAPIKey(ctx context.Context, id string) (*APIKey, error) {
	query := `
		SELECT id, name, key_encrypted, weight, is_healthy, failure_count,
			   circuit_state, last_used_at, rate_limit_per_minute, rate_limit_per_day,
			   concurrent_limit, current_concurrent, created_at, updated_at
		FROM api_keys WHERE id = ?
	`
	key, err := scanAPIKey(s.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("API key not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}
	return key, nil
}

// GetAllAPIKeys retrieves all API keys ordered by creation time
func (s *SQLiteAdapter) GetAllAPIKeys(ctx context.Context) ([]*APIKey, error) {
	query := `
		SELECT id, name, key_encrypted, weight, is_healthy, failure_count,
			   circuit_state, last_used_at, rate_limit_per_minute, rate_limit_per_day,
			   concurrent_limit, current_concurrent, created_at, updated_at
		FROM api_keys
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query API keys: %w", err)
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		key, err := scanAPIKeyFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// UpdateAPIKeyWeight updates the weight of an API key
func (s *SQLiteAdapter) UpdateAPIKeyWeight(ctx context.Context, id string, weight int) error {
	return s.execExpectingOneRow(ctx,
		`UPDATE api_keys SET weight = ?, updated_at = datetime('now') WHERE id = ?`,
		"API key", id, weight, id,
	)
}

// UpdateAPIKeyHealth updates the health status of an API key
func (s *SQLiteAdapter) UpdateAPIKeyHealth(ctx context.Context, id string, isHealthy bool, failureCount int, circuitState string) error {
	return s.execExpectingOneRow(ctx,
		`UPDATE api_keys SET is_healthy = ?, failure_count = ?, circuit_state = ?, updated_at = datetime('now') WHERE id = ?`,
		"API key", id, boolToInt(isHealthy), failureCount, circuitState, id,
	)
}

// UpdateAPIKeyLastUsed updates the last_used_at timestamp
func (s *SQLiteAdapter) UpdateAPIKeyLastUsed(ctx context.Context, id string) error {
	query := `UPDATE api_keys SET last_used_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to update API key last used: %w", err)
	}
	return nil
}

// UpdateAPIKeyConcurrent adjusts the current_concurrent counter by delta (+1 or -1)
func (s *SQLiteAdapter) UpdateAPIKeyConcurrent(ctx context.Context, id string, delta int) error {
	query := `UPDATE api_keys SET current_concurrent = current_concurrent + ?, updated_at = datetime('now') WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, delta, id)
	if err != nil {
		return fmt.Errorf("failed to update API key concurrent count: %w", err)
	}
	return nil
}

// DeleteAPIKey removes an API key from the pool
func (s *SQLiteAdapter) DeleteAPIKey(ctx context.Context, id string) error {
	return s.execExpectingOneRow(ctx,
		`DELETE FROM api_keys WHERE id = ?`,
		"API key", id, id,
	)
}

// ResetAPIKeyCircuit resets the circuit breaker to closed state
func (s *SQLiteAdapter) ResetAPIKeyCircuit(ctx context.Context, id string) error {
	return s.execExpectingOneRow(ctx,
		`UPDATE api_keys SET failure_count = 0, circuit_state = ?, is_healthy = 1, updated_at = datetime('now') WHERE id = ?`,
		"API key", id, CircuitStateClosed, id,
	)
}

// scanAPIKey scans a single row into an APIKey struct
func scanAPIKey(row *sql.Row) (*APIKey, error) {
	key := &APIKey{}
	var isHealthy int
	var lastUsedAt sql.NullTime

	err := row.Scan(
		&key.ID, &key.Name, &key.KeyEncrypted, &key.Weight, &isHealthy,
		&key.FailureCount, &key.CircuitState, &lastUsedAt,
		&key.RateLimitPerMinute, &key.RateLimitPerDay, &key.ConcurrentLimit,
		&key.CurrentConcurrent, &key.CreatedAt, &key.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	key.IsHealthy = intToBool(isHealthy)
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}
	return key, nil
}

// scanAPIKeyFromRows scans a single row from sql.Rows into an APIKey struct
func scanAPIKeyFromRows(rows *sql.Rows) (*APIKey, error) {
	key := &APIKey{}
	var isHealthy int
	var lastUsedAt sql.NullTime

	err := rows.Scan(
		&key.ID, &key.Name, &key.KeyEncrypted, &key.Weight, &isHealthy,
		&key.FailureCount, &key.CircuitState, &lastUsedAt,
		&key.RateLimitPerMinute, &key.RateLimitPerDay, &key.ConcurrentLimit,
		&key.CurrentConcurrent, &key.CreatedAt, &key.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	key.IsHealthy = intToBool(isHealthy)
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}
	return key, nil
}
