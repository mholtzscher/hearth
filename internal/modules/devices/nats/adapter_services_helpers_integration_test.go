//go:build integration

package nats //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func startAdapterSessionServer(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
) commandSessionFixture {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	runtimes := adapterServiceForTest(database)
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := sessions.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return commandSessionFixture{t, runtimes}
}

func adapterServiceForTest(database *sql.DB) *adapters.Service {
	repository := adapterssqlite.NewAdapterRepository(database)
	return adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
}

func testDeviceStores(repository *devicessqlite.DeviceRepository, database *sql.DB) devices.Stores {
	stores := devicessqlite.DeviceStores(repository)
	stores.AdapterReads = adapterServiceForTest(database)
	return stores
}
