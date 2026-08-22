package devices

import (
	"context"
	"errors"
	"testing"
)

type stubRegistrationRepository struct {
	binding Binding
	err     error
	calls   int
}

func (repository *stubRegistrationRepository) RegisterBinding(context.Context, RegisterBindingParams) (Binding, error) {
	repository.calls++
	return repository.binding, repository.err
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
	invalid.Entities[0].SupportedOperations = nil
	_, err = service.Register(context.Background(), "homeassistant", invalid)
	if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
		t.Fatalf("invalid descriptor error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want only the valid attempt", repository.calls)
	}
}

func TestRegistrationOperatorMessagesAreBounded(t *testing.T) {
	message := operatorMessage(string(make([]rune, 600)))
	if len([]rune(message)) != 512 {
		t.Fatalf("message length = %d, want 512", len([]rune(message)))
	}
}
