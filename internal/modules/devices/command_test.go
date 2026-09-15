package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
)

const (
	commandTestEntityID      = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestDeviceID      = DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestID            = CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestCorrelationID = CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestObservationID = ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestRuntimeID     = RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type commandRepository struct {
	mutex          sync.Mutex
	view           EntityWithState
	commands       map[CommandID]CommandRecord
	createErr      error
	completeErr    error
	forceUnhealthy bool
}

func newCommandRepository() *commandRepository {
	return &commandRepository{
		view: EntityWithState{Entity: Entity{
			ID: commandTestEntityID, DeviceID: commandTestDeviceID, AdapterID: "simulator", Name: "Power",
			TypeID: EntityTypePowerV1, Support: EntitySupport(`{"state":{},"operations":{"set":{}}}`), Enabled: true,
		}},
		commands: make(map[CommandID]CommandRecord),
	}
}

func (*commandRepository) ListDevices(context.Context, ListDevicesParams) (Page[Device], error) {
	panic("unexpected ListDevices call")
}

func (*commandRepository) GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error) {
	panic("unexpected GetDevice call")
}

func (*commandRepository) ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error) {
	panic("unexpected ListEntities call")
}

func (repository *commandRepository) GetEntity(context.Context, EntityID) (EntityWithState, error) {
	return repository.view, nil
}

func (*commandRepository) GetCommand(context.Context, CommandID) (CommandRecord, error) {
	panic("unexpected GetCommand call")
}

func (*commandRepository) ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error) {
	panic("unexpected ListEntityCommands call")
}

func (*commandRepository) ListEntityStateHistory(
	context.Context,
	ListEntityStateHistoryParams,
) (Page[EntityStateHistoryEntry], error) {
	panic("unexpected ListEntityStateHistory call")
}

func (repository *commandRepository) CreateCommand(_ context.Context, command CommandRecord) (CommandRecord, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.createErr != nil {
		return CommandRecord{}, repository.createErr
	}
	if _, exists := repository.commands[command.ID]; exists {
		return CommandRecord{}, ErrCommandIDConflict
	}
	switch {
	case !repository.view.Entity.Enabled:
		completedAt := command.RequestedAt
		failureCode := CommandFailureEntityDisabled
		command.Status = CommandStatusEntityDisabled
		command.CompletedAt = &completedAt
		command.FailureCode = &failureCode
	case repository.forceUnhealthy:
		completedAt := command.RequestedAt
		failureCode := CommandFailureAdapterUnhealthy
		command.Status = CommandStatusAdapterUnhealthy
		command.CompletedAt = &completedAt
		command.FailureCode = &failureCode
	default:
		runtimeID := commandTestRuntimeID
		command.RuntimeID = &runtimeID
	}
	repository.commands[command.ID] = command
	return CopyCommandRecord(command), nil
}

func (repository *commandRepository) MarkCommandAccepted(_ context.Context, id CommandID, acceptedAt time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	command := repository.commands[id]
	if command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted &&
		command.Status != CommandStatusSatisfied {
		return ErrCommandTerminal
	}
	if command.AcceptedAt == nil {
		value := acceptedAt
		command.AcceptedAt = &value
	}
	if command.Status == CommandStatusRequested {
		command.Status = CommandStatusAccepted
	}
	repository.commands[id] = command
	return nil
}

func (repository *commandRepository) CompleteCommand(_ context.Context, completion CommandCompletion) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.completeErr != nil {
		return repository.completeErr
	}
	command := repository.commands[completion.ID]
	if command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted {
		return ErrCommandTerminal
	}
	completedAt := completion.CompletedAt
	failureCode := completion.FailureCode
	command.Status = completion.Status
	command.CompletedAt = &completedAt
	command.FailureCode = &failureCode
	repository.commands[completion.ID] = command
	return nil
}

func (*commandRepository) InterruptActiveCommands(context.Context, time.Time) error {
	panic("unexpected InterruptActiveCommands call")
}

func (repository *commandRepository) ProjectObservation(
	_ context.Context,
	params ProjectObservationParams,
) (ProjectionResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	result := ProjectionResult{Disposition: DispositionUnchanged}
	if params.Observation.RefreshForCommand == nil {
		return result, nil
	}
	command := repository.commands[*params.Observation.RefreshForCommand]
	if command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted {
		return result, nil
	}
	matches := (string(command.Parameters) == `{"value":true}` && string(params.Observation.Value) == "true") ||
		(string(command.Parameters) == `{"value":false}` && string(params.Observation.Value) == "false")
	if !matches {
		return result, nil
	}
	completedAt := params.Now().UTC()
	observationID := params.Observation.ID
	command.Status = CommandStatusSatisfied
	command.CompletedAt = &completedAt
	command.OutcomeObservationID = &observationID
	repository.commands[command.ID] = command
	clonedValue := append(Value(nil), params.Observation.Value...)
	observed, err := NewCommandResult(command.ID, OutcomeObserved, &observationID, &clonedValue)
	if err != nil {
		return ProjectionResult{}, err
	}
	result.SatisfiedCommand = &observed
	return result, nil
}

func (*commandRepository) DeleteExpiredObservations(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservations call")
}

func (repository *commandRepository) command(id CommandID) CommandRecord {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.commands[id]
}

type commandSenderFunc func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error)

func (send commandSenderFunc) Send(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	request CommandRequest,
) (CommandAcceptance, error) {
	return send(ctx, adapterID, runtimeID, request)
}

func TestExecuteCommandCommitsBeforeDispatchAndHandlesAcceptanceRace(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	catalog := commandCatalog(t, time.Second)
	var service *Service
	sender := commandSenderFunc(
		func(ctx context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			if adapterID != "simulator" || runtimeID != commandTestRuntimeID ||
				repository.command(request.ID).Status != CommandStatusRequested {
				return CommandAcceptance{}, errors.New("command was dispatched before requested was committed")
			}
			observation := Observation{
				ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
				CorrelationID:     commandTestCorrelationID,
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
			}
			if _, err := service.ProjectObservation(
				ctx, adapterID, runtimeID, observation, time.Now().UTC(),
			); err != nil {
				return CommandAcceptance{}, err
			}
			return CommandAcceptance{Accepted: true}, nil
		},
	)
	service = newTestService(repository, sender, catalog, commandDependencies())

	result, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireObservedResult(t, result)
	stored := repository.command(commandTestID)
	if stored.Status != CommandStatusSatisfied || stored.AcceptedAt == nil || stored.OutcomeObservationID == nil ||
		*stored.OutcomeObservationID != commandTestObservationID {
		t.Fatalf("stored command = %#v", stored)
	}
}

func TestExecuteCommandReturnsSatisfiedWhenObservationWinsDispatchFailureRace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		acceptance CommandAcceptance
		sendErr    error
	}{
		{"adapter unhealthy", CommandAcceptance{}, ErrAdapterUnhealthy},
		{"entity unavailable", CommandAcceptance{}, ErrEntityUnavailable},
		{"upstream rejected", CommandAcceptance{Accepted: false}, nil},
		{"invalid response", CommandAcceptance{}, errors.New("invalid response")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newCommandRepository()
			var service *Service
			sender := commandSenderFunc(
				func(
					ctx context.Context,
					adapterID string,
					runtimeID RuntimeID,
					request CommandRequest,
				) (CommandAcceptance, error) {
					_, err := service.ProjectObservation(ctx, adapterID, runtimeID, Observation{
						ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
						CorrelationID:     commandTestCorrelationID,
						AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
					}, time.Now().UTC())
					if err != nil {
						return CommandAcceptance{}, err
					}
					return test.acceptance, test.sendErr
				},
			)
			service = newTestService(repository, sender, commandCatalog(t, time.Second), commandDependencies())

			result, err := service.ExecuteCommand(context.Background(), CommandInput{
				EntityID:      commandTestEntityID,
				OperationName: OperationNameSet,
				Parameters:    CommandParameters(`{"value":true}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			requireObservedResult(t, result)
			stored := repository.command(commandTestID)
			if stored.Status != CommandStatusSatisfied || stored.OutcomeObservationID == nil ||
				*stored.OutcomeObservationID != commandTestObservationID || stored.FailureCode != nil {
				t.Fatalf("stored command = %#v", stored)
			}
		})
	}
}

func TestExecuteCommandFailureMatrixIsDurablyClassified(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		acceptance CommandAcceptance
		sendErr    error
		deadline   time.Duration
		wantErr    error
		wantStatus CommandStatus
		wantCode   CommandFailureCode
	}{
		{
			"adapter unhealthy",
			CommandAcceptance{},
			ErrAdapterUnhealthy,
			time.Second,
			ErrAdapterUnhealthy,
			CommandStatusAdapterUnhealthy,
			CommandFailureAdapterUnhealthy,
		},
		{
			"entity unavailable",
			CommandAcceptance{},
			ErrEntityUnavailable,
			time.Second,
			ErrEntityUnavailable,
			CommandStatusEntityUnavailable,
			CommandFailureEntityUnavailable,
		},
		{
			"upstream rejected",
			CommandAcceptance{Accepted: false},
			nil,
			time.Second,
			ErrUpstreamRejected,
			CommandStatusRejected,
			CommandFailureUpstreamRejected,
		},
		{
			"internal send failure",
			CommandAcceptance{},
			errors.New("invalid response"),
			time.Second,
			nil,
			CommandStatusInternalFailure,
			CommandFailureInternalError,
		},
		{
			"outcome timeout",
			CommandAcceptance{Accepted: true},
			nil,
			15 * time.Millisecond,
			ErrOutcomeTimeout,
			CommandStatusOutcomeTimeout,
			CommandFailureOutcomeTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newCommandRepository()
			sender := commandSenderFunc(func(
				context.Context,
				string,
				RuntimeID,
				CommandRequest,
			) (CommandAcceptance, error) {
				return test.acceptance, test.sendErr
			})
			service := newTestService(repository, sender, commandCatalog(t, test.deadline), commandDependencies())
			_, err := service.ExecuteCommand(context.Background(), CommandInput{
				EntityID:      commandTestEntityID,
				OperationName: OperationNameSet,
				Parameters:    CommandParameters(`{"value":true}`),
			})
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil &&
				(err == nil || errors.Is(err, ErrAdapterUnhealthy) || errors.Is(err, ErrEntityUnavailable) ||
					errors.Is(err, ErrUpstreamRejected) || errors.Is(err, ErrOutcomeTimeout)) {
				t.Fatalf("error = %v, want internal failure", err)
			}
			var executionError *CommandExecutionError
			if !errors.As(err, &executionError) || executionError.CommandID != commandTestID {
				t.Fatalf("execution error = %#v", executionError)
			}
			stored := repository.command(commandTestID)
			if stored.Status != test.wantStatus || stored.FailureCode == nil || *stored.FailureCode != test.wantCode ||
				stored.CompletedAt == nil {
				t.Fatalf("stored command = %#v", stored)
			}
		})
	}
}

func TestExecuteCommandRejectsInvalidParametersAndCreationFailureBeforeDispatch(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	dispatches := 0
	sender := commandSenderFunc(func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
		dispatches++
		return CommandAcceptance{Accepted: true}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandDependencies())

	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":1}`),
	}); !errors.Is(
		err,
		ErrInvalidCommand,
	) {
		t.Fatalf("invalid parameters error = %v", err)
	}
	repository.createErr = errors.New("SQLite unavailable")
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(
		err,
		repository.createErr,
	) {
		t.Fatalf("creation error = %v", err)
	}
	if dispatches != 0 {
		t.Fatalf("dispatches = %d", dispatches)
	}
}

func TestExecuteCommandRejectsTemperatureOperationBeforeDispatch(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	repository.view.Entity = Entity{
		ID: commandTestEntityID, DeviceID: commandTestDeviceID, AdapterID: "simulator", Name: "Temperature",
		TypeID:  EntityTypeTemperatureV1,
		Support: EntitySupport(`{"state":{"unit":"mCel"},"operations":{}}`), Enabled: true,
	}
	dispatches := 0
	sender := commandSenderFunc(func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
		dispatches++
		return CommandAcceptance{Accepted: true}, nil
	})
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(repository, sender, catalog, commandDependencies())

	if _, execErr := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":21500}`),
	}); !errors.Is(execErr, ErrInvalidCommand) {
		t.Fatalf("temperature command error = %v", execErr)
	}
	if dispatches != 0 {
		t.Fatalf("dispatches = %d, want 0", dispatches)
	}
	if len(repository.commands) != 0 {
		t.Fatalf("commands = %#v, want none persisted", repository.commands)
	}
}

func TestExecuteCommandCreatesTerminalRecordWithoutWaiterOrDispatchWhenDisabled(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	repository.view.Entity.Enabled = false
	dispatches := 0
	sender := commandSenderFunc(func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
		dispatches++
		return CommandAcceptance{}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandDependencies())

	_, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	})
	if !errors.Is(err, ErrEntityDisabled) {
		t.Fatalf("disabled Command error = %v", err)
	}
	var executionError *CommandExecutionError
	if !errors.As(err, &executionError) || executionError.CommandID != commandTestID {
		t.Fatalf("execution error = %#v", executionError)
	}
	stored := repository.command(commandTestID)
	if stored.Status != CommandStatusEntityDisabled || stored.CompletedAt == nil ||
		stored.FailureCode == nil || *stored.FailureCode != CommandFailureEntityDisabled {
		t.Fatalf("stored Command = %#v", stored)
	}
	if dispatches != 0 {
		t.Fatalf("dispatches = %d", dispatches)
	}
	service.waiters.mutex.Lock()
	waiters := len(service.waiters.byID)
	service.waiters.mutex.Unlock()
	if waiters != 0 {
		t.Fatalf("waiters = %d", waiters)
	}

	repository.commands = make(map[CommandID]CommandRecord)
	if _, commandErr := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":1}`),
	}); !errors.Is(commandErr, ErrInvalidCommand) {
		t.Fatalf("invalid disabled Command error = %v", commandErr)
	}
	if len(repository.commands) != 0 {
		t.Fatalf("invalid disabled Command created records: %#v", repository.commands)
	}
}

func TestExecuteCommandKeepsOverlappingCommandsIndependent(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	catalog := commandCatalog(t, time.Second)
	commandIDs := []CommandID{
		"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"cmd_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	observationIDs := map[CommandID]ObservationID{ //nolint:exhaustive // Only commands linked by this test need observations.
		commandIDs[0]: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		commandIDs[1]: "obs_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	correlationIDs := []CorrelationID{
		"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"cor_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	var idMutex sync.Mutex
	nextID := 0
	nextCorrelationID := 0
	dependencies := Dependencies{
		Now: func() time.Time { return time.Now().UTC() },
		NewCommandID: func() (CommandID, error) {
			idMutex.Lock()
			defer idMutex.Unlock()
			id := commandIDs[nextID]
			nextID++
			return id, nil
		},
		NewCorrelationID: func() (CorrelationID, error) {
			idMutex.Lock()
			defer idMutex.Unlock()
			id := correlationIDs[nextCorrelationID]
			nextCorrelationID++
			return id, nil
		},
	}
	var service *Service
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	sender := commandSenderFunc(
		func(ctx context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			arrived <- struct{}{}
			<-release
			value := Value(`false`)
			if string(request.Parameters) == `{"value":true}` {
				value = Value(`true`)
			}
			observationID := observationIDs[request.ID]
			_, err := service.ProjectObservation(ctx, adapterID, runtimeID, Observation{
				ID: observationID, EntityID: request.EntityID, Value: value,
				CorrelationID:     commandTestCorrelationID,
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
			}, time.Now().UTC())
			return CommandAcceptance{Accepted: true}, err
		},
	)
	service = newTestService(repository, sender, catalog, dependencies)

	type result struct {
		command CommandResult
		err     error
	}
	results := make(chan result, 2)
	for _, parameters := range []CommandParameters{CommandParameters(`{"value":true}`), CommandParameters(`{"value":false}`)} {
		go func() {
			command, err := service.ExecuteCommand(context.Background(), CommandInput{
				EntityID:      commandTestEntityID,
				OperationName: OperationNameSet,
				Parameters:    parameters,
			})
			results <- result{command: command, err: err}
		}()
	}
	<-arrived
	<-arrived
	close(release)
	seen := make(map[CommandID]bool)
	seenCorrelations := make(map[CorrelationID]bool)
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		seen[outcome.command.CommandID] = true
		seenCorrelations[repository.command(outcome.command.CommandID).CorrelationID] = true
	}
	if len(seenCorrelations) != 2 {
		t.Fatalf("correlation IDs = %#v", seenCorrelations)
	}
	for _, id := range commandIDs {
		stored := repository.command(id)
		if !seen[id] || stored.Status != CommandStatusSatisfied || stored.OutcomeObservationID == nil ||
			*stored.OutcomeObservationID != observationIDs[id] {
			t.Fatalf("command %s = %#v, seen = %#v", id, stored, seen)
		}
	}
}

func TestExecuteCommandIgnoresMismatchedLinkedObservation(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	var service *Service
	sender := commandSenderFunc(
		func(ctx context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			_, err := service.ProjectObservation(ctx, adapterID, runtimeID, Observation{
				ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`false`),
				CorrelationID:     commandTestCorrelationID,
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
			}, time.Now().UTC())
			return CommandAcceptance{Accepted: true}, err
		},
	)
	service = newTestService(repository, sender, commandCatalog(t, 15*time.Millisecond), commandDependencies())
	_, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	})
	if !errors.Is(err, ErrOutcomeTimeout) {
		t.Fatalf("error = %v", err)
	}
	if stored := repository.command(
		commandTestID,
	); stored.Status != CommandStatusOutcomeTimeout ||
		stored.OutcomeObservationID != nil {
		t.Fatalf("stored command = %#v", stored)
	}
}

func TestExecuteCommandContinuesAfterCallerCancellation(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	dispatched := make(chan CommandRequest, 1)
	release := make(chan struct{})
	sender := commandSenderFunc(func(
		_ context.Context,
		_ string,
		_ RuntimeID,
		request CommandRequest,
	) (CommandAcceptance, error) {
		dispatched <- request
		<-release
		return CommandAcceptance{Accepted: false}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandDependencies())
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(ctx, CommandInput{
			EntityID:      commandTestEntityID,
			OperationName: OperationNameSet,
			Parameters:    CommandParameters(`{"value":true}`),
		})
		returned <- err
	}()
	request := <-dispatched
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if repository.command(request.ID).Status == CommandStatusRejected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("command did not finish after cancellation: %#v", repository.command(request.ID))
}

func requireObservedResult(t *testing.T, result CommandResult) {
	t.Helper()
	if result.Outcome != OutcomeObserved || result.CommandID != commandTestID || result.ObservationID == nil ||
		*result.ObservationID != commandTestObservationID || result.Value == nil || string(*result.Value) != "true" {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecuteCommandDispatchedCompletesAfterAcceptanceWithoutObservation(t *testing.T) {
	t.Parallel()
	repository := newDispatchedCommandRepository()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sender := commandSenderFunc(
		func(_ context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			if adapterID != "simulator" || runtimeID != commandTestRuntimeID ||
				repository.command(request.ID).Status != CommandStatusRequested {
				return CommandAcceptance{}, errors.New("dispatched command was not durably requested before dispatch")
			}
			return CommandAcceptance{Accepted: true}, nil
		},
	)
	service := newTestService(repository, sender, catalog, commandDependencies())

	result, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationName("trigger"),
		Parameters:    CommandParameters(`{"name":"blink"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandID != commandTestID || result.Outcome != OutcomeDispatched ||
		result.ObservationID != nil || result.Value != nil {
		t.Fatalf("result = %#v", result)
	}
	stored := repository.command(commandTestID)
	if stored.Status != CommandStatusDispatched || stored.AcceptedAt == nil || stored.CompletedAt == nil ||
		stored.OutcomeObservationID != nil {
		t.Fatalf("stored command = %#v", stored)
	}
}

type dispatchedFailureCase struct {
	name         string
	parameters   CommandParameters
	acceptance   CommandAcceptance
	wantErr      error
	wantStatus   CommandStatus
	wantDispatch bool
}

func TestExecuteCommandDispatchedFailsBeforeCommitWithoutAcceptance(t *testing.T) {
	t.Parallel()
	tests := []dispatchedFailureCase{
		{
			"upstream rejected", CommandParameters(`{"name":"blink"}`),
			CommandAcceptance{Accepted: false}, ErrUpstreamRejected, CommandStatusRejected, true,
		},
		{
			"unsupported effect name rejected pre-dispatch", CommandParameters(`{"name":"party"}`),
			CommandAcceptance{Accepted: true}, ErrInvalidCommand, "", false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runDispatchedFailureCase(t, test)
		})
	}
}

func runDispatchedFailureCase(t *testing.T, test dispatchedFailureCase) {
	t.Helper()
	repository := newDispatchedCommandRepository()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	dispatches := 0
	sender := commandSenderFunc(func(
		context.Context,
		string,
		RuntimeID,
		CommandRequest,
	) (CommandAcceptance, error) {
		dispatches++
		return test.acceptance, nil
	})
	service := newTestService(repository, sender, catalog, commandDependencies())
	_, err = service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationName("trigger"),
		Parameters:    test.parameters,
	})
	if !errors.Is(err, test.wantErr) {
		t.Fatalf("error = %v, want %v", err, test.wantErr)
	}
	if !test.wantDispatch {
		if dispatches != 0 || len(repository.commands) != 0 {
			t.Fatalf("pre-dispatch rejection dispatched %d and persisted %#v", dispatches, repository.commands)
		}
		return
	}
	if dispatches != 1 {
		t.Fatalf("dispatches = %d, want 1", dispatches)
	}
	stored := repository.command(commandTestID)
	if stored.Status != test.wantStatus || stored.OutcomeObservationID != nil {
		t.Fatalf("stored command = %#v", stored)
	}
}

func newDispatchedCommandRepository() *commandRepository {
	repository := newCommandRepository()
	repository.view.Entity = Entity{
		ID: commandTestEntityID, DeviceID: commandTestDeviceID, AdapterID: "simulator", Name: "Effect",
		TypeID: EntityTypeEnumactionV1, Support: EntitySupport(
			`{"state":{},"operations":{"trigger":{"values":["blink","stop_effect"]}}}`),
		Enabled: true,
	}
	return repository
}

func commandDependencies() Dependencies {
	return Dependencies{
		Now:              func() time.Time { return time.Now().UTC() },
		NewCommandID:     func() (CommandID, error) { return commandTestID, nil },
		NewCorrelationID: func() (CorrelationID, error) { return commandTestCorrelationID, nil },
	}
}

func commandCatalog(t *testing.T, deadline time.Duration) *TypeCatalog {
	t.Helper()
	codecs, err := contractpowerv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := DefineEntityType(
		EntityTypePowerV1, codecs.State, codecs.Support,
		contractpowerv1.ValidateSupport, contractpowerv1.ValidateState, contractpowerv1.EqualState,
		DefineOperation(
			OperationNameSet, codecs.SetParameters,
			func(support contractpowerv1.Support) (contractpowerv1.SetSupport, bool) {
				return support.Operations.Set, true
			},
			contractpowerv1.ValidateSetParameters, deadline, OutcomeObserved, contractpowerv1.SetSatisfied,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
