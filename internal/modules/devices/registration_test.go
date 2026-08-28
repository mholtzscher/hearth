package devices

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type stubRegistrationRepository struct {
	binding Binding
	err     error
	calls   int
	params  RegisterBindingParams
}

func (repository *stubRegistrationRepository) RegisterBinding(
	_ context.Context,
	params RegisterBindingParams,
) (Binding, error) {
	repository.calls++
	repository.params = params
	return repository.binding, repository.err
}

func (*stubRegistrationRepository) ListDevices(context.Context, ListDevicesParams) (Page[Device], error) {
	panic("unexpected ListDevices call")
}

func (*stubRegistrationRepository) GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error) {
	panic("unexpected GetDevice call")
}

func (*stubRegistrationRepository) ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error) {
	panic("unexpected ListEntities call")
}

func (*stubRegistrationRepository) GetEntity(context.Context, EntityID) (EntityWithState, error) {
	panic("unexpected GetEntity call")
}

func (*stubRegistrationRepository) SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error) {
	panic("unexpected SetEntityEnabled call")
}

func (*stubRegistrationRepository) GetCommand(context.Context, CommandID) (CommandRecord, error) {
	panic("unexpected GetCommand call")
}

func (*stubRegistrationRepository) ListEntityCommands(
	context.Context,
	ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	panic("unexpected ListEntityCommands call")
}

func (*stubRegistrationRepository) ProjectObservation(
	context.Context,
	ProjectObservationParams,
) (ProjectionResult, error) {
	panic("unexpected ProjectObservation call")
}

func (*stubRegistrationRepository) DeleteExpiredObservationReceipts(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservationReceipts call")
}

func (*stubRegistrationRepository) CreateCommand(context.Context, CommandRecord) (CommandRecord, error) {
	panic("unexpected CreateCommand call")
}

func (*stubRegistrationRepository) MarkCommandAccepted(context.Context, CommandID, time.Time) error {
	panic("unexpected MarkCommandAccepted call")
}

func (*stubRegistrationRepository) CompleteCommand(context.Context, CommandCompletion) error {
	panic("unexpected CompleteCommand call")
}

func (*stubRegistrationRepository) InterruptActiveCommands(context.Context, time.Time) error {
	panic("unexpected InterruptActiveCommands call")
}

func TestRegisterClassifiesOnlyDescriptorAndIdentityFailuresAsPermanent(t *testing.T) {
	catalog := firstLightCatalog(t)
	infrastructureFailure := errors.New("SQLite busy")
	repository := &stubRegistrationRepository{err: infrastructureFailure}
	service := NewService(repository, nil, catalog, Dependencies{})

	_, err := service.Register(context.Background(), "homeassistant", validDomainRegistration())
	if !errors.Is(err, infrastructureFailure) {
		t.Fatalf("infrastructure error = %v, want original error", err)
	}
	var rejected *RegistrationRejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("infrastructure error was classified as permanent: %v", err)
	}

	invalid := validDomainRegistration()
	invalid.Entities[0].Support = EntitySupport(`{"state":{},"operations":{}}`)
	_, err = service.Register(context.Background(), "homeassistant", invalid)
	if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
		t.Fatalf("invalid descriptor error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want only the valid attempt", repository.calls)
	}
}

func TestRegisterPersistsNormalizedSupportWithoutMutatingInput(t *testing.T) {
	catalog := firstLightCatalog(t)
	repository := &stubRegistrationRepository{}
	service := NewService(repository, nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].Support = EntitySupport(" \n { \"state\" : {}, \"operations\" : { \"set\" : {} } } ")
	original := string(registration.Entities[0].Support)

	if _, err := service.Register(context.Background(), "homeassistant", registration); err != nil {
		t.Fatal(err)
	}
	if got := string(repository.params.Entities[0].Entity.Support); got != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("repository support = %s", got)
	}
	if got := string(registration.Entities[0].Support); got != original {
		t.Fatalf("input support mutated to %s", got)
	}
}

func TestRegisterRejectsInvalidEntitySetsBeforeGeneratingIDsOrCallingRepository(t *testing.T) {
	tests := map[string]func(Registration) Registration{
		"empty": func(registration Registration) Registration {
			registration.Entities = nil
			return registration
		},
		"more than 64": func(registration Registration) Registration {
			registration.Entities = make([]EntityDescriptor, 65)
			for index := range registration.Entities {
				registration.Entities[index] = registrationEntity(
					fmt.Sprintf("power-%d", index),
					fmt.Sprintf("light.office.%d", index),
				)
			}
			return registration
		},
		"duplicate key": func(registration Registration) Registration {
			registration.Entities = append(
				registration.Entities,
				registrationEntity("power", "light.office.brightness"),
			)
			return registration
		},
		"duplicate external ID": func(registration Registration) Registration {
			registration.Entities = append(registration.Entities, registrationEntity("brightness", "light.office"))
			return registration
		},
		"invalid later descriptor": func(registration Registration) Registration {
			invalid := registrationEntity("brightness", "light.office.brightness")
			invalid.Support = EntitySupport(`{"state":{},"operations":{}}`)
			registration.Entities = append(registration.Entities, invalid)
			return registration
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repository := &stubRegistrationRepository{}
			generatedIDs := 0
			service := NewService(repository, nil, firstLightCatalog(t), Dependencies{
				NewDeviceID: func() (DeviceID, error) { generatedIDs++; return "", nil },
				NewEntityID: func() (EntityID, error) { generatedIDs++; return "", nil },
			})

			_, err := service.Register(context.Background(), "homeassistant", mutate(validDomainRegistration()))
			var rejected *RegistrationRejectedError
			if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
				t.Fatalf("registration error = %v", err)
			}
			if generatedIDs != 0 || repository.calls != 0 {
				t.Fatalf("generated IDs = %d, repository calls = %d", generatedIDs, repository.calls)
			}
		})
	}
}

func TestRegisterAccepts64Entities(t *testing.T) {
	repository := &stubRegistrationRepository{}
	service := NewService(repository, nil, firstLightCatalog(t), Dependencies{})
	registration := validDomainRegistration()
	registration.Entities = make([]EntityDescriptor, 64)
	for index := range registration.Entities {
		registration.Entities[index] = registrationEntity(
			fmt.Sprintf("power-%d", index),
			fmt.Sprintf("light.office.%d", index),
		)
	}

	if _, err := service.Register(context.Background(), "homeassistant", registration); err != nil {
		t.Fatal(err)
	}
	if len(repository.params.Entities) != 64 {
		t.Fatalf("repository entities = %d", len(repository.params.Entities))
	}
}

func TestRegistrationOperatorMessagesAreBounded(t *testing.T) {
	message := operatorMessage(string(make([]rune, 600)))
	if len([]rune(message)) != 512 {
		t.Fatalf("message length = %d, want 512", len([]rune(message)))
	}
}

func registrationEntity(key, externalID string) EntityDescriptor {
	return EntityDescriptor{
		Key: key, ExternalID: externalID, Name: "Power", TypeID: EntityTypePowerV1,
		Support: EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}
}
