package devices

import (
	"log/slog"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

type Dependencies struct {
	Logger           *slog.Logger
	Now              func() time.Time
	DeviceFacts      DeviceFactNotifier
	NewDeviceID      func() (DeviceID, error)
	NewEntityID      func() (EntityID, error)
	NewCommandID     func() (CommandID, error)
	NewCorrelationID func() (CorrelationID, error)
}

// Stores groups persistence capabilities consumed by Service.
type Stores struct {
	Registration  RegistrationRepository
	OwnedMappings OwnedMappingRepository
	Runtimes      RuntimeRepository
	Adapters      AdapterRepository
	Availability  AvailabilityRepository
	Reads         ReadRepository
	Enablement    EnablementRepository
	Commands      CommandLedger
	Observations  ObservationRepository
	EntityEvents  EntityEventRepository
}

type Service struct {
	logger           *slog.Logger
	stores           Stores
	sender           CommandSender
	catalog          *TypeCatalog
	dependencies     Dependencies
	deviceFacts      DeviceFactNotifier
	waiters          commandWaiters
	commandAdmission *lifecycle.AdmissionGroup
}

type commandWaiters struct {
	mutex sync.Mutex
	byID  map[CommandID]chan CommandResult
}

func NewService(stores Stores, sender CommandSender, catalog *TypeCatalog, dependencies Dependencies) *Service {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.Default().With(slog.String("component", "devices"))
	}
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
	return &Service{
		logger:           logger,
		stores:           stores,
		sender:           sender,
		catalog:          catalog,
		dependencies:     dependencies,
		deviceFacts:      dependencies.DeviceFacts,
		waiters:          commandWaiters{byID: make(map[CommandID]chan CommandResult)},
		commandAdmission: lifecycle.NewAdmissionGroup(),
	}
}
