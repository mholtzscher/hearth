package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestRegistrationWithoutAdapterRollsBackAndRetainsHistoryAfterClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	service := newTestService(
		repository,
		nil,
		catalog,
		Dependencies{Now: func() time.Time { return now }},
	)
	if _, err := service.Register(
		ctx, "simulator", testRuntimeID, validDomainRegistration(),
	); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("registration before Adapter claim error = %v", err)
	}
	for _, table := range []string{
		"adapter_bindings", "devices", "entities", "adapter_entity_mappings",
	} {
		assertTableCount(t, database, table, 0)
	}

	if err := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, now),
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: now, ReceivedAt: now, LeaseExpiresAt: now.Add(adapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, reportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{{
			EntityID: binding.Entities[0].EntityID, Status: EntityAvailabilityAvailable,
			SourceObservedAt: now,
		}},
		ReportedAt: now,
	}); reportErr != nil {
		t.Fatal(reportErr)
	}
	history, err := repository.ListEntityAvailabilityHistory(ctx, ListEntityAvailabilityParams{
		EntityID: binding.Entities[0].EntityID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 2 || history.Items[0].Status != "available" ||
		history.Items[1].Status != "unknown" || history.Items[1].Source != healthSourceCore {
		t.Fatalf("Entity history after retry = %#v", history.Items)
	}
}

func TestRegistrationIsIdempotentAndUpdatesDescriptors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 20, 20, 0, 0, 123, time.FixedZone("test", -5*60*60))
	service := newTestService(
		NewSQLiteRepository(database, catalog),
		nil,
		catalog,
		Dependencies{Now: func() time.Time { return now }},
	)
	registration := validDomainRegistration()

	first, err := service.Register(ctx, "homeassistant", testAdapterRuntime("homeassistant"), registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Device.Name = "Renamed light"
	deviceExternalID := "ha-device-renamed"
	registration.Device.ExternalID = &deviceExternalID
	registration.Entities[0].Name = "Renamed power"
	registration.Entities[0].ExternalID = "light.office-renamed"
	now = now.Add(time.Minute)
	second, err := service.Register(ctx, "homeassistant", testAdapterRuntime("homeassistant"), registration)
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceID != first.DeviceID || second.Entities[0].EntityID != first.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}

	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	database = openRegistrationDatabase(t, path)
	var deviceName, entityName, storedDeviceExternalID, storedEntityExternalID, storedSupport string
	err = database.QueryRowContext(ctx, `
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
		t.Fatalf(
			"persisted descriptors = %q %q %q %q %s",
			deviceName,
			entityName,
			storedDeviceExternalID,
			storedEntityExternalID,
			storedSupport,
		)
	}
}

func TestMultiEntityRegistrationIsAdditiveAndReturnsSubmittedOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	service := newTestService(
		NewSQLiteRepository(database, catalog),
		nil,
		catalog,
		Dependencies{Now: func() time.Time { return now }},
	)

	power, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	multi := multiEntityRegistration()
	combined, err := service.Register(ctx, "homeassistant", testAdapterRuntime("homeassistant"), multi)
	if err != nil {
		t.Fatal(err)
	}
	if len(combined.Entities) != 2 || combined.Entities[0].Key != "power" || combined.Entities[1].Key != "brightness" {
		t.Fatalf("combined binding = %#v", combined)
	}
	if combined.DeviceID != power.DeviceID || combined.Entities[0].EntityID != power.Entities[0].EntityID {
		t.Fatalf("adding brightness changed existing IDs: power=%#v combined=%#v", power, combined)
	}

	reordered := copyRegistration(multi)
	reordered.Entities[0], reordered.Entities[1] = reordered.Entities[1], reordered.Entities[0]
	now = now.Add(time.Minute)
	reorderedBinding, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), reordered,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reorderedBinding.Entities) != 2 || reorderedBinding.Entities[0].Key != "brightness" ||
		reorderedBinding.Entities[0].EntityID != combined.Entities[1].EntityID ||
		reorderedBinding.Entities[1].Key != "power" || reorderedBinding.Entities[1].EntityID != combined.Entities[0].EntityID {
		t.Fatalf("reordered binding = %#v", reorderedBinding)
	}
	var powerEntityUpdatedAt, powerMappingUpdatedAt string
	if scanErr := database.QueryRowContext(ctx, `
		SELECT e.updated_at, m.updated_at
		FROM entities e JOIN adapter_entity_mappings m ON m.entity_id = e.id
		WHERE e.id = ?`, power.Entities[0].EntityID,
	).Scan(&powerEntityUpdatedAt, &powerMappingUpdatedAt); scanErr != nil {
		t.Fatal(scanErr)
	}

	brightnessOnly := copyRegistration(multi)
	brightnessOnly.Entities = brightnessOnly.Entities[1:]
	now = now.Add(time.Minute)
	omitted, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), brightnessOnly,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(omitted.Entities) != 1 || omitted.Entities[0] != combined.Entities[1] {
		t.Fatalf("submitted-only binding = %#v", omitted)
	}
	assertCounts(t, database, 1, 2)
	var powerName string
	if scanErr := database.QueryRowContext(ctx, "SELECT name FROM entities WHERE id = ?", power.Entities[0].EntityID).
		Scan(&powerName); scanErr != nil {
		t.Fatal(scanErr)
	}
	if powerName != "Power" {
		t.Fatalf("omitted power name = %q", powerName)
	}
	var omittedEntityUpdatedAt, omittedMappingUpdatedAt string
	if scanErr := database.QueryRowContext(ctx, `
		SELECT e.updated_at, m.updated_at
		FROM entities e JOIN adapter_entity_mappings m ON m.entity_id = e.id
		WHERE e.id = ?`, power.Entities[0].EntityID,
	).Scan(&omittedEntityUpdatedAt, &omittedMappingUpdatedAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	if omittedEntityUpdatedAt != powerEntityUpdatedAt || omittedMappingUpdatedAt != powerMappingUpdatedAt {
		t.Fatalf("omitted power was updated: entity %q -> %q, mapping %q -> %q",
			powerEntityUpdatedAt, omittedEntityUpdatedAt, powerMappingUpdatedAt, omittedMappingUpdatedAt)
	}
}

func TestRegistrationAllowsDeviceAggregateBeyondRequestLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities = make([]EntityDescriptor, 64)
	for index := range registration.Entities {
		registration.Entities[index] = registrationEntity(
			fmt.Sprintf("power-%d", index), fmt.Sprintf("light.office.%d", index),
		)
	}
	if _, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), registration,
	); err != nil {
		t.Fatal(err)
	}

	additional := validDomainRegistration()
	additional.Entities = []EntityDescriptor{registrationEntity("power-additional", "light.office.additional")}
	if _, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), additional,
	); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, database, 1, 65)
}

func TestRegistrationRejectsExternalIDTransfersIndependentOfOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original := multiEntityRegistration()
	if _, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), original,
	); err != nil {
		t.Fatal(err)
	}

	swap := copyRegistration(original)
	swap.Device.Name = "must roll back"
	swap.Entities[0].ExternalID, swap.Entities[1].ExternalID = swap.Entities[1].ExternalID, swap.Entities[0].ExternalID
	transfer := copyRegistration(original)
	transfer.Device.Name = "must roll back"
	transfer.Entities[0].ExternalID = "light.office.new"
	transfer.Entities = append(transfer.Entities, registrationEntity("alternate", "light.office"))
	for _, registration := range []Registration{
		swap,
		{BindingKey: swap.BindingKey, Device: swap.Device, Entities: []EntityDescriptor{swap.Entities[1], swap.Entities[0]}},
		transfer,
		{BindingKey: transfer.BindingKey, Device: transfer.Device, Entities: []EntityDescriptor{transfer.Entities[2], transfer.Entities[1], transfer.Entities[0]}},
	} {
		_, err := service.Register(
			ctx, "homeassistant", testAdapterRuntime("homeassistant"), registration,
		)
		assertRegistrationRejection(t, err, RegistrationIdentityConflict)
	}
	assertCounts(t, database, 1, 2)
	var deviceName string
	if err := database.QueryRowContext(ctx, "SELECT name FROM devices").Scan(&deviceName); err != nil {
		t.Fatal(err)
	}
	if deviceName != original.Device.Name {
		t.Fatalf("failed identity reconciliation changed device name to %q", deviceName)
	}
}

func TestExternalIDCanTransferAcrossRegistrations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}

	release := validDomainRegistration()
	release.Entities[0].ExternalID = "light.office.new"
	released, err := service.Register(ctx, "homeassistant", testAdapterRuntime("homeassistant"), release)
	if err != nil {
		t.Fatal(err)
	}
	claim := validDomainRegistration()
	claim.Entities = []EntityDescriptor{registrationEntity("alternate", "light.office")}
	claimed, err := service.Register(ctx, "homeassistant", testAdapterRuntime("homeassistant"), claim)
	if err != nil {
		t.Fatal(err)
	}
	if released.Entities[0].EntityID != original.Entities[0].EntityID ||
		claimed.Entities[0].EntityID == original.Entities[0].EntityID {
		t.Fatalf("release and claim bindings: original=%#v released=%#v claimed=%#v", original, released, claimed)
	}
	assertCounts(t, database, 1, 2)
}

func TestRegistrationRollsBackWhenLaterEntityWriteFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{
		NewEntityID: func() (EntityID, error) {
			return EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"), nil
		},
	})

	if _, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), multiEntityRegistration(),
	); err == nil {
		t.Fatal("registration unexpectedly succeeded with duplicate generated Entity IDs")
	}
	for _, table := range []string{"devices", "entities", "adapter_bindings", "adapter_entity_mappings"} {
		var count int
		if err := database.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d after rollback", table, count)
		}
	}
}

func TestReRegistrationReplacesNormalizedSupport(t *testing.T) {
	t.Parallel()
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

	stateCodec := compileTestCodec[bool](t, "repository-state", `{"type":"boolean"}`)
	supportCodec := compileTestCodec[support](t, "repository-support", `{
		"type":"object","required":["state","operations"],"additionalProperties":false,
		"properties":{
			"state":{"type":"object","required":["mode"],"properties":{"mode":{"type":"string"}},"additionalProperties":false},
			"operations":{"type":"object","required":["set"],"properties":{"set":{"type":"object","maxProperties":0}},"additionalProperties":false}
		}}`)
	parametersCodec := compileTestCodec[parameters](
		t,
		"repository-parameters",
		`{"type":"object","required":["value"],"properties":{"value":{"type":"boolean"}},"additionalProperties":false}`,
	)
	set := DefineOperation(
		OperationNameSet,
		parametersCodec,
		func(value support) (struct{}, bool) { return value.Operations.Set, true },
		func(_ support, _ struct{}, _ parameters) error { return nil },
		time.Second,
		func(parameters parameters, state bool) bool { return parameters.Value == state },
	)
	definition, err := DefineEntityType(
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
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = "test.mutable/v1"
	registration.Entities[0].Support = EntitySupport(`{"state":{"mode":"first"},"operations":{"set":{}}}`)
	first, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Entities[0].Support = EntitySupport(`{ "operations": { "set": {} }, "state": { "mode": "second" } }`)
	second, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || first.Entities[0].EntityID != second.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}

	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	database = openRegistrationDatabase(t, path)
	var stored string
	if scanErr := database.QueryRowContext(ctx, "SELECT support_json FROM entities WHERE id = ?", second.Entities[0].EntityID).
		Scan(&stored); scanErr != nil {
		t.Fatal(scanErr)
	}
	if stored != `{"state":{"mode":"second"},"operations":{"set":{}}}` {
		t.Fatalf("stored support = %s", stored)
	}
}

func TestConcurrentRegistrationReturnsOneBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	const attempts = 8
	results := make(chan Binding, attempts)
	errors := make(chan error, attempts)
	for range attempts {
		go func() {
			binding, err := service.Register(
				ctx, "homeassistant", testAdapterRuntime("homeassistant"), multiEntityRegistration(),
			)
			results <- binding
			errors <- err
		}()
	}
	var first Binding
	for index := range attempts {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		binding := <-results
		if index == 0 {
			first = binding
			continue
		}
		if binding.DeviceID != first.DeviceID || len(binding.Entities) != 2 ||
			binding.Entities[0].EntityID != first.Entities[0].EntityID ||
			binding.Entities[1].EntityID != first.Entities[1].EntityID {
			t.Fatalf("concurrent registration changed IDs: first=%#v got=%#v", first, binding)
		}
	}
	assertCounts(t, database, 1, 2)
}

func TestRegistrationRejectionsAreAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original := validDomainRegistration()
	if _, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), original,
	); err != nil {
		t.Fatal(err)
	}

	typeChange := validDomainRegistration()
	typeChange.Device.Name = "must roll back"
	typeChange.Entities[0].TypeID = "example.changed/v1"
	_, err := service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), typeChange,
	)
	assertRegistrationRejection(t, err, RegistrationInvalidDescriptor)

	// Inject a catalog-known alternate type so the repository, rather than catalog
	// validation, owns and atomically rejects the immutable type change.
	powerDefinition, err := newPowerV1TypeDefinition(EntityTypePowerV1)
	if err != nil {
		t.Fatal(err)
	}
	alternateDefinition, err := newPowerV1TypeDefinition("example.changed/v1")
	if err != nil {
		t.Fatal(err)
	}
	alternateCatalog, err := NewTypeCatalog([]EntityTypeDefinition{powerDefinition, alternateDefinition})
	if err != nil {
		t.Fatal(err)
	}
	service = newTestService(NewSQLiteRepository(database, alternateCatalog), nil, alternateCatalog, Dependencies{})
	_, err = service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), typeChange,
	)
	assertRegistrationRejection(t, err, RegistrationImmutableTypeChange)
	assertCounts(t, database, 1, 1)
	var deviceName string
	if scanErr := database.QueryRowContext(ctx, "SELECT name FROM devices").Scan(&deviceName); scanErr != nil {
		t.Fatal(scanErr)
	}
	if deviceName != original.Device.Name {
		t.Fatalf("failed type change partially updated device name to %q", deviceName)
	}

	conflict := validDomainRegistration()
	conflict.BindingKey = "other-light"
	otherExternalID := "other-device"
	conflict.Device.ExternalID = &otherExternalID
	_, err = service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), conflict,
	)
	assertRegistrationRejection(t, err, RegistrationIdentityConflict)
	assertCounts(t, database, 1, 1)

	invalid := validDomainRegistration()
	invalid.Entities[0].Support = EntitySupport(`{"state":{"unexpected":true},"operations":{"set":{}}}`)
	_, err = service.Register(
		ctx, "homeassistant", testAdapterRuntime("homeassistant"), invalid,
	)
	assertRegistrationRejection(t, err, RegistrationInvalidDescriptor)
	assertCounts(t, database, 1, 1)
}

func TestRegistrationInitialEnablementAndRetryPreservesCurrentValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := newTestService(
		NewSQLiteRepository(database, catalog),
		nil,
		catalog,
		Dependencies{Now: func() time.Time { return now }},
	)

	registration := validDomainRegistration()
	initiallyEnabled := false
	registration.Entities[0].InitiallyEnabled = &initiallyEnabled
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Entities[0].Enabled {
		t.Fatalf("initial binding = %#v", binding.Entities[0])
	}
	view, err := service.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Enabled {
		t.Fatalf("initial Entity = %#v", view.Entity)
	}

	now = now.Add(time.Minute)
	if _, enablementErr := service.SetEntityEnabled(ctx, view.Entity.ID, true); enablementErr != nil {
		t.Fatal(enablementErr)
	}
	now = now.Add(time.Minute)
	retried, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	if !retried.Entities[0].Enabled || retried.Entities[0].EntityID != binding.Entities[0].EntityID {
		t.Fatalf("retried binding = %#v", retried.Entities[0])
	}
}

func TestSetEntityEnabledIsIdempotentAndOwnerScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := newTestService(
		NewSQLiteRepository(database, catalog),
		nil,
		catalog,
		Dependencies{Now: func() time.Time { return now }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID

	var registeredAt string
	if scanErr := database.QueryRowContext(ctx, "SELECT updated_at FROM entities WHERE id = ?", entityID).
		Scan(&registeredAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	now = now.Add(time.Minute)
	confirmed, err := service.SetOwnedEntityEnabled(ctx, "simulator", testRuntimeID, entityID, false)
	if err != nil || confirmed {
		t.Fatalf("owner disable = %t, %v", confirmed, err)
	}
	var disabledAt string
	if scanErr := database.QueryRowContext(ctx, "SELECT updated_at FROM entities WHERE id = ?", entityID).
		Scan(&disabledAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	if disabledAt == registeredAt {
		t.Fatal("changed enablement did not update updated_at")
	}

	now = now.Add(time.Minute)
	view, err := service.SetEntityEnabled(ctx, entityID, false)
	if err != nil || view.Entity.Enabled {
		t.Fatalf("management no-op = %#v, %v", view, err)
	}
	var afterNoop string
	if scanErr := database.QueryRowContext(ctx, "SELECT updated_at FROM entities WHERE id = ?", entityID).
		Scan(&afterNoop); scanErr != nil {
		t.Fatal(scanErr)
	}
	if afterNoop != disabledAt {
		t.Fatalf("no-op updated_at = %q, want %q", afterNoop, disabledAt)
	}
	if _, enableErr := service.SetOwnedEntityEnabled(
		ctx,
		"homeassistant",
		testAdapterRuntime("homeassistant"),
		entityID,
		true,
	); !errors.Is(
		enableErr,
		ErrEntityWrongAdapter,
	) {
		t.Fatalf("wrong owner error = %v", enableErr)
	}
	unknown, entityIDErr := NewEntityID()
	if entityIDErr != nil {
		t.Fatal(entityIDErr)
	}
	if _, enablementErr := service.SetEntityEnabled(ctx, unknown, true); !errors.Is(enablementErr, ErrEntityNotFound) {
		t.Fatalf("unknown Entity error = %v", enablementErr)
	}
}

func TestRegistrationAndEnablementFenceStaleRuntimeBeforeWriting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 20, 0, 0, 1, 0, time.UTC)
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	registration := validDomainRegistration()
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: now.Add(time.Second),
	}); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testSecondRuntime, now.Add(2*time.Second)),
	); claimErr != nil {
		t.Fatal(claimErr)
	}

	registration.Device.Name = "must not be stored"
	registration.Entities[0].Name = "must not be stored"
	now = now.Add(3 * time.Second)
	if _, registerErr := service.Register(
		ctx, "simulator", testRuntimeID, registration,
	); !errors.Is(registerErr, ErrRuntimeFenced) {
		t.Fatalf("stale registration error = %v", registerErr)
	}
	if _, enableErr := service.SetOwnedEntityEnabled(
		ctx, "simulator", testRuntimeID, binding.Entities[0].EntityID, false,
	); !errors.Is(enableErr, ErrRuntimeFenced) {
		t.Fatalf("stale enablement error = %v", enableErr)
	}
	view, err := service.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Name != "Power" || !view.Entity.Enabled {
		t.Fatalf("Entity changed after stale writes: %#v", view.Entity)
	}

	registration.Device.Name = "Replacement runtime light"
	registration.Entities[0].Name = "Replacement power"
	if _, registerErr := service.Register(
		ctx, "simulator", testSecondRuntime, registration,
	); registerErr != nil {
		t.Fatal(registerErr)
	}
	confirmed, enableErr := service.SetOwnedEntityEnabled(
		ctx, "simulator", testSecondRuntime, binding.Entities[0].EntityID, false,
	)
	if enableErr != nil || confirmed {
		t.Fatalf("replacement enablement = %t, %v", confirmed, enableErr)
	}
	view, err = service.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Name != "Replacement power" || view.Entity.Enabled {
		t.Fatalf("replacement writes = %#v", view.Entity)
	}
}

func TestCreateCommandDurablyClassifiesDisabledEntity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	if _, enablementErr := service.SetEntityEnabled(ctx, entityID, false); enablementErr != nil {
		t.Fatal(enablementErr)
	}
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	candidate := newCommandRecord(t, entityID, requestedAt)
	created, err := repository.CreateCommand(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != CommandStatusEntityDisabled || created.AcceptedAt != nil ||
		created.CompletedAt == nil || !created.CompletedAt.Equal(requestedAt) ||
		created.OutcomeObservationID != nil || created.FailureCode == nil ||
		*created.FailureCode != CommandFailureEntityDisabled {
		t.Fatalf("created Command = %#v", created)
	}
	stored, err := repository.GetCommand(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != created.Status || stored.CompletedAt == nil || !stored.CompletedAt.Equal(requestedAt) ||
		stored.FailureCode == nil || *stored.FailureCode != CommandFailureEntityDisabled ||
		string(stored.Parameters) != string(candidate.Parameters) || stored.AdapterID != candidate.AdapterID {
		t.Fatalf("stored Command = %#v", stored)
	}
}

func TestCreateCommandDispatchesWhenHealthyEntityIsReportedUnavailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := time.Date(2026, 8, 20, 0, 0, 1, 0, time.UTC)
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt,
		LeaseExpiresAt: healthyAt.Add(adapterLeaseDuration),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	if _, reportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{{
			EntityID: binding.Entities[0].EntityID, Status: EntityAvailabilityUnavailable,
			SourceObservedAt: healthyAt,
			Reason:           &HealthReason{Code: "adapter.hearth-simulator.entity_unavailable"},
		}},
		ReportedAt: healthyAt,
	}); reportErr != nil {
		t.Fatal(reportErr)
	}

	candidate := newCommandRecord(t, binding.Entities[0].EntityID, healthyAt.Add(time.Second))
	created, err := repository.CreateCommand(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != CommandStatusRequested || created.RuntimeID == nil ||
		*created.RuntimeID != testRuntimeID || created.CompletedAt != nil || created.FailureCode != nil {
		t.Fatalf("Command for unavailable Entity = %#v", created)
	}
}

func TestCommandCreationAndEnablementFollowCommitOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	claimTestAdapterRuntime(t, repository, requestedAt)

	commandFirst := newCommandRecord(t, entityID, requestedAt)
	created, err := repository.CreateCommand(ctx, commandFirst)
	if err != nil || created.Status != CommandStatusRequested {
		t.Fatalf("Command-first creation = %#v, %v", created, err)
	}
	if _, enablementErr := service.SetEntityEnabled(ctx, entityID, false); enablementErr != nil {
		t.Fatal(enablementErr)
	}
	stored, err := repository.GetCommand(ctx, commandFirst.ID)
	if err != nil || stored.Status != CommandStatusRequested {
		t.Fatalf("Command after later disable = %#v, %v", stored, err)
	}

	disableFirst := newCommandRecord(t, entityID, requestedAt.Add(time.Second))
	created, err = repository.CreateCommand(ctx, disableFirst)
	if err != nil || created.Status != CommandStatusEntityDisabled {
		t.Fatalf("disable-first creation = %#v, %v", created, err)
	}
}

func TestCommandRuntimeIsNotRetargetedAfterTakeover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	first := newCommandRecord(t, binding.Entities[0].EntityID, claimedAt.Add(time.Second))
	first, err = repository.CreateCommand(ctx, first)
	if err != nil || first.RuntimeID == nil || *first.RuntimeID != testRuntimeID {
		t.Fatalf("first runtime Command = %#v, %v", first, err)
	}
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: claimedAt.Add(2 * time.Second),
	}); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testSecondRuntime, claimedAt.Add(3*time.Second)),
	); claimErr != nil {
		t.Fatal(claimErr)
	}
	second := newCommandRecord(t, binding.Entities[0].EntityID, claimedAt.Add(4*time.Second))
	second, err = repository.CreateCommand(ctx, second)
	if err != nil || second.RuntimeID == nil || *second.RuntimeID != testSecondRuntime {
		t.Fatalf("replacement runtime Command = %#v, %v", second, err)
	}
	storedFirst, err := repository.GetCommand(ctx, first.ID)
	if err != nil || storedFirst.RuntimeID == nil || *storedFirst.RuntimeID != testRuntimeID {
		t.Fatalf("stored first Command = %#v, %v", storedFirst, err)
	}
}

//nolint:gocognit,gocyclo,cyclop // The command transition matrix is clearer as one persistence test.
func TestCommandLedgerTransitionsAreMonotonicAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	binding, registrationErr := newTestService(
		repository,
		nil,
		catalog,
		Dependencies{},
	).Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if registrationErr != nil {
		t.Fatal(registrationErr)
	}
	requestedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	claimTestAdapterRuntime(t, repository, requestedAt)
	command := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt)
	if _, err := repository.CreateCommand(ctx, command); err != nil {
		t.Fatal(err)
	}
	acceptedAt := requestedAt.Add(time.Second)
	if err := repository.MarkCommandAccepted(ctx, command.ID, acceptedAt); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCommandAccepted(ctx, command.ID, acceptedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, lookupErr := repository.GetCommand(ctx, command.ID)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if stored.Status != CommandStatusAccepted || stored.AcceptedAt == nil || !stored.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("accepted command = %#v", stored)
	}
	completedAt := requestedAt.Add(2 * time.Second)
	completion := CommandCompletion{
		ID: command.ID, Status: CommandStatusRejected, CompletedAt: completedAt,
		FailureCode: CommandFailureUpstreamRejected,
	}
	if err := repository.CompleteCommand(ctx, completion); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCommand(ctx, completion); err != nil {
		t.Fatalf("repeat completion: %v", err)
	}
	if err := repository.MarkCommandAccepted(ctx, command.ID, completedAt); !errors.Is(err, ErrCommandTerminal) {
		t.Fatalf("accept terminal command error = %v", err)
	}
	conflicting := completion
	conflicting.Status = CommandStatusOutcomeTimeout
	conflicting.FailureCode = CommandFailureOutcomeTimeout
	if err := repository.CompleteCommand(ctx, conflicting); !errors.Is(err, ErrCommandTerminal) {
		t.Fatalf("conflicting completion error = %v", err)
	}

	satisfied := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt.Add(30*time.Second))
	if _, err := repository.CreateCommand(ctx, satisfied); err != nil {
		t.Fatal(err)
	}
	observationID, observationIDErr := NewObservationID()
	if observationIDErr != nil {
		t.Fatal(observationIDErr)
	}
	satisfiedAt := satisfied.RequestedAt.Add(time.Second)
	if _, err := database.ExecContext(ctx, `
		UPDATE commands
		SET status = 'satisfied', completed_at = ?, outcome_observation_id = ?
		WHERE id = ?`, formatTime(satisfiedAt), observationID, satisfied.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCommandAccepted(ctx, satisfied.ID, satisfiedAt.Add(time.Second)); err != nil {
		t.Fatalf("accept after linked outcome: %v", err)
	}
	storedSatisfied, lookupErr := repository.GetCommand(ctx, satisfied.ID)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if storedSatisfied.Status != CommandStatusSatisfied || storedSatisfied.AcceptedAt == nil {
		t.Fatalf("acceptance regressed satisfied command: %#v", storedSatisfied)
	}

	requested := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt.Add(time.Minute))
	accepted := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt.Add(2*time.Minute))
	if _, err := repository.CreateCommand(ctx, requested); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCommand(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCommandAccepted(ctx, accepted.ID, accepted.RequestedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restartedAt := requestedAt.Add(3 * time.Minute)
	if err := repository.CompleteCommand(ctx, CommandCompletion{
		ID: requested.ID, Status: CommandStatusInterrupted, CompletedAt: restartedAt,
		FailureCode: CommandFailureCoreRestarted,
	}); err == nil {
		t.Fatal("per-command interruption unexpectedly accepted")
	}
	stillRequested, lookupErr := repository.GetCommand(ctx, requested.ID)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if stillRequested.Status != CommandStatusRequested {
		t.Fatalf("rejected per-command interruption changed status to %q", stillRequested.Status)
	}
	if err := repository.InterruptActiveCommands(ctx, restartedAt); err != nil {
		t.Fatal(err)
	}
	if err := repository.InterruptActiveCommands(ctx, restartedAt.Add(time.Second)); err != nil {
		t.Fatalf("repeat interruption: %v", err)
	}
	for _, id := range []CommandID{requested.ID, accepted.ID} {
		interrupted, interruptedErr := repository.GetCommand(ctx, id)
		if interruptedErr != nil {
			t.Fatal(interruptedErr)
		}
		if interrupted.Status != CommandStatusInterrupted || interrupted.FailureCode == nil ||
			*interrupted.FailureCode != CommandFailureCoreRestarted || interrupted.CompletedAt == nil ||
			!interrupted.CompletedAt.Equal(restartedAt) {
			t.Fatalf("interrupted command = %#v", interrupted)
		}
	}
}

func openMigratedDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, openErr := platformdb.Open(context.Background(), path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

func openRegistrationDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database := openMigratedDatabase(t, path)
	repository := NewSQLiteRepository(database, nil)
	claimedAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	for _, adapterID := range []string{"homeassistant", "simulator"} {
		if err := repository.ClaimAdapterRuntime(
			context.Background(),
			testAdapterClaim(adapterID, claimedAt),
		); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func testAdapterClaim(adapterID string, claimedAt time.Time) ClaimRuntimeWrite {
	runtimeID := testAdapterRuntime(adapterID)
	softwareName := "hearth-simulator"
	if adapterID == "homeassistant" {
		softwareName = "hearth-adapter-homeassistant"
	}
	return ClaimRuntimeWrite{
		RuntimeID: runtimeID, AdapterID: adapterID,
		SoftwareName: softwareName, SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(adapterLeaseDuration),
	}
}

func testAdapterRuntime(adapterID string) RuntimeID {
	if adapterID == "homeassistant" {
		return RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
	}
	return testRuntimeID
}

func firstLightCatalog(t *testing.T) *TypeCatalog {
	t.Helper()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func validDomainRegistration() Registration {
	externalID := "ha-device"
	return Registration{
		BindingKey: "office-light",
		Device:     DeviceDescriptor{ExternalID: &externalID, Name: "Office light", Kind: DeviceKindLight},
		Entities: []EntityDescriptor{{
			Key: "power", ExternalID: "light.office", Name: "Power", TypeID: EntityTypePowerV1,
			Support: EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	}
}

func multiEntityRegistration() Registration {
	registration := validDomainRegistration()
	registration.Entities = append(registration.Entities, EntityDescriptor{
		Key: "brightness", ExternalID: "light.office.brightness", Name: "Brightness", TypeID: EntityTypeBrightnessV1,
		Support: EntitySupport(`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`),
	})
	return registration
}

func assertRegistrationRejection(t *testing.T, err error, code RegistrationRejectionCode) {
	t.Helper()
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) || rejected.Code != code {
		t.Fatalf("registration error = %v, want rejection %q", err, code)
	}
}

//nolint:unparam // Call sites keep both expected table counts explicit.
func assertCounts(t *testing.T, database *sql.DB, devices, entities int) {
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

func claimTestAdapterRuntime(t *testing.T, repository *SQLiteRepository, claimedAt time.Time) {
	t.Helper()
	runtimeID := RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	err := repository.ClaimAdapterRuntime(context.Background(), ClaimRuntimeWrite{
		RuntimeID: runtimeID,
		AdapterID: "simulator", SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(15 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newCommandRecord(t *testing.T, entityID EntityID, requestedAt time.Time) CommandRecord {
	t.Helper()
	id, err := NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	return CommandRecord{
		ID: id, EntityID: entityID, AdapterID: "simulator", OperationName: OperationNameSet,
		Parameters: CommandParameters(`{"value":true}`), CorrelationID: correlationID,
		Status: CommandStatusRequested, RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(10 * time.Second),
	}
}
