package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type stubRegistrationRepository struct {
	binding       Binding
	err           error
	runtimeErr    error
	calls         int
	runtimeChecks int
	params        RegisterBindingParams
}

func (repository *stubRegistrationRepository) RegisterBinding(
	_ context.Context,
	params RegisterBindingParams,
) (Binding, error) {
	repository.calls++
	repository.params = params
	return repository.binding, repository.err
}

func (repository *stubRegistrationRepository) GetAdapter(
	_ context.Context,
	adapterID string,
) (AdapterInstance, error) {
	repository.runtimeChecks++
	if repository.runtimeErr != nil {
		return AdapterInstance{}, repository.runtimeErr
	}
	return AdapterInstance{
		ID: adapterID,
		Health: AdapterHealth{Runtime: &RuntimeEvidence{
			ID: commandTestRuntimeID, Status: runtimeStatusOnline,
		}},
	}, nil
}

func TestRegisterClassifiesOnlyDescriptorAndIdentityFailuresAsPermanent(t *testing.T) {
	t.Parallel()
	catalog := firstLightCatalog(t)
	infrastructureFailure := errors.New("SQLite busy")
	repository := &stubRegistrationRepository{err: infrastructureFailure}
	service := newTestService(repository, nil, catalog, Dependencies{})

	_, err := service.Register(
		context.Background(), "homeassistant", commandTestRuntimeID, validDomainRegistration(),
	)
	if !errors.Is(err, infrastructureFailure) {
		t.Fatalf("infrastructure error = %v, want original error", err)
	}
	var rejected *RegistrationRejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("infrastructure error was classified as permanent: %v", err)
	}

	invalid := validDomainRegistration()
	invalid.Entities[0].Support = EntitySupport(`{"state":{},"operations":{}}`)
	_, err = service.Register(context.Background(), "homeassistant", commandTestRuntimeID, invalid)
	if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
		t.Fatalf("invalid descriptor error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want only the valid attempt", repository.calls)
	}
}

func TestRegisterPrefersRuntimeFencingToDescriptorRejection(t *testing.T) {
	t.Parallel()
	repository := &stubRegistrationRepository{runtimeErr: ErrRuntimeFenced}
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})
	invalid := validDomainRegistration()
	invalid.Entities[0].TypeID = "unknown.entity/v1"
	invalid.Entities[0].Support = EntitySupport(`{}`)

	_, err := service.Register(context.Background(), "homeassistant", commandTestRuntimeID, invalid)
	if !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("registration error = %v", err)
	}
	if _, ok := errors.AsType[*RegistrationRejectedError](err); ok {
		t.Fatalf("stale runtime received descriptor rejection: %v", err)
	}
	if repository.runtimeChecks != 1 || repository.calls != 0 {
		t.Fatalf("runtime checks = %d, registration writes = %d", repository.runtimeChecks, repository.calls)
	}
}

func TestRegisterPersistsNormalizedSupportWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	catalog := firstLightCatalog(t)
	repository := &stubRegistrationRepository{}
	service := newTestService(repository, nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].Support = EntitySupport(" \n { \"state\" : {}, \"operations\" : { \"set\" : {} } } ")
	original := string(registration.Entities[0].Support)

	if _, err := service.Register(
		context.Background(), "homeassistant", commandTestRuntimeID, registration,
	); err != nil {
		t.Fatal(err)
	}
	if repository.params.RuntimeID != commandTestRuntimeID {
		t.Fatalf("repository runtime ID = %q", repository.params.RuntimeID)
	}
	if got := string(repository.params.Entities[0].Entity.Support); got != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("repository support = %s", got)
	}
	if got := string(registration.Entities[0].Support); got != original {
		t.Fatalf("input support mutated to %s", got)
	}
}

func TestRegisterRejectsInvalidEntitySetsBeforeGeneratingIDsOrCallingRepository(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			repository := &stubRegistrationRepository{}
			generatedIDs := 0
			service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{
				NewDeviceID: func() (DeviceID, error) { generatedIDs++; return "", nil },
				NewEntityID: func() (EntityID, error) { generatedIDs++; return "", nil },
			})

			_, err := service.Register(
				context.Background(), "homeassistant", commandTestRuntimeID, mutate(validDomainRegistration()),
			)
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
	t.Parallel()
	repository := &stubRegistrationRepository{}
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})
	registration := validDomainRegistration()
	registration.Entities = make([]EntityDescriptor, 64)
	for index := range registration.Entities {
		registration.Entities[index] = registrationEntity(
			fmt.Sprintf("power-%d", index),
			fmt.Sprintf("light.office.%d", index),
		)
	}

	if _, err := service.Register(
		context.Background(), "homeassistant", commandTestRuntimeID, registration,
	); err != nil {
		t.Fatal(err)
	}
	if len(repository.params.Entities) != 64 {
		t.Fatalf("repository entities = %d", len(repository.params.Entities))
	}
}

func TestRegisterAcceptsCanonicalDeviceKinds(t *testing.T) {
	t.Parallel()
	for _, kind := range []DeviceKind{DeviceKindLight, DeviceKindRelay, DeviceKindSensor} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			repository := &stubRegistrationRepository{}
			service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})
			registration := validDomainRegistration()
			registration.Device.Kind = kind

			if _, err := service.Register(
				context.Background(), "homeassistant", commandTestRuntimeID, registration,
			); err != nil {
				t.Fatalf("kind %q error = %v", kind, err)
			}
			if repository.calls != 1 || repository.params.Device.Kind != kind {
				t.Fatalf("repository calls = %d, kind = %q", repository.calls, repository.params.Device.Kind)
			}
		})
	}
}

func TestRegisterRejectsUnknownDeviceKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []DeviceKind{"", "plug", "switch", "Light", "SENSOR"} {
		t.Run("kind:"+string(kind), func(t *testing.T) {
			t.Parallel()
			repository := &stubRegistrationRepository{}
			service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})
			registration := validDomainRegistration()
			registration.Device.Kind = kind

			_, err := service.Register(context.Background(), "homeassistant", commandTestRuntimeID, registration)
			var rejected *RegistrationRejectedError
			if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
				t.Fatalf("kind %q error = %v", kind, err)
			}
			if repository.calls != 0 {
				t.Fatalf("repository calls = %d, want 0", repository.calls)
			}
		})
	}
}

func TestRegistrationOperatorMessagesAreBounded(t *testing.T) {
	t.Parallel()
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
