package migration

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed sql/*.sql
var migrationFS embed.FS

// Migration represents a parsed SQL migration file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Engine manages database migrations.
type Engine struct {
	fs embed.FS
}

// NewEngine initializes migration engine with embedded SQL files.
func NewEngine() *Engine {
	return &Engine{fs: migrationFS}
}

// Run executes all pending up migrations in sequence.
func (e *Engine) Run(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	// 1. Ensure schema_migrations table exists
	if err := e.initSchema(ctx, db); err != nil {
		return fmt.Errorf("failed to init schema_migrations: %w", err)
	}

	// 2. Load applied versions
	applied, err := e.getAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// 3. Parse available migrations from embed.FS
	migrations, err := e.loadMigrations()
	if err != nil {
		return fmt.Errorf("failed to load migration files: %w", err)
	}

	// 4. Apply pending migrations inside transactions
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}

		slog.InfoContext(ctx, "Applying database migration", "version", m.Version, "name", m.Name)

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to start transaction for migration %d: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to execute migration %d (%s): %w", m.Version, m.Name, err)
		}

		insertSQL := "INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)"
		if _, err := tx.ExecContext(ctx, insertSQL, m.Version, m.Name, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", m.Version, err)
		}

		slog.InfoContext(ctx, "Database migration applied successfully", "version", m.Version, "name", m.Name)
	}

	return nil
}

func (e *Engine) initSchema(ctx context.Context, db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err := db.ExecContext(ctx, query)
	return err
}

func (e *Engine) getAppliedVersions(ctx context.Context, db *sql.DB) (applied map[int]bool, retErr error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close applied migrations rows: %w", closeErr))
		}
	}()

	applied = make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func (e *Engine) loadMigrations() ([]Migration, error) {
	entries, err := e.fs.ReadDir("sql")
	if err != nil {
		return nil, err
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		baseName := strings.TrimSuffix(entry.Name(), ".up.sql")
		parts := strings.SplitN(baseName, "_", 2)
		if len(parts) < 2 {
			continue
		}

		version, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		if strings.HasPrefix(parts[1], "example_") && !includeExampleMigrations {
			continue
		}

		content, err := e.fs.ReadFile(filepath.Join("sql", entry.Name()))
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    parts[1],
			SQL:     string(content),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}
