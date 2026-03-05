package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Migration represents a single database migration
type Migration struct {
	Version  int
	Name     string
	Filename string
	SQL      string
}

// RunMigrations executes all pending migrations in order
func RunMigrations(ctx context.Context, db *sql.DB, migrationsPath string) error {
	// Create migrations tracking table if it doesn't exist
	if err := createMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Load all migrations from filesystem
	migrations, err := loadMigrations(migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	// Get already applied migrations
	applied, err := getAppliedMigrations(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Execute pending migrations
	for _, migration := range migrations {
		if applied[migration.Version] {
			continue // Already applied
		}

		if err := executeMigration(ctx, db, migration); err != nil {
			return fmt.Errorf("failed to execute migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}

	return nil
}

// createMigrationsTable creates the schema_migrations table
func createMigrationsTable(ctx context.Context, db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
		)
	`
	_, err := db.ExecContext(ctx, query)
	return err
}

// loadMigrations reads all migration files from the filesystem
func loadMigrations(migrationsPath string) ([]Migration, error) {
	entries, err := os.ReadDir(migrationsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version from filename (e.g., "001_create_api_keys.sql" -> version 1)
		var version int
		var name string
		filename := entry.Name()
		parts := strings.SplitN(filename, "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid migration filename format: %s (expected NNN_name.sql)", filename)
		}

		if _, err := fmt.Sscanf(parts[0], "%d", &version); err != nil {
			return nil, fmt.Errorf("failed to parse version from %s: %w", filename, err)
		}

		name = strings.TrimSuffix(parts[1], ".sql")

		// Read migration SQL
		sqlBytes, err := os.ReadFile(filepath.Join(migrationsPath, filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", filename, err)
		}

		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			Filename: filename,
			SQL:      string(sqlBytes),
		})
	}

	// Sort by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// getAppliedMigrations returns a set of already applied migration versions
func getAppliedMigrations(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	query := `SELECT version FROM schema_migrations`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

// executeMigration runs a single migration within a transaction
func executeMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute the migration SQL
	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("failed to execute SQL: %w", err)
	}

	// Record the migration as applied
	recordQuery := `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
	if _, err := tx.ExecContext(ctx, recordQuery, migration.Version, migration.Name); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
