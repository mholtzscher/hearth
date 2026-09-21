package db //nolint:testpackage // Migration tests require the package-private embedded migration set.

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// agentTimestampLayout mirrors the agent module's stored layout; the db package
// must not import the module just to assert a migration.
const agentTimestampLayout = "2006-01-02T15:04:05.000000000Z"

func newGooseProvider(t *testing.T, database *sql.DB) *goose.Provider {
	t.Helper()
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// TestAgentTimestampMigrationRewritesLegacyRows proves migration 00005 converts
// rows written in the variable-width RFC3339Nano layout so TEXT ordering and the
// retention cutoff comparisons stay chronological. It applies the schema only
// through migration 00004, seeds legacy rows, then applies 00005.
func TestAgentTimestampMigrationRewritesLegacyRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, upToErr := provider.UpTo(ctx, 4); upToErr != nil {
		t.Fatal(upToErr)
	}

	legacyConversations := []struct {
		id, at string
	}{
		{id: "aconv_legacy_trimmed", at: "2026-09-10T12:00:00Z"},
		{id: "aconv_legacy_fraction", at: "2026-09-10T12:00:00.5Z"},
	}
	for _, row := range legacyConversations {
		if _, execErr := database.ExecContext(ctx,
			`INSERT INTO agent_conversations(id, created_at) VALUES (?, ?)`, row.id, row.at,
		); execErr != nil {
			t.Fatal(execErr)
		}
	}
	if _, execErr := database.ExecContext(ctx,
		`INSERT INTO agent_messages(conversation_id, role, message_json, created_at)
		  VALUES (?, 'user', '{"role":"user","content":"hi"}', ?)`,
		"aconv_legacy_trimmed", "2026-09-10T12:00:00Z",
	); execErr != nil {
		t.Fatal(execErr)
	}

	if _, upErr := provider.Up(ctx); upErr != nil {
		t.Fatal(upErr)
	}

	for _, row := range legacyConversations {
		var stored string
		if queryErr := database.QueryRowContext(ctx,
			`SELECT created_at FROM agent_conversations WHERE id = ?`, row.id,
		).Scan(&stored); queryErr != nil {
			t.Fatal(queryErr)
		}
		if _, parseErr := time.Parse(agentTimestampLayout, stored); parseErr != nil {
			t.Fatalf("converted conversation timestamp %q: %v", stored, parseErr)
		}
	}
	var fraction string
	if queryErr := database.QueryRowContext(ctx,
		`SELECT created_at FROM agent_conversations WHERE id = 'aconv_legacy_fraction'`,
	).Scan(&fraction); queryErr != nil {
		t.Fatal(queryErr)
	}
	if fraction != "2026-09-10T12:00:00.500000000Z" {
		t.Fatalf("fractional timestamp = %q, want millisecond precision preserved", fraction)
	}
	var messageAt string
	if queryErr := database.QueryRowContext(ctx,
		`SELECT created_at FROM agent_messages`,
	).Scan(&messageAt); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, parseErr := time.Parse(agentTimestampLayout, messageAt); parseErr != nil {
		t.Fatalf("converted message timestamp %q: %v", messageAt, parseErr)
	}

	var indexCount int
	if queryErr := database.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'index'
		  AND name = 'agent_messages_conversation_created_idx'`,
	).Scan(&indexCount); queryErr != nil {
		t.Fatal(queryErr)
	}
	if indexCount != 1 {
		t.Fatalf("retention index count = %d, want 1", indexCount)
	}
}

// TestAgentTimestampMigrationPreservesFixedWidthNanoseconds proves a down/up
// cycle does not truncate timestamps written after migration 00005.
func TestAgentTimestampMigrationPreservesFixedWidthNanoseconds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, upErr := provider.Up(ctx); upErr != nil {
		t.Fatal(upErr)
	}

	const timestamp = "2026-09-10T12:00:00.123456789Z"
	if _, execErr := database.ExecContext(ctx,
		`INSERT INTO agent_conversations(id, created_at) VALUES ('aconv_fixed', ?)`, timestamp,
	); execErr != nil {
		t.Fatal(execErr)
	}
	if _, downErr := provider.Down(ctx); downErr != nil {
		t.Fatal(downErr)
	}
	if _, upErr := provider.Up(ctx); upErr != nil {
		t.Fatal(upErr)
	}

	var stored string
	if queryErr := database.QueryRowContext(ctx,
		`SELECT created_at FROM agent_conversations WHERE id = 'aconv_fixed'`,
	).Scan(&stored); queryErr != nil {
		t.Fatal(queryErr)
	}
	if stored != timestamp {
		t.Fatalf("timestamp after migration reapply = %q, want %q", stored, timestamp)
	}
}
