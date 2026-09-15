// Package sqlite implements the devices persistence capabilities over one
// SQLite database: transaction boundaries, query execution, row mapping, and
// the generated dbsqlc queries. It imports the devices domain, never the
// reverse, so domain policy never depends on SQL row types.
package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

var (
	_ devices.RegistrationRepository = (*DeviceRepository)(nil)
	_ devices.OwnedMappingRepository = (*DeviceRepository)(nil)
	_ devices.RuntimeRepository      = (*DeviceRepository)(nil)
	_ devices.AdapterRepository      = (*DeviceRepository)(nil)
	_ devices.AvailabilityRepository = (*DeviceRepository)(nil)
	_ devices.ReadRepository         = (*DeviceRepository)(nil)
	_ devices.EnablementRepository   = (*DeviceRepository)(nil)
	_ devices.CommandLedger          = (*DeviceRepository)(nil)
	_ devices.ObservationRepository  = (*DeviceRepository)(nil)
	_ devices.EntityEventRepository  = (*DeviceRepository)(nil)
	_ devices.DeviceFactOutbox       = (*DeviceRepository)(nil)
)

// DeviceRepository implements the devices persistence capabilities over one
// migrated database, keeping related evidence and outbox writes transactional.
type DeviceRepository struct {
	database *sql.DB
	queries  *dbsqlc.Queries
	catalog  *devices.TypeCatalog
	// deviceFactID mints the stable identity of one Device Fact. It is called
	// inside the transaction that commits the fact's evidence, so a mint failure
	// rolls the evidence back and inbound redelivery can retry both together.
	deviceFactID func() (devices.DeviceFactID, error)
}

// NewDeviceRepository binds device persistence to a migrated database and the
// Entity-type catalog used to validate State and command-linked outcomes.
func NewDeviceRepository(database *sql.DB, catalog *devices.TypeCatalog) *DeviceRepository {
	return newDeviceRepository(database, catalog, devices.NewDeviceFactID)
}

// newDeviceRepository is the single repository constructor. The Device Fact ID
// generator is injected so one test can make a mint fail inside a devices
// transaction and prove the evidence rolls back with it; production always
// mints canonical fct_ identities.
func newDeviceRepository(
	database *sql.DB,
	catalog *devices.TypeCatalog,
	deviceFactID func() (devices.DeviceFactID, error),
) *DeviceRepository {
	if deviceFactID == nil {
		deviceFactID = devices.NewDeviceFactID
	}
	return &DeviceRepository{
		database:     database,
		queries:      dbsqlc.New(database),
		catalog:      catalog,
		deviceFactID: deviceFactID,
	}
}

// DeviceStores exposes one SQLite repository through each Service capability.
func DeviceStores(repository *DeviceRepository) devices.Stores {
	return devices.Stores{
		Registration:  repository,
		OwnedMappings: repository,
		Runtimes:      repository,
		Adapters:      repository,
		Availability:  repository,
		Reads:         repository,
		Enablement:    repository,
		Commands:      repository,
		Observations:  repository,
		EntityEvents:  repository,
	}
}
