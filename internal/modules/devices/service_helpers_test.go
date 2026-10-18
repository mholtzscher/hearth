package devices //nolint:testpackage // Test helpers assemble package-private persistence capabilities.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func storesForTest(repository any) Stores {
	stores := Stores{}
	stores.Registration, _ = repository.(RegistrationRepository)
	stores.OwnedMappings, _ = repository.(OwnedMappingRepository)
	stores.AdapterReads, _ = repository.(AdapterReader)
	stores.AvailabilityHistory, _ = repository.(AvailabilityHistoryRepository)
	stores.HealthProjection, _ = repository.(HealthProjectionRepository)
	stores.Availability, _ = repository.(AvailabilityRepository)
	stores.Reads, _ = repository.(ReadRepository)
	stores.Enablement, _ = repository.(EnablementRepository)
	stores.Commands, _ = repository.(CommandLedger)
	stores.Observations, _ = repository.(ObservationRepository)
	stores.EntityEvents, _ = repository.(EntityEventRepository)
	return stores
}

func newTestService(
	repository any,
	sender CommandSender,
	catalog *TypeCatalog,
	dependencies Dependencies,
) *Service {
	return NewService(storesForTest(repository), sender, catalog, dependencies)
}

const testRuntimeID = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")

type availabilityHistoryRepositoryStub struct {
	availabilityHistoryPage  Page[HealthTransition]
	availabilityHistoryCalls int
}

func (repository *availabilityHistoryRepositoryStub) ListEntityAvailabilityHistory(
	context.Context,
	ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	repository.availabilityHistoryCalls++
	return repository.availabilityHistoryPage, nil
}
