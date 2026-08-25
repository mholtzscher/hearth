package devices

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterNormalizesWithoutMutatingInput(t *testing.T) {
	catalog, err := newBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, database := newDeviceTestService(t, controlsWithCatalog(catalog))
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	registration := validDomainRegistration()
	registration.Entities[0].Support = EntitySupport(" \n { \"state\" : {}, \"operations\" : { \"set\" : {} } } ")
	original := string(registration.Entities[0].Support)

	if _, err := service.Register(context.Background(), "homeassistant", registration); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := database.QueryRow("SELECT support_json FROM entities").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("stored support = %s", stored)
	}
	if string(registration.Entities[0].Support) != original {
		t.Fatalf("input support mutated to %s", registration.Entities[0].Support)
	}
}

func TestRegisterClassifiesDescriptorAndIdentityFailures(t *testing.T) {
	service, _ := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))

	invalid := validDomainRegistration()
	invalid.Entities[0].Support = EntitySupport(`{"state":{},"operations":{}}`)
	_, err := service.Register(context.Background(), "homeassistant", invalid)
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) || rejected.Code != RegistrationInvalidDescriptor {
		t.Fatalf("invalid descriptor error = %v", err)
	}

	if _, err := service.Register(context.Background(), "homeassistant", validDomainRegistration()); err != nil {
		t.Fatal(err)
	}
	conflict := validDomainRegistration()
	conflict.BindingKey = "another-light"
	_, err = service.Register(context.Background(), "homeassistant", conflict)
	if !errors.As(err, &rejected) || rejected.Code != RegistrationIdentityConflict {
		t.Fatalf("identity conflict error = %v", err)
	}
}

func TestRegistrationOperatorMessagesAreBounded(t *testing.T) {
	message := operatorMessage(string(make([]rune, 600)))
	if len([]rune(message)) != 512 {
		t.Fatalf("message length = %d, want 512", len([]rune(message)))
	}
}
