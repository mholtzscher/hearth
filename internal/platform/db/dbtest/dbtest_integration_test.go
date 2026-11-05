//go:build integration

package dbtest_test

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestHistoricalImagesStopAtRequestedVersion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version   int64
		revisions int
		steps     int
		oldValues int
	}{
		{version: 20, revisions: 0, steps: 0, oldValues: 0},
		{version: 22, revisions: 1, steps: 1, oldValues: 0},
		{version: 23, revisions: 1, steps: 1, oldValues: 1},
		{version: 24, revisions: 1, steps: 1, oldValues: 0},
	} {
		t.Run(strconv.FormatInt(test.version, 10), func(t *testing.T) {
			t.Parallel()
			database := dbtest.OpenAtVersion(t, filepath.Join(t.TempDir(), "historical.db"), test.version)
			var version int64
			if err := database.QueryRowContext(t.Context(),
				`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != test.version {
				t.Fatalf("schema version = %d, want %d", version, test.version)
			}
			assertCount(t, database,
				`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='automation_definition_revisions'`,
				test.revisions)
			assertCount(t, database,
				`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='automation_step_executions'`,
				test.steps)
			assertCount(t, database,
				`SELECT count(*) FROM pragma_table_info('automation_history') WHERE name='value_evidence_bytes'`,
				test.oldValues)
		})
	}
}

func TestHistoricalImageCopiesIsolateRowsAndSchema(t *testing.T) {
	t.Parallel()
	for index := range 3 {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "isolated.db")
			database := dbtest.OpenAtVersion(t, path, 20)
			assertCount(t, database, `SELECT count(*) FROM sqlite_master WHERE name='fixture_only'`, 0)
			if _, err := database.ExecContext(
				t.Context(),
				`CREATE TABLE fixture_only (value TEXT NOT NULL)`,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(
				t.Context(),
				`INSERT INTO fixture_only VALUES ('owned row')`,
			); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := platformdb.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			assertCount(t, reopened, `SELECT count(*) FROM fixture_only WHERE value='owned row'`, 1)
		})
	}
}

func TestMigratedPathsPreserveIndependentReopenState(t *testing.T) {
	t.Parallel()
	for index := range 2 {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			path := dbtest.MigratedPath(t)
			database, err := platformdb.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := platformdb.NewMigrationProvider(database)
			if err != nil {
				t.Fatal(err)
			}
			current, target, err := provider.GetVersions(t.Context())
			if err != nil || current != target {
				t.Fatalf("copied image version = %d, want current %d: %v", current, target, err)
			}
			// Keep the production startup version check, not just the fixture open.
			if err = platformdb.Migrate(t.Context(), database); err != nil {
				t.Fatal(err)
			}
			assertCount(t, database, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='dashboards'`, 1)
			if _, err = database.ExecContext(t.Context(), `CREATE TABLE fixture_only (value INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if err = database.Close(); err != nil {
				t.Fatal(err)
			}
			// Reusing a path must not replace the database with an empty image.
			reopened := dbtest.OpenMigrated(t, path)
			assertCount(t, reopened, `SELECT count(*) FROM sqlite_master WHERE name='fixture_only'`, 1)
		})
	}
}

func assertCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(t.Context(), query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d for %s", got, want, query)
	}
}
