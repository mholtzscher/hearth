package devices //nolint:testpackage // Tests verify command identity through real migrated SQLite.

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExecuteCommandPersistsReservedIdentitiesBeforeDispatchAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	for _, outcome := range []OutcomeKind{OutcomeObserved, OutcomeDispatched} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			testReservedCommandIdentity(t, outcome)
		})
	}
}

func testReservedCommandIdentity(t *testing.T, outcome OutcomeKind) {
	t.Helper()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	operation, parameters := OperationNameSet, CommandParameters(`{"value":true}`)
	if outcome == OutcomeDispatched {
		registration.Entities[0].TypeID = EntityTypeEnumactionV1
		registration.Entities[0].Support = EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`)
		operation, parameters = "trigger", CommandParameters(`{"name":"blink"}`)
	}
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	input := CommandInput{ID: commandTestID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		EntityID: binding.Entities[0].EntityID, OperationName: operation, Parameters: parameters}
	sends := 0
	service.sender = commandSenderFunc(func(
		ctx context.Context, adapter string, runtime RuntimeID, request CommandRequest,
	) (CommandAcceptance, error) {
		sends++
		// A separate repository read must see the committed record, not a transaction-local candidate.
		stored, readErr := NewSQLiteRepository(database, catalog).GetCommand(ctx, input.ID)
		if readErr != nil {
			return CommandAcceptance{}, readErr
		}
		if stored.Status != CommandStatusRequested || stored.ID != input.ID ||
			stored.CorrelationID != input.CorrelationID ||
			request.ID != input.ID || request.CorrelationID != input.CorrelationID ||
			request.EntityID != input.EntityID ||
			adapter != "simulator" || runtime != testRuntimeID || !request.Deadline.Equal(stored.DeadlineAt) {
			return CommandAcceptance{}, errors.New("reserved identities or route not persisted before dispatch")
		}
		if outcome == OutcomeObserved {
			_, projectErr := service.ProjectObservation(ctx, adapter, runtime, Observation{
				ID: commandTestObservationID, EntityID: input.EntityID, Value: Value(`true`),
				CorrelationID: commandTestCorrelationID, AdapterReceivedAt: time.Now().UTC(),
				RefreshForCommand: &request.ID,
			}, time.Now().UTC())
			if projectErr != nil {
				return CommandAcceptance{}, projectErr
			}
		}
		return CommandAcceptance{Accepted: true}, nil
	})
	result, err := service.ExecuteCommand(ctx, input)
	if err != nil || result.CommandID != input.ID || result.Outcome != outcome || sends != 1 {
		t.Fatalf("execution = %#v, %v, sends = %d", result, err, sends)
	}
	assertDuplicateCommandIdentity(t, service, repository, input)
	assertTableCount(t, database, "commands", 1)
}

func assertDuplicateCommandIdentity(t *testing.T, service *Service, repository *SQLiteRepository, input CommandInput) {
	t.Helper()
	ctx := context.Background()
	sends := 0
	service.sender = commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		sends++
		return CommandAcceptance{}, nil
	})
	original, err := repository.GetCommand(ctx, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []CorrelationID{input.CorrelationID, "cor_01890f47-7a6b-7c4d-8e9f-0123456789ad"} {
		duplicate := input
		duplicate.CorrelationID = marker
		// Validation does not test freshness or adopt old commands; creation owns conflicts.
		if _, err = service.ValidateCommand(ctx, duplicate); err != nil {
			t.Fatal(err)
		}
		sends = 0
		result, executeErr := service.ExecuteCommand(ctx, duplicate)
		var executionError *CommandExecutionError
		if !errors.Is(executeErr, ErrCommandIDConflict) || errors.As(executeErr, &executionError) ||
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
	for _, status := range []CommandStatus{CommandStatusEntityDisabled, CommandStatusAdapterUnhealthy} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			testImmediateTerminalCommandIdentity(t, status)
		})
	}
}

func testImmediateTerminalCommandIdentity(t *testing.T, status CommandStatus) {
	t.Helper()
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
	wantErr := ErrEntityDisabled
	if status == CommandStatusEntityDisabled {
		if _, err = service.SetEntityEnabled(ctx, entityID, false); err != nil {
			t.Fatal(err)
		}
	} else {
		wantErr = ErrAdapterUnhealthy
		if err = repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
			AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	input := CommandInput{ID: commandTestID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ad",
		EntityID: entityID, OperationName: OperationNameSet, Parameters: CommandParameters(`{"value":true}`)}
	// Validation ignores temporary control eligibility and never generates IDs or reads the clock.
	service.dependencies = commandValidationDependencies()
	if _, err = service.ValidateCommand(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, database, "commands", 0)
	service.dependencies.Now = time.Now
	service.sender = commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		t.Error("immediately terminal command dispatched")
		return CommandAcceptance{}, nil
	})
	_, err = service.ExecuteCommand(ctx, input)
	var executionError *CommandExecutionError
	if !errors.Is(err, wantErr) || !errors.As(err, &executionError) || executionError.CommandID != input.ID {
		t.Fatalf("execution error = %v", err)
	}
	stored, err := repository.GetCommand(ctx, input.ID)
	if err != nil || stored.ID != input.ID ||
		stored.CorrelationID != input.CorrelationID || stored.Status != status ||
		stored.CompletedAt == nil || !stored.CompletedAt.Equal(stored.RequestedAt) || stored.RuntimeID != nil {
		t.Fatalf("terminal command = %#v, %v", stored, err)
	}
	if len(service.waiters.byID) != 0 {
		t.Fatal("terminal command registered a waiter")
	}
}

func TestCommandValidationMissingTargetAndChangedSupportCreateNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = EntityTypeEnumactionV1
	registration.Entities[0].Support = EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	input := CommandInput{ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID: commandTestEntityID, OperationName: "trigger", Parameters: CommandParameters(`{"name":"blink"}`)}
	service.dependencies = commandValidationDependencies()
	if _, err = service.ValidateCommand(ctx, input); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("validation error = %v", err)
	}
	if _, err = service.ExecuteCommand(ctx, input); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("execution error = %v", err)
	}
	input.EntityID = binding.Entities[0].EntityID
	if _, err = service.ValidateCommand(ctx, input); err != nil {
		t.Fatal(err)
	}
	// A previous validation cannot cache support for a future execution.
	registration.Entities[0].Support = EntitySupport(`{"state":{},"operations":{"trigger":{"values":["stop_effect"]}}}`)
	writer := newTestService(repository, nil, catalog, Dependencies{})
	if _, err = writer.Register(ctx, "simulator", testRuntimeID, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExecuteCommand(ctx, input); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("changed support error = %v", err)
	}
	assertTableCount(t, database, "commands", 0)
}

func TestCreateCommandDoesNotMapOtherConstraintsToIdentityConflict(t *testing.T) {
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
	candidate := newCommandRecord(t, binding.Entities[0].EntityID, time.Now().UTC())
	candidate.Parameters = CommandParameters(`[]`)
	if _, err = repository.CreateCommand(ctx, candidate); err == nil || errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("CHECK constraint error = %v", err)
	}
	candidate.Parameters = CommandParameters(`{"value":true}`)
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
	if _, err = repository.CreateCommand(ctx, candidate); err == nil || errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("non-ID UNIQUE constraint error = %v", err)
	}
	assertTableCount(t, database, "commands", 1)
}
