package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestExecuteCommandPersistsReservedIdentitiesBeforeDispatchAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	for _, outcome := range []devices.OutcomeKind{devices.OutcomeObserved, devices.OutcomeDispatched} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			testReservedCommandIdentity(t, outcome)
		})
	}
}

func testReservedCommandIdentity(t *testing.T, outcome devices.OutcomeKind) {
	t.Helper()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	registration := validDomainRegistration()
	operation, parameters := devices.OperationNameSet, devices.CommandParameters(`{"value":true}`)
	if outcome == devices.OutcomeDispatched {
		registration.Entities[0].TypeID = devices.EntityTypeEnumactionV1
		registration.Entities[0].Support = devices.EntitySupport(
			`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`,
		)
		operation, parameters = "trigger", devices.CommandParameters(`{"name":"blink"}`)
	}
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	input := devices.CommandInput{ID: commandTestID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		EntityID: binding.Entities[0].EntityID, OperationName: operation, Parameters: parameters}
	sends := 0
	sender := commandSenderFunc(func(
		ctx context.Context, adapter string, runtime devices.RuntimeID, request devices.CommandRequest,
	) (devices.CommandAcceptance, error) {
		sends++
		// A separate repository read must see the committed record, not a transaction-local candidate.
		stored, readErr := NewDeviceRepository(database, catalog).GetCommand(ctx, input.ID)
		if readErr != nil {
			return devices.CommandAcceptance{}, readErr
		}
		if stored.Status != devices.CommandStatusRequested || stored.ID != input.ID ||
			stored.CorrelationID != input.CorrelationID ||
			request.ID != input.ID || request.CorrelationID != input.CorrelationID ||
			request.EntityID != input.EntityID ||
			adapter != "simulator" || runtime != testRuntimeID || !request.Deadline.Equal(stored.DeadlineAt) {
			return devices.CommandAcceptance{}, errors.New("reserved identities or route not persisted before dispatch")
		}
		if outcome == devices.OutcomeObserved {
			_, projectErr := service.ProjectObservation(ctx, adapter, runtime, devices.Observation{
				ID: commandTestObservationID, EntityID: input.EntityID, Value: devices.Value(`true`),
				CorrelationID: commandTestCorrelationID, AdapterReceivedAt: time.Now().UTC(),
				RefreshForCommand: &request.ID,
			}, time.Now().UTC())
			if projectErr != nil {
				return devices.CommandAcceptance{}, projectErr
			}
		}
		return devices.CommandAcceptance{Accepted: true}, nil
	})
	// Dispatch needs the scripted sender, and the sender observes the same
	// Service it is handed to, so rebuild the Service with it after the
	// registration the sender's assertions depend on.
	service = newTestService(repository, sender, catalog, devices.Dependencies{})
	result, err := service.ExecuteCommand(ctx, input)
	if err != nil || result.CommandID != input.ID || result.Outcome != outcome || sends != 1 {
		t.Fatalf("execution = %#v, %v, sends = %d", result, err, sends)
	}
	assertDuplicateCommandIdentity(t, catalog, repository, input)
	assertTableCount(t, database, "commands", 1)
}

func assertDuplicateCommandIdentity(
	t *testing.T,
	catalog *devices.TypeCatalog,
	repository *DeviceRepository,
	input devices.CommandInput,
) {
	t.Helper()
	ctx := context.Background()
	sends := 0
	// A duplicate Command must never reach the sender, so the sender records
	// every call instead of returning an acceptance.
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, devices.RuntimeID, devices.CommandRequest,
	) (devices.CommandAcceptance, error) {
		sends++
		return devices.CommandAcceptance{}, nil
	}), catalog, devices.Dependencies{})
	original, err := repository.GetCommand(ctx, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []devices.CorrelationID{input.CorrelationID, "cor_01890f47-7a6b-7c4d-8e9f-0123456789ad"} {
		duplicate := input
		duplicate.CorrelationID = marker
		// Validation does not test freshness or adopt old commands; creation owns conflicts.
		if _, err = service.ValidateCommand(ctx, duplicate); err != nil {
			t.Fatal(err)
		}
		sends = 0
		result, executeErr := service.ExecuteCommand(ctx, duplicate)
		var executionError *devices.CommandExecutionError
		if !errors.Is(executeErr, devices.ErrCommandIDConflict) || errors.As(executeErr, &executionError) ||
			result.CommandID != "" || sends != 0 {
			t.Fatalf("duplicate = %#v, %v, sends = %d", result, executeErr, sends)
		}
		stored, readErr := repository.GetCommand(ctx, input.ID)
		if readErr != nil || !reflect.DeepEqual(stored, original) {
			t.Fatalf("duplicate changed original: %#v, %v", stored, readErr)
		}
	}
}

func TestExecuteCommandPreservesReservedIdentitiesForImmediateTerminalCommands(t *testing.T) {
	t.Parallel()
	for _, status := range []devices.CommandStatus{
		devices.CommandStatusEntityDisabled, devices.CommandStatusAdapterUnhealthy,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			testImmediateTerminalCommandIdentity(t, status)
		})
	}
}

func testImmediateTerminalCommandIdentity(t *testing.T, status devices.CommandStatus) {
	t.Helper()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	wantErr := devices.ErrEntityDisabled
	if status == devices.CommandStatusEntityDisabled {
		if _, err = service.SetEntityEnabled(ctx, entityID, false); err != nil {
			t.Fatal(err)
		}
	} else {
		wantErr = devices.ErrAdapterUnhealthy
		if err = repository.ReleaseAdapterRuntime(ctx, devices.ReleaseRuntimeWrite{
			AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	input := devices.CommandInput{ID: commandTestID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ad",
		EntityID: entityID, OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":true}`)}
	// Validation ignores temporary control eligibility and never generates IDs or reads the clock.
	validationService := newTestService(repository, nil, catalog, commandValidationDependencies())
	if _, err = validationService.ValidateCommand(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "commands", 0)
	// A terminal Command must not dispatch, and it must complete on the same
	// call instead of leaving a waiter behind. The waiter map is private to the
	// devices package, so this boundary only observes that execution returns the
	// terminal failure without ever reaching the sender.
	executionService := newTestService(repository, commandSenderFunc(func(
		context.Context, string, devices.RuntimeID, devices.CommandRequest,
	) (devices.CommandAcceptance, error) {
		t.Error("immediately terminal command dispatched")
		return devices.CommandAcceptance{}, nil
	}), catalog, devices.Dependencies{})
	_, err = executionService.ExecuteCommand(ctx, input)
	var executionError *devices.CommandExecutionError
	if !errors.Is(err, wantErr) || !errors.As(err, &executionError) || executionError.CommandID != input.ID {
		t.Fatalf("execution error = %v", err)
	}
	stored, err := repository.GetCommand(ctx, input.ID)
	if err != nil || stored.ID != input.ID ||
		stored.CorrelationID != input.CorrelationID || stored.Status != status ||
		stored.CompletedAt == nil || !stored.CompletedAt.Equal(stored.RequestedAt) || stored.RuntimeID != nil {
		t.Fatalf("terminal command = %#v, %v", stored, err)
	}
}

func TestCommandValidationMissingTargetAndChangedSupportCreateNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = devices.EntityTypeEnumactionV1
	registration.Entities[0].Support = devices.EntitySupport(
		`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`,
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	input := devices.CommandInput{ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID: commandTestEntityID, OperationName: "trigger",
		Parameters: devices.CommandParameters(`{"name":"blink"}`)}
	validationService := newTestService(repository, nil, catalog, commandValidationDependencies())
	if _, err = validationService.ValidateCommand(ctx, input); !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("validation error = %v", err)
	}
	if _, err = validationService.ExecuteCommand(ctx, input); !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("execution error = %v", err)
	}
	input.EntityID = binding.Entities[0].EntityID
	if _, err = validationService.ValidateCommand(ctx, input); err != nil {
		t.Fatal(err)
	}
	// A previous validation cannot cache support for a future execution.
	registration.Entities[0].Support = devices.EntitySupport(
		`{"state":{},"operations":{"trigger":{"values":["stop_effect"]}}}`,
	)
	writer := newTestService(repository, nil, catalog, devices.Dependencies{})
	if _, err = writer.Register(ctx, "simulator", testRuntimeID, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = validationService.ExecuteCommand(ctx, input); !errors.Is(err, devices.ErrInvalidCommand) {
		t.Fatalf("changed support error = %v", err)
	}
	assertTableCount(t, database, "commands", 0)
}

func TestCreateCommandDoesNotMapOtherConstraintsToIdentityConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	candidate := newCommandRecord(t, binding.Entities[0].EntityID, time.Now().UTC())
	candidate.Parameters = devices.CommandParameters(`[]`)
	if _, err = repository.CreateCommand(ctx, candidate); err == nil || errors.Is(err, devices.ErrCommandIDConflict) {
		t.Fatalf("CHECK constraint error = %v", err)
	}
	candidate.Parameters = devices.CommandParameters(`{"value":true}`)
	if _, err = repository.CreateCommand(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	// A non-ID uniqueness violation must retain its database meaning, even if future
	// migrations add unique constraints to command creation fields.
	if _, err = database.ExecContext(
		ctx, `CREATE UNIQUE INDEX test_command_correlation ON commands(correlation_id)`,
	); err != nil {
		t.Fatal(err)
	}
	candidate.ID = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	if _, err = repository.CreateCommand(ctx, candidate); err == nil || errors.Is(err, devices.ErrCommandIDConflict) {
		t.Fatalf("non-ID UNIQUE constraint error = %v", err)
	}
	assertTableCount(t, database, "commands", 1)
}
