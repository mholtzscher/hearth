package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// Open opens the core SQLite database with the required connection policy.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("SQLite path is required")
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	dsn := path + separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	database.SetMaxOpenConns(1)
	if pingErr := database.PingContext(ctx); pingErr != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping SQLite: %w", pingErr)
	}
	return database, nil
}

// Migrate applies all embedded Goose migrations.
func Migrate(ctx context.Context, database *sql.DB) error {
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, migrationErr := provider.Up(ctx); migrationErr != nil {
		return fmt.Errorf("apply migrations: %w", migrationErr)
	}
	return nil
}
