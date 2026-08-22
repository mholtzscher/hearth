package devices

import (
	"context"
	"database/sql"
	"encoding/json"
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
	service := NewService(NewSQLiteRepository(database), catalog, Dependencies{Now: func() time.Time { return now }})
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
	var deviceName, entityName, storedDeviceExternalID, storedEntityExternalID string
	err = database.QueryRowContext(ctx, `
		SELECT d.name, e.name, b.external_device_id, m.external_entity_id
		FROM devices d
		JOIN adapter_bindings b ON b.device_id = d.id
		JOIN adapter_entity_mappings m
		  ON m.adapter_id = b.adapter_id AND m.binding_key = b.binding_key
		JOIN entities e ON e.id = m.entity_id
		WHERE b.adapter_id = ? AND b.binding_key = ?`, "homeassistant", registration.BindingKey,
	).Scan(&deviceName, &entityName, &storedDeviceExternalID, &storedEntityExternalID)
	if err != nil {
		t.Fatal(err)
	}
	if deviceName != "Renamed light" || entityName != "Renamed power" ||
		storedDeviceExternalID != deviceExternalID || storedEntityExternalID != "light.office-renamed" {
		t.Fatalf("persisted descriptors = %q %q %q %q", deviceName, entityName, storedDeviceExternalID, storedEntityExternalID)
	}
}

func TestConcurrentRegistrationReturnsOneBinding(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := NewService(NewSQLiteRepository(database), firstLightCatalog(t), Dependencies{})
	const attempts = 8
	results := make(chan Binding, attempts)
	errors := make(chan error, attempts)
	for range attempts {
		go func() {
			binding, err := service.Register(ctx, "homeassistant", validDomainRegistration())
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
		if binding.DeviceID != first.DeviceID || binding.Entities[0].EntityID != first.Entities[0].EntityID {
			t.Fatalf("concurrent registration changed IDs: first=%#v got=%#v", first, binding)
		}
	}
	assertCounts(t, database, 1, 1)
}

func TestRegistrationRejectionsAreAtomic(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := NewService(NewSQLiteRepository(database), firstLightCatalog(t), Dependencies{})
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
	alternateCatalog, err := NewTypeCatalog([]EntityTypeDefinition{
		firstLightDefinition(),
		{
			ID: "example.changed/v1", StateSchema: json.RawMessage(`{"type":"boolean"}`),
			ConstraintsSchema: json.RawMessage(`{"type":"object","maxProperties":0}`),
			OperationDefinitions: map[OperationName]OperationDefinition{OperationNameSet: {
				ParametersSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"boolean"}},"required":["value"],"additionalProperties":false}`),
				OutcomePolicy:    OutcomeParameterEqualsState, OutcomeParameter: "value", Deadline: 10 * time.Second,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service = NewService(NewSQLiteRepository(database), alternateCatalog, Dependencies{})
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
	invalid.Entities[0].Constraints = json.RawMessage(`{"unexpected":true}`)
	_, err = service.Register(ctx, "homeassistant", invalid)
	assertRegistrationRejection(t, err, RegistrationInvalidDescriptor)
	assertCounts(t, database, 1, 1)
}

func TestCommandLedgerTransitionsAreMonotonicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database)
	binding, err := NewService(repository, firstLightCatalog(t), Dependencies{}).Register(ctx, "simulator", validDomainRegistration())
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
	catalog, err := NewFirstLightTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func firstLightDefinition() EntityTypeDefinition {
	return EntityTypeDefinition{
		ID: EntityTypePowerV1, StateSchema: json.RawMessage(`{"type":"boolean"}`),
		ConstraintsSchema: json.RawMessage(`{"type":"object","maxProperties":0,"additionalProperties":false}`),
		OperationDefinitions: map[OperationName]OperationDefinition{OperationNameSet: {
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"boolean"}},"required":["value"],"additionalProperties":false}`),
			OutcomePolicy:    OutcomeParameterEqualsState, OutcomeParameter: "value", Deadline: 10 * time.Second,
		}},
	}
}

func validDomainRegistration() Registration {
	externalID := "ha-device"
	return Registration{
		BindingKey: "office-light",
		Device:     DeviceDescriptor{ExternalID: &externalID, Name: "Office light", Kind: DeviceKindLight},
		Entities: []EntityDescriptor{{
			Key: "power", ExternalID: "light.office", Name: "Power", TypeID: EntityTypePowerV1,
			Constraints: json.RawMessage(`{}`), SupportedOperations: []OperationName{OperationNameSet},
		}},
	}
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
