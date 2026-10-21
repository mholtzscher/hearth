//go:build integration

package httpapi_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func openAutomationTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return database
}

func newAutomationService(t *testing.T, stub *apiDevices, options ...automations.Dependencies) *automations.Service {
	t.Helper()
	database := openAutomationTestDatabase(t)
	dependencies := automations.Dependencies{}
	if len(options) > 0 {
		dependencies = options[0]
	}
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
	service := automations.NewService(repository, stub, dependencies)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Drain(ctx); err != nil {
			t.Errorf("drain automations: %v", err)
		}
	})
	return service
}
