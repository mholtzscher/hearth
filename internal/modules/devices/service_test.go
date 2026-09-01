package devices //nolint:testpackage // Test helpers assemble package-private persistence capabilities.

func storesForTest(repository any) Stores {
	stores := Stores{}
	stores.Registration, _ = repository.(RegistrationRepository)
	stores.Runtimes, _ = repository.(RuntimeRepository)
	stores.Adapters, _ = repository.(AdapterRepository)
	stores.Availability, _ = repository.(AvailabilityRepository)
	stores.Reads, _ = repository.(ReadRepository)
	stores.Enablement, _ = repository.(EnablementRepository)
	stores.Commands, _ = repository.(CommandLedger)
	stores.Observations, _ = repository.(ObservationRepository)
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
