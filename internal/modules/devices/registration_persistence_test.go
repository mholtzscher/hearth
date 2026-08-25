package devices

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestRegistrationIsIdempotentAndUpdatesDescriptors(t *testing.T) {
	now := time.Date(2026, 8, 20, 20, 0, 0, 123, time.FixedZone("test", -5*60*60))
	controls := productionServiceControls()
	controls.now = func() time.Time { return now }
	service, database := newRunningDeviceTestService(t, controls, acceptingTestDelivery())
	registration := validDomainRegistration()

	first, err := service.Register(context.Background(), "homeassistant", registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Device.Name = "Renamed light"
	deviceExternalID := "ha-device-renamed"
	registration.Device.ExternalID = &deviceExternalID
	registration.Entities[0].Name = "Renamed power"
	registration.Entities[0].ExternalID = "light.office-renamed"
	now = now.Add(time.Minute)
	second, err := service.Register(context.Background(), "homeassistant", registration)
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceID != first.DeviceID || second.Entities[0].EntityID != first.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}

	var deviceName, entityName, storedDeviceExternalID, storedEntityExternalID, storedSupport string
	err = database.QueryRowContext(context.Background(), `
		SELECT d.name, e.name, b.external_device_id, m.external_entity_id, e.support_json
		FROM devices d
		JOIN adapter_bindings b ON b.device_id = d.id
		JOIN adapter_entity_mappings m
		  ON m.adapter_id = b.adapter_id AND m.binding_key = b.binding_key
		JOIN entities e ON e.id = m.entity_id
		WHERE b.adapter_id = ? AND b.binding_key = ?`, "homeassistant", registration.BindingKey,
	).Scan(&deviceName, &entityName, &storedDeviceExternalID, &storedEntityExternalID, &storedSupport)
	if err != nil {
		t.Fatal(err)
	}
	if deviceName != "Renamed light" || entityName != "Renamed power" ||
		storedDeviceExternalID != deviceExternalID || storedEntityExternalID != "light.office-renamed" ||
		storedSupport != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("persisted descriptors = %q %q %q %q %s", deviceName, entityName, storedDeviceExternalID, storedEntityExternalID, storedSupport)
	}
}

func TestReRegistrationReplacesNormalizedSupport(t *testing.T) {
	type stateSupport struct {
		Mode string `json:"mode"`
	}
	type operations struct {
		Set struct{} `json:"set"`
	}
	type support struct {
		State      stateSupport `json:"state"`
		Operations operations   `json:"operations"`
	}
	type parameters struct {
		Value bool `json:"value"`
	}

	stateCodec := compileTestCodec[bool](t, "registration-state", `{"type":"boolean"}`)
	supportCodec := compileTestCodec[support](t, "registration-support", `{
		"type":"object","required":["state","operations"],"additionalProperties":false,
		"properties":{
			"state":{"type":"object","required":["mode"],"properties":{"mode":{"type":"string"}},"additionalProperties":false},
			"operations":{"type":"object","required":["set"],"properties":{"set":{"type":"object","maxProperties":0}},"additionalProperties":false}
		}}`)
	parametersCodec := compileTestCodec[parameters](t, "registration-parameters", `{"type":"object","required":["value"],"properties":{"value":{"type":"boolean"}},"additionalProperties":false}`)
	set := defineOperation(
		OperationNameSet,
		parametersCodec,
		func(value support) (struct{}, bool) { return value.Operations.Set, true },
		func(_ support, _ struct{}, _ parameters) error { return nil },
		time.Second,
		func(parameters parameters, state bool) bool { return parameters.Value == state },
	)
	definition, err := defineEntityType(
		"test.mutable/v1",
		stateCodec,
		supportCodec,
		func(_ support, _ bool) error { return nil },
		func(left, right bool) bool { return left == right },
		set,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := newTypeCatalog([]entityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	controls := productionServiceControls()
	controls.newCatalog = func() (*typeCatalog, error) { return catalog, nil }
	service, database := newRunningDeviceTestService(t, controls, acceptingTestDelivery())
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = "test.mutable/v1"
	registration.Entities[0].Support = EntitySupport(`{"state":{"mode":"first"},"operations":{"set":{}}}`)
	first, err := service.Register(context.Background(), "simulator", registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Entities[0].Support = EntitySupport(`{ "operations": { "set": {} }, "state": { "mode": "second" } }`)
	second, err := service.Register(context.Background(), "simulator", registration)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || first.Entities[0].EntityID != second.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}
	var stored string
	if err := database.QueryRow("SELECT support_json FROM entities WHERE id = ?", second.Entities[0].EntityID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"state":{"mode":"second"},"operations":{"set":{}}}` {
		t.Fatalf("stored support = %s", stored)
	}
}

func TestConcurrentRegistrationReturnsOneBinding(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	const attempts = 8
	results := make(chan Binding, attempts)
	errorsChannel := make(chan error, attempts)
	for range attempts {
		go func() {
			binding, err := service.Register(context.Background(), "homeassistant", validDomainRegistration())
			results <- binding
			errorsChannel <- err
		}()
	}
	var first Binding
	for index := range attempts {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
		binding := <-results
		if index == 0 {
			first = binding
			continue
		}
		if binding.DeviceID != first.DeviceID || binding.Entities[0].EntityID != first.Entities[0].EntityID {
			t.Fatalf("concurrent registration changed IDs: first=%#v got=%#v", first, binding)
		}
	}
	assertRegistrationCounts(t, database, 1, 1)
}

func TestRegistrationRejectionsAreAtomic(t *testing.T) {
	powerDefinition, err := newPowerV1TypeDefinition(EntityTypePowerV1)
	if err != nil {
		t.Fatal(err)
	}
	alternateDefinition, err := newPowerV1TypeDefinition("example.changed/v1")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := newTypeCatalog([]entityTypeDefinition{powerDefinition, alternateDefinition})
	if err != nil {
		t.Fatal(err)
	}
	controls := productionServiceControls()
	controls.newCatalog = func() (*typeCatalog, error) { return catalog, nil }
	service, database := newRunningDeviceTestService(t, controls, acceptingTestDelivery())
	original := validDomainRegistration()
	if _, err := service.Register(context.Background(), "homeassistant", original); err != nil {
		t.Fatal(err)
	}

	secondEntity := validDomainRegistration()
	secondEntity.Entities[0].Key = "alternate-power"
	secondEntity.Entities[0].ExternalID = "light.office-alternate"
	_, err = service.Register(context.Background(), "homeassistant", secondEntity)
	assertRegistrationRejection(t, err, RegistrationIdentityConflict)
	assertRegistrationCounts(t, database, 1, 1)

	typeChange := validDomainRegistration()
	typeChange.Device.Name = "must roll back"
	typeChange.Entities[0].TypeID = "example.changed/v1"
	_, err = service.Register(context.Background(), "homeassistant", typeChange)
	assertRegistrationRejection(t, err, RegistrationImmutableTypeChange)
	assertRegistrationCounts(t, database, 1, 1)
	var deviceName string
	if err := database.QueryRow("SELECT name FROM devices").Scan(&deviceName); err != nil {
		t.Fatal(err)
	}
	if deviceName != original.Device.Name {
		t.Fatalf("failed type change partially updated device name to %q", deviceName)
	}

	conflict := validDomainRegistration()
	conflict.BindingKey = "other-light"
	otherExternalID := "other-device"
	conflict.Device.ExternalID = &otherExternalID
	_, err = service.Register(context.Background(), "homeassistant", conflict)
	assertRegistrationRejection(t, err, RegistrationIdentityConflict)
	assertRegistrationCounts(t, database, 1, 1)
}

func assertRegistrationRejection(t *testing.T, err error, code RegistrationRejectionCode) {
	t.Helper()
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) || rejected.Code != code {
		t.Fatalf("registration error = %v, want rejection %q", err, code)
	}
}

func assertRegistrationCounts(t *testing.T, database *sql.DB, devices, entities int) {
	t.Helper()
	for table, want := range map[string]int{"devices": devices, "entities": entities} {
		var got int
		if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
}
