package devices

import "time"

type Dependencies struct {
	Now         func() time.Time
	NewDeviceID func() (DeviceID, error)
	NewEntityID func() (EntityID, error)
}

type Service struct {
	registration RegistrationRepository
	catalog      *TypeCatalog
	dependencies Dependencies
}

func NewService(repository RegistrationRepository, catalog *TypeCatalog, dependencies Dependencies) *Service {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.NewDeviceID == nil {
		dependencies.NewDeviceID = NewDeviceID
	}
	if dependencies.NewEntityID == nil {
		dependencies.NewEntityID = NewEntityID
	}
	return &Service{registration: repository, catalog: catalog, dependencies: dependencies}
}
