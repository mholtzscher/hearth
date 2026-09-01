package devices

import (
	"sync"
	"time"
)

type Dependencies struct {
	Now              func() time.Time
	NewDeviceID      func() (DeviceID, error)
	NewEntityID      func() (EntityID, error)
	NewCommandID     func() (CommandID, error)
	NewCorrelationID func() (CorrelationID, error)
	NewRuntimeID     func() (RuntimeID, error)
}

// Stores groups persistence capabilities consumed by Service.
type Stores struct {
	Registration RegistrationRepository
	Runtimes     RuntimeRepository
	Adapters     AdapterRepository
	Availability AvailabilityRepository
	Reads        ReadRepository
	Enablement   EnablementRepository
	Commands     CommandLedger
	Observations ObservationRepository
}

type Service struct {
	stores       Stores
	sender       CommandSender
	catalog      *TypeCatalog
	dependencies Dependencies
	leaseExpiry  leaseExpiryState
	waiters      commandWaiters
}

type commandWaiters struct {
	mutex sync.Mutex
	byID  map[CommandID]chan CommandResult
}

func NewService(stores Stores, sender CommandSender, catalog *TypeCatalog, dependencies Dependencies) *Service {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.NewDeviceID == nil {
		dependencies.NewDeviceID = NewDeviceID
	}
	if dependencies.NewEntityID == nil {
		dependencies.NewEntityID = NewEntityID
	}
	if dependencies.NewCommandID == nil {
		dependencies.NewCommandID = NewCommandID
	}
	if dependencies.NewCorrelationID == nil {
		dependencies.NewCorrelationID = NewCorrelationID
	}
	if dependencies.NewRuntimeID == nil {
		dependencies.NewRuntimeID = NewRuntimeID
	}
	return &Service{
		stores:       stores,
		sender:       sender,
		catalog:      catalog,
		dependencies: dependencies,
		leaseExpiry:  newLeaseExpiryState(),
		waiters:      commandWaiters{byID: make(map[CommandID]chan CommandResult)},
	}
}
