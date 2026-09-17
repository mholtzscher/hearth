// Package dbtest shares one migrated SQLite template across every test that
// needs a Core database. Before this helper existed, each fixture paid
// ~300ms (race-enabled) replaying the full migration history through goose;
// a file copy of the already-migrated image plus a plain open costs ~30ms
// instead. The image is byte-identical to what Migrate produces because the
// same binary builds it from the same embedded migrations.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

//nolint:gochecknoglobals // Immutable template built once per test binary, shared read-only by every test.
var templateImage = sync.OnceValues(buildMigratedTemplateImage)

// OpenMigrated opens the SQLite database at path for a test and registers its
// close for test cleanup. A path that does not exist yet starts as a file
// copy of the shared migrated template, so no migration replay happens. A
// path that already exists (a test reopening its own database) is opened and
// passed through Migrate, which is a cheap version check on a current schema.
func OpenMigrated(t *testing.T, path string) *sql.DB {
	t.Helper()
	fresh := installTemplate(t, path)
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if !fresh {
		if migrateErr := platformdb.Migrate(context.Background(), database); migrateErr != nil {
			t.Fatal(migrateErr)
		}
	}
	return database
}

// Image returns the shared migrated, empty database image, building it on
// first use. Fixtures that assemble their own database file (claims,
// registrations) copy these bytes instead of migrating.
func Image(t *testing.T) []byte {
	t.Helper()
	image, err := templateImage()
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// installTemplate copies the shared image to path, which must not exist yet,
// and reports whether the copy happened.
func installTemplate(t *testing.T, path string) bool {
	t.Helper()
	if _, statErr := os.Stat(path); statErr == nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	image, err := templateImage()
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(path, image, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return true
}

func buildMigratedTemplateImage() ([]byte, error) {
	directory, err := os.MkdirTemp("", "hearth-sqlite-template-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	path := filepath.Join(directory, "template.db")
	ctx := context.Background()
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		_ = database.Close()
		return nil, migrateErr
	}
	// Checkpoint the WAL back into the main file so a template copy is
	// self-contained and no -wal/-shm sidecars are needed.
	if _, checkpointErr := database.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); checkpointErr != nil {
		_ = database.Close()
		return nil, checkpointErr
	}
	if closeErr := database.Close(); closeErr != nil {
		return nil, closeErr
	}
	// A clean close removes the sidecars; drop stragglers defensively so a
	// copied template never references files that were not copied with it.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
	return os.ReadFile(path)
}
