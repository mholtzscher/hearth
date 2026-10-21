//go:build integration || e2e

package hearthd //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
)

func adapterServiceForTest(database *sql.DB) *adapters.Service {
	repository := adapterssqlite.NewAdapterRepository(database)
	return adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
}

func testDeviceStores(repository *devicessqlite.DeviceRepository, database *sql.DB) devices.Stores {
	stores := devicessqlite.DeviceStores(repository)
	stores.AdapterReads = adapterServiceForTest(database)
	return stores
}
