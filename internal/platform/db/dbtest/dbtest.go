//go:build integration || e2e

// Package dbtest shares immutable migrated SQLite images while giving each test
// its own database file. Images are built from the production migration set;
// copying preparation does not replace the migration a test intends to exercise.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

//nolint:gochecknoglobals // Immutable template built once per test binary, shared read-only by every test.
var templateImage = sync.OnceValues(buildMigratedTemplateImage)

//nolint:gochecknoglobals // One immutable historical image per requested version, built once per test binary.
var historicalImages = struct {
	mu        sync.Mutex
	byVersion map[int64]func() ([]byte, error)
}{byVersion: make(map[int64]func() ([]byte, error))}

// MigratedPath returns a fresh current-schema database path for production Core
// to open. Startup still runs its migration/version check against this file.
func MigratedPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hearth.db")
	installTemplate(t, path)
	return path
}

// OpenAtVersion opens an independent empty database at exactly version. Only
// setup is shared: callers must still execute the upgrade/rollback under test.
// Unlike OpenMigrated, it rejects an existing path rather than advancing it.
func OpenAtVersion(t *testing.T, path string, version int64) *sql.DB {
	t.Helper()
	if version <= 0 {
		t.Fatal("historical migration version must be positive")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("historical database path must not exist: %s", path)
	}
	image, err := historicalTemplateImage(version)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func historicalTemplateImage(version int64) ([]byte, error) {
	historicalImages.mu.Lock()
	build := historicalImages.byVersion[version]
	if build == nil {
		build = sync.OnceValues(func() ([]byte, error) { return buildTemplateImage(version) })
		historicalImages.byVersion[version] = build
	}
	historicalImages.mu.Unlock()
	return build()
}

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
	return buildTemplateImage(0)
}

// Version zero selects the complete current history. Historical images use the
// same provider, including Go-backed migrations, but stop at the requested version.
func buildTemplateImage(version int64) ([]byte, error) {
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
	if migrateErr := migrateTemplate(ctx, database, version); migrateErr != nil {
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

func migrateTemplate(ctx context.Context, database *sql.DB, version int64) error {
	if version == 0 {
		return platformdb.Migrate(ctx, database)
	}
	provider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		return err
	}
	if _, err = provider.UpTo(ctx, version); err != nil {
		return err
	}
	actual, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	if actual != version {
		return fmt.Errorf("historical migration version = %d, want %d", actual, version)
	}
	return nil
}
