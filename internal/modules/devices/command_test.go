package devices

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	commandTestID            = CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestCorrelationID = CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestObservationID = ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

func TestExecuteCommandCommitsBeforeDeliveryAndHandlesAcceptanceRace(t *testing.T) {
	controls := commandTestControls()
	service, database := newDeviceTestService(t, controls)
	var deliveryErr error
	delivery := commandDeliveryFunc(func(ctx context.Context, adapterID string, dispatch CommandDispatch) (CommandAcceptance, error) {
		command, err := getCommand(ctx, database, dispatch.ID)
		if err != nil || command.status != commandStatusRequested {
			return CommandAcceptance{}, errors.New("Command was delivered before requested was committed")
		}
		_, deliveryErr = service.ReceiveObservation(ctx, ReceivedObservation{
			AdapterID: adapterID,
			Observation: Observation{
				ID: commandTestObservationID, EntityID: dispatch.EntityID, Value: Value(`true`),
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &dispatch.ID,
			},
			ObservedAt: time.Now().UTC(),
		})
		return CommandAcceptance{Accepted: true}, deliveryErr
	})
	runDeviceTestService(t, service, delivery)
	entityID := registerCommandEntity(t, service)

	result, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, CommandParameters(`{"value":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if deliveryErr != nil {
		t.Fatal(deliveryErr)
	}
	if result.CommandID != commandTestID || result.ObservationID != commandTestObservationID || string(result.Value) != "true" {
		t.Fatalf("result = %#v", result)
	}
	stored, err := getCommand(context.Background(), database, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusSatisfied || stored.acceptedAt == nil || stored.outcomeObservationID == nil ||
		*stored.outcomeObservationID != commandTestObservationID {
		t.Fatalf("stored Command = %#v", stored)
	}
}

func TestExecuteCommandReturnsSatisfiedWhenObservationWinsDeliveryFailureRace(t *testing.T) {
	tests := []struct {
		name       string
		acceptance CommandAcceptance
		deliverErr error
	}{
		{"adapter unavailable", CommandAcceptance{}, ErrAdapterUnavailable},
		{"upstream rejected", CommandAcceptance{Accepted: false}, nil},
		{"invalid response", CommandAcceptance{}, errors.New("invalid response")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, database := newDeviceTestService(t, commandTestControls())
			delivery := commandDeliveryFunc(func(ctx context.Context, adapterID string, dispatch CommandDispatch) (CommandAcceptance, error) {
				_, err := service.ReceiveObservation(ctx, ReceivedObservation{
					AdapterID: adapterID,
					Observation: Observation{
						ID: commandTestObservationID, EntityID: dispatch.EntityID, Value: Value(`true`),
						AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &dispatch.ID,
					},
					ObservedAt: time.Now().UTC(),
				})
				if err != nil {
					return CommandAcceptance{}, err
				}
				return test.acceptance, test.deliverErr
			})
			runDeviceTestService(t, service, delivery)
			entityID := registerCommandEntity(t, service)

			result, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, CommandParameters(`{"value":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.CommandID != commandTestID || result.ObservationID != commandTestObservationID {
				t.Fatalf("result = %#v", result)
			}
			stored, err := getCommand(context.Background(), database, commandTestID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.status != commandStatusSatisfied || stored.failureCode != nil {
				t.Fatalf("stored Command = %#v", stored)
			}
		})
	}
}

func TestExecuteCommandFailureMatrixIsDurablyClassified(t *testing.T) {
	tests := []struct {
		name       string
		acceptance CommandAcceptance
		deliverErr error
		timeout    bool
		wantErr    error
		wantStatus commandStatus
		wantCode   commandFailureCode
	}{
		{"adapter unavailable", CommandAcceptance{}, ErrAdapterUnavailable, false, ErrAdapterUnavailable, commandStatusAdapterUnavailable, commandFailureAdapterUnavailable},
		{"upstream rejected", CommandAcceptance{Accepted: false}, nil, false, ErrUpstreamRejected, commandStatusRejected, commandFailureUpstreamRejected},
		{"internal delivery failure", CommandAcceptance{}, errors.New("invalid response"), false, nil, commandStatusInternalFailure, commandFailureInternalError},
		{"outcome timeout", CommandAcceptance{Accepted: true}, nil, true, ErrOutcomeTimeout, commandStatusOutcomeTimeout, commandFailureOutcomeTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controls := commandTestControls()
			if test.timeout {
				controls.withDeadline = func(parent context.Context, _ time.Time) (context.Context, context.CancelFunc) {
					ctx, cancel := context.WithCancel(parent)
					cancel()
					return ctx, func() {}
				}
			}
			service, database := newRunningDeviceTestService(t, controls, commandDeliveryFunc(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
				return test.acceptance, test.deliverErr
			}))
			entityID := registerCommandEntity(t, service)
			_, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, CommandParameters(`{"value":true}`))
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && err == nil {
				t.Fatal("internal failure returned nil")
			}
			var executionError *CommandExecutionError
			if !errors.As(err, &executionError) || executionError.CommandID != commandTestID {
				t.Fatalf("execution error = %#v", executionError)
			}
			stored, getErr := getCommand(context.Background(), database, commandTestID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if stored.status != test.wantStatus || stored.failureCode == nil || *stored.failureCode != test.wantCode || stored.completedAt == nil {
				t.Fatalf("stored Command = %#v", stored)
			}
		})
	}
}

func TestExecuteCommandRejectsInvalidParametersBeforeDelivery(t *testing.T) {
	var deliveries int
	service, database := newRunningDeviceTestService(t, commandTestControls(), commandDeliveryFunc(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		deliveries++
		return CommandAcceptance{Accepted: true}, nil
	}))
	entityID := registerCommandEntity(t, service)
	if _, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, CommandParameters(`{"value":1}`)); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("invalid parameters error = %v", err)
	}
	if deliveries != 0 {
		t.Fatalf("deliveries = %d", deliveries)
	}
	var commands int
	if err := database.QueryRow("SELECT count(*) FROM commands").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if commands != 0 {
		t.Fatalf("persisted Commands = %d", commands)
	}
}

func TestExecuteCommandKeepsOverlappingCommandsIndependent(t *testing.T) {
	commandIDs := []CommandID{
		"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"cmd_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	correlationIDs := []CorrelationID{
		"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"cor_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	observationIDs := map[CommandID]ObservationID{
		commandIDs[0]: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		commandIDs[1]: "obs_01890f47-7a6c-7c4d-8e9f-0123456789ab",
	}
	controls := productionServiceControls()
	var idMutex sync.Mutex
	nextCommand, nextCorrelation := 0, 0
	controls.newCommandID = func() (CommandID, error) {
		idMutex.Lock()
		defer idMutex.Unlock()
		id := commandIDs[nextCommand]
		nextCommand++
		return id, nil
	}
	controls.newCorrelationID = func() (CorrelationID, error) {
		idMutex.Lock()
		defer idMutex.Unlock()
		id := correlationIDs[nextCorrelation]
		nextCorrelation++
		return id, nil
	}
	service, database := newDeviceTestService(t, controls)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	delivery := commandDeliveryFunc(func(ctx context.Context, adapterID string, dispatch CommandDispatch) (CommandAcceptance, error) {
		arrived <- struct{}{}
		<-release
		value := Value(`false`)
		if string(dispatch.Parameters) == `{"value":true}` {
			value = Value(`true`)
		}
		observationID := observationIDs[dispatch.ID]
		_, err := service.ReceiveObservation(ctx, ReceivedObservation{
			AdapterID: adapterID,
			Observation: Observation{
				ID: observationID, EntityID: dispatch.EntityID, Value: value,
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &dispatch.ID,
			},
			ObservedAt: time.Now().UTC(),
		})
		return CommandAcceptance{Accepted: true}, err
	})
	runDeviceTestService(t, service, delivery)
	entityID := registerCommandEntity(t, service)

	type outcome struct {
		result CommandResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, parameters := range []CommandParameters{CommandParameters(`{"value":true}`), CommandParameters(`{"value":false}`)} {
		go func(parameters CommandParameters) {
			result, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, parameters)
			outcomes <- outcome{result: result, err: err}
		}(parameters)
	}
	<-arrived
	<-arrived
	close(release)
	seen := make(map[CommandID]bool)
	for range 2 {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		seen[outcome.result.CommandID] = true
	}
	for _, id := range commandIDs {
		stored, err := getCommand(context.Background(), database, id)
		if err != nil {
			t.Fatal(err)
		}
		if !seen[id] || stored.status != commandStatusSatisfied || stored.outcomeObservationID == nil ||
			*stored.outcomeObservationID != observationIDs[id] {
			t.Fatalf("Command %s = %#v, seen = %#v", id, stored, seen)
		}
	}
}

func TestExecuteCommandContinuesAfterCallerCancellation(t *testing.T) {
	calls := make(chan CommandDispatch, 1)
	release := make(chan struct{})
	service, database := newRunningDeviceTestService(t, commandTestControls(), commandDeliveryFunc(func(_ context.Context, _ string, dispatch CommandDispatch) (CommandAcceptance, error) {
		calls <- dispatch
		<-release
		return CommandAcceptance{Accepted: false}, nil
	}))
	entityID := registerCommandEntity(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(ctx, entityID, OperationNameSet, CommandParameters(`{"value":true}`))
		returned <- err
	}()
	dispatch := <-calls
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		stored, err := getCommand(context.Background(), database, dispatch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.status == commandStatusRejected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Command did not finish after cancellation: %#v", stored)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExecuteCommandPreservesContextValuesAfterCallerCancellation(t *testing.T) {
	type contextKey string
	const key contextKey = "test"
	entered := make(chan struct{})
	release := make(chan struct{})
	seen := make(chan any, 1)
	service, _ := newRunningDeviceTestService(t, commandTestControls(), commandDeliveryFunc(func(ctx context.Context, _ string, _ CommandDispatch) (CommandAcceptance, error) {
		close(entered)
		<-release
		if err := ctx.Err(); err != nil {
			return CommandAcceptance{}, fmt.Errorf("detached delivery context was canceled: %w", err)
		}
		seen <- ctx.Value(key)
		return CommandAcceptance{Accepted: false}, nil
	}))
	entityID := registerCommandEntity(t, service)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "preserved"))
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(ctx, entityID, OperationNameSet, CommandParameters(`{"value":true}`))
		returned <- err
	}()
	<-entered
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	close(release)
	if value := <-seen; value != "preserved" {
		t.Fatalf("context value = %v", value)
	}
}

func commandTestControls() serviceControls {
	controls := productionServiceControls()
	controls.newCommandID = func() (CommandID, error) { return commandTestID, nil }
	controls.newCorrelationID = func() (CorrelationID, error) { return commandTestCorrelationID, nil }
	return controls
}

func registerCommandEntity(t *testing.T, service *Service) EntityID {
	t.Helper()
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	return binding.Entities[0].EntityID
}
