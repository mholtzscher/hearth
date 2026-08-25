package devices

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRegistrationIsIdempotentAndConcurrent(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))

	const attempts = 8
	type outcome struct {
		binding Binding
		err     error
	}
	results := make(chan outcome, attempts)
	var start sync.WaitGroup
	start.Add(1)
	for range attempts {
		go func() {
			start.Wait()
			binding, err := service.Register(context.Background(), "homeassistant", validDomainRegistration())
			results <- outcome{binding: binding, err: err}
		}()
	}
	start.Done()
	var first Binding
	for index := range attempts {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if index == 0 {
			first = result.binding
		} else if result.binding.DeviceID != first.DeviceID || result.binding.Entities[0].EntityID != first.Entities[0].EntityID {
			t.Fatalf("registration changed IDs: first=%#v got=%#v", first, result.binding)
		}
	}
	for table := range map[string]struct{}{"devices": {}, "entities": {}} {
		var count int
		if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d", table, count)
		}
	}
}

func TestRegistrationConflictRollsBackDescriptorWrites(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	original := validDomainRegistration()
	if _, err := service.Register(context.Background(), "homeassistant", original); err != nil {
		t.Fatal(err)
	}

	conflict := validDomainRegistration()
	conflict.Device.Name = "must roll back"
	conflict.Entities[0].Key = "another"
	conflict.Entities[0].ExternalID = "light.another"
	_, err := service.Register(context.Background(), "homeassistant", conflict)
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) || rejected.Code != RegistrationIdentityConflict {
		t.Fatalf("error = %v", err)
	}
	var name string
	if err := database.QueryRow("SELECT name FROM devices").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != original.Device.Name {
		t.Fatalf("failed registration changed device name to %q", name)
	}
}
