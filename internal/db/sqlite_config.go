package db

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateKeyEvent inserts a new key lifecycle event
func (s *SQLiteAdapter) CreateKeyEvent(ctx context.Context, event *KeyEvent) error {
	query := `INSERT INTO key_events (id, key_id, event_type, message) VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, event.ID, event.KeyID, event.EventType, event.Message)
	if err != nil {
		return fmt.Errorf("failed to create key event: %w", err)
	}
	return nil
}

// GetKeyEvents retrieves recent events for a specific key
func (s *SQLiteAdapter) GetKeyEvents(ctx context.Context, keyID string, limit int) ([]*KeyEvent, error) {
	query := `
		SELECT id, key_id, event_type, message, created_at
		FROM key_events
		WHERE key_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, keyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query key events: %w", err)
	}
	defer rows.Close()

	var events []*KeyEvent
	for rows.Next() {
		event := &KeyEvent{}
		var message sql.NullString

		err := rows.Scan(&event.ID, &event.KeyID, &event.EventType, &message, &event.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan key event: %w", err)
		}

		if message.Valid {
			event.Message = &message.String
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// GetSystemConfig retrieves all system configuration as a key-value map
func (s *SQLiteAdapter) GetSystemConfig(ctx context.Context) (map[string]string, error) {
	query := `SELECT key, value FROM system_config`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query system config: %w", err)
	}
	defer rows.Close()

	config := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan system config: %w", err)
		}
		config[key] = value
	}
	return config, rows.Err()
}

// UpdateSystemConfig upserts a system configuration value
func (s *SQLiteAdapter) UpdateSystemConfig(ctx context.Context, key, value string) error {
	query := `
		INSERT INTO system_config (key, value, updated_at) 
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')
	`
	_, err := s.db.ExecContext(ctx, query, key, value)
	if err != nil {
		return fmt.Errorf("failed to update system config: %w", err)
	}
	return nil
}

// execExpectingOneRow is a shared helper for UPDATE/DELETE that must affect exactly 1 row
func (s *SQLiteAdapter) execExpectingOneRow(ctx context.Context, query, entityName, entityID string, args ...interface{}) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to modify %s: %w", entityName, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%s not found: %s", entityName, entityID)
	}
	return nil
}
