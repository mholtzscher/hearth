package devices

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterClassifiesOnlyDescriptorAndIdentityFailuresAsPermanent(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	if _, err := database.Exec(`
		CREATE TEMP TRIGGER fail_registration
		BEFORE INSERT ON devices
		BEGIN SELECT RAISE(ABORT, 'simulated SQLite failure'); END`); err != nil {
		t.Fatal(err)
	}

	_, err := service.Register(context.Background(), "homeassistant", validDomainRegistration())
	if err == nil {
		t.Fatal("infrastructure failure was not returned")
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
}

func TestRegisterPersistsNormalizedSupportWithoutMutatingInput(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	registration := validDomainRegistration()
	registration.Entities[0].Support = EntitySupport(" \n { \"state\" : {}, \"operations\" : { \"set\" : {} } } ")
	original := string(registration.Entities[0].Support)

	binding, err := service.Register(context.Background(), "homeassistant", registration)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := database.QueryRow("SELECT support_json FROM entities WHERE id = ?", binding.Entities[0].EntityID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("stored support = %s", stored)
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
