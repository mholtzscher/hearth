package devices

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestRegistrationIsIdempotentAndUpdatesDescriptors(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openMigratedDatabase(t, path)
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 20, 20, 0, 0, 123, time.FixedZone("test", -5*60*60))
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{Now: func() time.Time { return now }})
	registration := validDomainRegistration()

	first, err := service.Register(ctx, "homeassistant", registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Device.Name = "Renamed light"
	deviceExternalID := "ha-device-renamed"
	registration.Device.ExternalID = &deviceExternalID
	registration.Entities[0].Name = "Renamed power"
	registration.Entities[0].ExternalID = "light.office-renamed"
	now = now.Add(time.Minute)
	second, err := service.Register(ctx, "homeassistant", registration)
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceID != first.DeviceID || second.Entities[0].EntityID != first.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openMigratedDatabase(t, path)
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
		t.Fatalf("persisted descriptors = %q %q %q %q %s", deviceName, entityName, storedDeviceExternalID, storedEntityExternalID, storedSupport)
	}
}

func TestMultiEntityRegistrationIsAdditiveAndReturnsSubmittedOrder(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{Now: func() time.Time { return now }})

	power, err := service.Register(ctx, "homeassistant", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	multi := multiEntityRegistration()
	combined, err := service.Register(ctx, "homeassistant", multi)
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
	reorderedBinding, err := service.Register(ctx, "homeassistant", reordered)
	if err != nil {
		t.Fatal(err)
	}
	if len(reorderedBinding.Entities) != 2 || reorderedBinding.Entities[0].Key != "brightness" ||
		reorderedBinding.Entities[0].EntityID != combined.Entities[1].EntityID ||
		reorderedBinding.Entities[1].Key != "power" || reorderedBinding.Entities[1].EntityID != combined.Entities[0].EntityID {
		t.Fatalf("reordered binding = %#v", reorderedBinding)
	}
	var powerEntityUpdatedAt, powerMappingUpdatedAt string
	if err := database.QueryRowContext(ctx, `
		SELECT e.updated_at, m.updated_at
		FROM entities e JOIN adapter_entity_mappings m ON m.entity_id = e.id
		WHERE e.id = ?`, power.Entities[0].EntityID,
	).Scan(&powerEntityUpdatedAt, &powerMappingUpdatedAt); err != nil {
		t.Fatal(err)
	}

	brightnessOnly := copyRegistration(multi)
	brightnessOnly.Entities = brightnessOnly.Entities[1:]
	now = now.Add(time.Minute)
	omitted, err := service.Register(ctx, "homeassistant", brightnessOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(omitted.Entities) != 1 || omitted.Entities[0] != combined.Entities[1] {
		t.Fatalf("submitted-only binding = %#v", omitted)
	}
	assertCounts(t, database, 1, 2)
	var powerName string
	if err := database.QueryRowContext(ctx, "SELECT name FROM entities WHERE id = ?", power.Entities[0].EntityID).Scan(&powerName); err != nil {
		t.Fatal(err)
	}
	if powerName != "Power" {
		t.Fatalf("omitted power name = %q", powerName)
	}
	var omittedEntityUpdatedAt, omittedMappingUpdatedAt string
	if err := database.QueryRowContext(ctx, `
		SELECT e.updated_at, m.updated_at
		FROM entities e JOIN adapter_entity_mappings m ON m.entity_id = e.id
		WHERE e.id = ?`, power.Entities[0].EntityID,
	).Scan(&omittedEntityUpdatedAt, &omittedMappingUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if omittedEntityUpdatedAt != powerEntityUpdatedAt || omittedMappingUpdatedAt != powerMappingUpdatedAt {
		t.Fatalf("omitted power was updated: entity %q -> %q, mapping %q -> %q",
			powerEntityUpdatedAt, omittedEntityUpdatedAt, powerMappingUpdatedAt, omittedMappingUpdatedAt)
	}
}

func TestRegistrationRejectsExternalIDTransfersIndependentOfOrder(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original := multiEntityRegistration()
	if _, err := service.Register(ctx, "homeassistant", original); err != nil {
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
		_, err := service.Register(ctx, "homeassistant", registration)
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
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original, err := service.Register(ctx, "homeassistant", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}

	release := validDomainRegistration()
	release.Entities[0].ExternalID = "light.office.new"
	released, err := service.Register(ctx, "homeassistant", release)
	if err != nil {
		t.Fatal(err)
	}
	claim := validDomainRegistration()
	claim.Entities = []EntityDescriptor{registrationEntity("alternate", "light.office")}
	claimed, err := service.Register(ctx, "homeassistant", claim)
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
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{
		NewEntityID: func() (EntityID, error) {
			return EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"), nil
		},
	})

	if _, err := service.Register(ctx, "homeassistant", multiEntityRegistration()); err == nil {
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
	parametersCodec := compileTestCodec[parameters](t, "repository-parameters", `{"type":"object","required":["value"],"properties":{"value":{"type":"boolean"}},"additionalProperties":false}`)
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
	database := openMigratedDatabase(t, path)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = "test.mutable/v1"
	registration.Entities[0].Support = EntitySupport(`{"state":{"mode":"first"},"operations":{"set":{}}}`)
	first, err := service.Register(ctx, "simulator", registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Entities[0].Support = EntitySupport(`{ "operations": { "set": {} }, "state": { "mode": "second" } }`)
	second, err := service.Register(ctx, "simulator", registration)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || first.Entities[0].EntityID != second.Entities[0].EntityID {
		t.Fatalf("re-registration changed IDs: first=%#v second=%#v", first, second)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openMigratedDatabase(t, path)
	var stored string
	if err := database.QueryRowContext(ctx, "SELECT support_json FROM entities WHERE id = ?", second.Entities[0].EntityID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"state":{"mode":"second"},"operations":{"set":{}}}` {
		t.Fatalf("stored support = %s", stored)
	}
}

func TestConcurrentRegistrationReturnsOneBinding(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	const attempts = 8
	results := make(chan Binding, attempts)
	errors := make(chan error, attempts)
	for range attempts {
		go func() {
			binding, err := service.Register(ctx, "homeassistant", multiEntityRegistration())
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
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	original := validDomainRegistration()
	if _, err := service.Register(ctx, "homeassistant", original); err != nil {
		t.Fatal(err)
	}

	typeChange := validDomainRegistration()
	typeChange.Device.Name = "must roll back"
	typeChange.Entities[0].TypeID = "example.changed/v1"
	_, err := service.Register(ctx, "homeassistant", typeChange)
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
	service = NewService(NewSQLiteRepository(database, alternateCatalog), nil, alternateCatalog, Dependencies{})
	_, err = service.Register(ctx, "homeassistant", typeChange)
	assertRegistrationRejection(t, err, RegistrationImmutableTypeChange)
	assertCounts(t, database, 1, 1)
	var deviceName string
	if err := database.QueryRowContext(ctx, "SELECT name FROM devices").Scan(&deviceName); err != nil {
		t.Fatal(err)
	}
	if deviceName != original.Device.Name {
		t.Fatalf("failed type change partially updated device name to %q", deviceName)
	}

	conflict := validDomainRegistration()
	conflict.BindingKey = "other-light"
	otherExternalID := "other-device"
	conflict.Device.ExternalID = &otherExternalID
	_, err = service.Register(ctx, "homeassistant", conflict)
	assertRegistrationRejection(t, err, RegistrationIdentityConflict)
	assertCounts(t, database, 1, 1)

	invalid := validDomainRegistration()
	invalid.Entities[0].Support = EntitySupport(`{"state":{"unexpected":true},"operations":{"set":{}}}`)
	_, err = service.Register(ctx, "homeassistant", invalid)
	assertRegistrationRejection(t, err, RegistrationInvalidDescriptor)
	assertCounts(t, database, 1, 1)
}

func TestCommandLedgerTransitionsAreMonotonicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	binding, err := NewService(repository, nil, catalog, Dependencies{}).Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	command := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt)
	if err := repository.CreateCommand(ctx, command); err != nil {
		t.Fatal(err)
	}
	acceptedAt := requestedAt.Add(time.Second)
	if err := repository.MarkCommandAccepted(ctx, command.ID, acceptedAt); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCommandAccepted(ctx, command.ID, acceptedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
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
	if err := repository.CreateCommand(ctx, satisfied); err != nil {
		t.Fatal(err)
	}
	observationID, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
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
	storedSatisfied, err := repository.GetCommand(ctx, satisfied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSatisfied.Status != CommandStatusSatisfied || storedSatisfied.AcceptedAt == nil {
		t.Fatalf("acceptance regressed satisfied command: %#v", storedSatisfied)
	}

	requested := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt.Add(time.Minute))
	accepted := newCommandRecord(t, binding.Entities[0].EntityID, requestedAt.Add(2*time.Minute))
	if err := repository.CreateCommand(ctx, requested); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateCommand(ctx, accepted); err != nil {
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
	stillRequested, err := repository.GetCommand(ctx, requested.ID)
	if err != nil {
		t.Fatal(err)
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
		interrupted, err := repository.GetCommand(ctx, id)
		if err != nil {
			t.Fatal(err)
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
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
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
