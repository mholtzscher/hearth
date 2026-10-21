//go:build integration

package automations_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// openAutomationDatabase opens one migrated Core database in a temporary
// directory, so Automation SQLite tests exercise the real schema.
func openAutomationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return database
}

// newAutomationRepository builds the SQLite Automation repository with
// production-default dependencies.
func newAutomationRepository(t *testing.T, database *sql.DB) *automationssqlite.AutomationRepository {
	t.Helper()
	return automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
}

func newRuntimeService(
	t *testing.T,
	scripted *scriptedDevices,
	dependencies automations.Dependencies,
) (*automations.Service, *sql.DB) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
	service := automations.NewService(repository, scripted, dependencies)
	return service, database
}
