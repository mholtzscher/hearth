package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubRegistrationRepository struct {
	binding Binding
	err     error
	calls   int
	params  RegisterBindingParams
}

func (repository *stubRegistrationRepository) RegisterBinding(_ context.Context, params RegisterBindingParams) (Binding, error) {
	repository.calls++
	repository.params = params
	return repository.binding, repository.err
}

func (*stubRegistrationRepository) GetEntityView(context.Context, EntityID) (EntityView, error) {
	panic("unexpected GetEntityView call")
}

func (*stubRegistrationRepository) ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error) {
	panic("unexpected ProjectObservation call")
}

func (*stubRegistrationRepository) DeleteExpiredObservationReceipts(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservationReceipts call")
}

func TestRegisterClassifiesOnlyDescriptorAndIdentityFailuresAsPermanent(t *testing.T) {
	catalog := firstLightCatalog(t)
	infrastructureFailure := errors.New("SQLite busy")
	repository := &stubRegistrationRepository{err: infrastructureFailure}
	service := NewService(repository, catalog, Dependencies{})

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
	service := NewService(repository, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].Support = EntitySupport(" \n { \"state\" : {}, \"operations\" : { \"set\" : {} } } ")
	original := string(registration.Entities[0].Support)

	if _, err := service.Register(context.Background(), "homeassistant", registration); err != nil {
		t.Fatal(err)
	}
	if got := string(repository.params.Entity.Support); got != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("repository support = %s", got)
	}
	if got := string(registration.Entities[0].Support); got != original {
		t.Fatalf("input support mutated to %s", got)
	}
}

func TestRegistrationOperatorMessagesAreBounded(t *testing.T) {
	message := operatorMessage(string(make([]rune, 600)))
	if len([]rune(message)) != 512 {
		t.Fatalf("message length = %d, want 512", len([]rune(message)))
	}
}
