package devices //nolint:testpackage // Tests exercise the command admission gate and worker drain.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Shutdown must reject new direct Commands while still tracking explicit
// automation Steps admitted before closure, and WaitCommands must join the
// detached lifecycle even when the caller already returned.
func TestCommandAdmissionGateRejectsDirectButTracksReserved(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	entered := make(chan CommandRequest, 4)
	service := newTestService(repository, commandSenderFunc(func(
		_ context.Context, _ string, _ RuntimeID, request CommandRequest,
	) (CommandAcceptance, error) {
		entered <- request
		return CommandAcceptance{Accepted: true}, nil
	}), commandCatalog(t, time.Minute), commandDependencies())

	if !service.CommandAdmissionOpen() {
		t.Fatal("admission starts closed")
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(context.Background(), CommandInput{
			EntityID: commandTestEntityID, OperationName: OperationNameSet,
			Parameters: CommandParameters(`{"value":true}`),
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not enter sender")
	}
	service.StopCommandAdmission()
	if service.CommandAdmissionOpen() {
		t.Fatal("admission stayed open after stop")
	}
	// A new direct Command is rejected without touching persistence, even when
	// both reserved identities are supplied: HTTP can never supply explicit
	// automation Step permission.
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID: commandTestEntityID, OperationName: OperationNameSet,
		Parameters: CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrCommandUnavailable) {
		t.Fatalf("direct error = %v, want %v", err, ErrCommandUnavailable)
	}
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID: commandTestEntityID, OperationName: OperationNameSet,
		Parameters: CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrCommandUnavailable) {
		t.Fatalf("direct reserved-identities error = %v, want %v", err, ErrCommandUnavailable)
	}
	// Explicit automation Steps still track after closure.
	service.admitAutomationStepWorker()
	service.releaseCommandWorker()
	// The waiter-blocked worker still drains: satisfy it through the retained
	// observation path after admission closed.
	refreshFor := commandTestID
	if _, err := service.ProjectObservation(
		context.Background(),
		"simulator",
		commandTestRuntimeID,
		Observation{
			ID: commandTestObservationID, EntityID: commandTestEntityID, Value: Value(`true`),
			AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &refreshFor,
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drained error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted command did not drain")
	}
	if err := service.WaitCommands(context.Background()); err != nil {
		t.Fatalf("wait = %v", err)
	}
	service.lifecycleMu.Lock()
	workers, idle := service.commandWorkers, service.commandIdle
	service.lifecycleMu.Unlock()
	if workers != 0 {
		t.Fatalf("workers = %d, want 0", workers)
	}
	select {
	case <-idle:
	default:
		t.Fatal("idle channel stayed open with no workers")
	}
}

// Explicit automation Step permission dispatches after admission closes while
// direct Commands cannot: the Step intent committed before closure drains.
func TestAutomationStepCommandDispatchesAfterAdmissionCloses(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	entered := make(chan CommandRequest, 1)
	service := newTestService(repository, commandSenderFunc(func(
		_ context.Context, _ string, _ RuntimeID, request CommandRequest,
	) (CommandAcceptance, error) {
		entered <- request
		return CommandAcceptance{Accepted: true}, nil
	}), commandCatalog(t, time.Minute), commandDependencies())
	service.StopCommandAdmission()
	// Reserved execution without both identities is a caller bug, not admission.
	if _, err := service.ExecuteAutomationStepCommand(context.Background(), CommandInput{
		EntityID: commandTestEntityID, OperationName: OperationNameSet,
		Parameters: CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("reserved without identities error = %v, want %v", err, ErrInvalidCommand)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.ExecuteAutomationStepCommand(context.Background(), CommandInput{
			ID: commandTestID, CorrelationID: commandTestCorrelationID,
			EntityID: commandTestEntityID, OperationName: OperationNameSet,
			Parameters: CommandParameters(`{"value":true}`),
		})
		done <- err
	}()
	select {
	case request := <-entered:
		if request.ID != commandTestID || request.CorrelationID != commandTestCorrelationID {
			t.Fatalf("reserved identities not dispatched: %#v", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reserved Step did not dispatch after admission closure")
	}
	refreshFor := commandTestID
	if _, err := service.ProjectObservation(
		context.Background(),
		"simulator",
		commandTestRuntimeID,
		Observation{
			ID: commandTestObservationID, EntityID: commandTestEntityID, Value: Value(`true`),
			AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &refreshFor,
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reserved drain error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reserved Step did not drain")
	}
	if err := service.WaitCommands(context.Background()); err != nil {
		t.Fatalf("wait = %v", err)
	}
}

// Caller cancellation stops waiting but the detached worker must still drain
// through WaitCommands before dependencies tear down.
func TestWaitCommandsDrainsDetachedWorkerAfterCallerCancellation(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	dispatched := make(chan CommandRequest, 1)
	release := make(chan struct{})
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		dispatched <- CommandRequest{ID: commandTestID}
		<-release
		return CommandAcceptance{Accepted: false}, nil
	}), commandCatalog(t, time.Minute), commandDependencies())

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(ctx, CommandInput{
			EntityID: commandTestEntityID, OperationName: OperationNameSet,
			Parameters: CommandParameters(`{"value":true}`),
		})
		returned <- err
	}()
	select {
	case <-dispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not enter sender")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller did not return after cancellation")
	}
	service.StopCommandAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.WaitCommands(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("wait returned while detached worker blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detached worker did not drain after release")
	}
	if got := repository.command(commandTestID).Status; got != CommandStatusRejected {
		t.Fatalf("status = %q, want rejected", got)
	}
}

// A canceled wait reports the caller's error without disturbing the workers.
func TestWaitCommandsCanceledWaitKeepsWorkers(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	release := make(chan struct{})
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		<-release
		return CommandAcceptance{Accepted: false}, nil
	}), commandCatalog(t, time.Minute), commandDependencies())
	done := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(context.Background(), CommandInput{
			EntityID: commandTestEntityID, OperationName: OperationNameSet,
			Parameters: CommandParameters(`{"value":true}`),
		})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		service.lifecycleMu.Lock()
		workers := service.commandWorkers
		service.lifecycleMu.Unlock()
		if workers == 1 || time.Now().After(deadline) {
			if workers != 1 {
				t.Fatal("worker was never registered")
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.WaitCommands(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v, want context.Canceled", err)
	}
	// The canceled wait left the worker registered; releasing the sender lets
	// the detached lifecycle finish and the next wait succeed.
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, ErrUpstreamRejected) {
			t.Fatalf("drained error = %v, want %v", err, ErrUpstreamRejected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish after release")
	}
	if got := repository.command(commandTestID).Status; got != CommandStatusRejected {
		t.Fatalf("status = %q, want rejected", got)
	}
	if err := service.WaitCommands(context.Background()); err != nil {
		t.Fatalf("wait = %v", err)
	}
}

// This test protects shutdown drain under a blocked diagnostic sink and fails
// if a completed worker still holds lifecycle tracking while the synchronous
// creation log blocks. The worker must release before logging so WaitCommands
// completes even though command.created is still pending.
func TestWaitCommandsCompletesWhileCreationLogBlocked(t *testing.T) {
	t.Parallel()
	writer, logger, entered, release := newBlockingCommandLogSink()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	repository := newCommandRepository()
	var service *Service
	sender := commandSenderFunc(
		func(ctx context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			observation := Observation{
				ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
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
	service = newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))

	result, err := service.ExecuteCommand(commandOperationContext(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireObservedResult(t, result)

	// The completed worker blocks inside the synchronous creation emission.
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked command.created emission was never attempted")
	}

	// Lifecycle tracking is already released while the log stays blocked.
	service.lifecycleMu.Lock()
	workers := service.commandWorkers
	service.lifecycleMu.Unlock()
	if workers != 0 {
		t.Fatalf("workers = %d, want 0 while creation log is blocked", workers)
	}

	// Shutdown closes admission, then drains: the wait must succeed without
	// waiting for the still-blocked diagnostic sink.
	service.StopCommandAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.WaitCommands(context.Background()) }()
	select {
	case waitErr := <-waited:
		if waitErr != nil {
			t.Fatalf("wait = %v", waitErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitCommands did not complete while command.created writer was blocked")
	}

	close(release)
	created := waitForCommandEvent(t, writer, "command.created")
	requireCommandField(t, created, "command_id", string(commandTestID))
}

// Immediate terminal Commands must release lifecycle tracking before their
// synchronous creation log, so shutdown drain finishes while the diagnostic
// sink is still blocked. The caller stays blocked in the log emission until
// released, then observes the terminal error and the creation record.
func TestWaitCommandsCompletesWhileImmediateTerminalCreationLogBlocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		disabled  bool
		unhealthy bool
		wantErr   error
	}{
		{"entity disabled", true, false, ErrEntityDisabled},
		{"adapter unhealthy", false, true, ErrAdapterUnhealthy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requireImmediateTerminalDrainsWhileLogBlocked(t, test.disabled, test.unhealthy, test.wantErr)
		})
	}
}

func requireImmediateTerminalDrainsWhileLogBlocked(t *testing.T, disabled, unhealthy bool, wantErr error) {
	t.Helper()
	writer, logger, entered, release := newBlockingCommandLogSink()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	repository := newCommandRepository()
	repository.view.Entity.Enabled = !disabled
	repository.forceUnhealthy = unhealthy
	sender := commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		t.Error("immediate terminal command dispatched")
		return CommandAcceptance{}, nil
	})
	service := newTestService(
		repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger),
	)

	type outcome struct{ err error }
	finished := make(chan outcome, 1)
	go func() {
		_, err := service.ExecuteCommand(commandOperationContext(), CommandInput{
			EntityID:      commandTestEntityID,
			OperationName: OperationNameSet,
			Parameters:    CommandParameters(`{"value":true}`),
		})
		finished <- outcome{err: err}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked immediate terminal command.created emission was never attempted")
	}

	service.lifecycleMu.Lock()
	workers := service.commandWorkers
	service.lifecycleMu.Unlock()
	if workers != 0 {
		t.Fatalf("workers = %d, want 0 while immediate terminal creation log is blocked", workers)
	}

	service.StopCommandAdmission()
	requireWaitCommandsSucceedsWhileLogBlocked(t, service)

	close(release)
	select {
	case completed := <-finished:
		requireTerminalExecutionError(t, completed.err, wantErr)
	case <-time.After(3 * time.Second):
		t.Fatal("immediate terminal ExecuteCommand did not return after log release")
	}

	created := waitForCommandEvent(t, writer, "command.created")
	requireCommandField(t, created, "command_id", string(commandTestID))
	assertCommandWorkerIdle(t, service)
}

func requireWaitCommandsSucceedsWhileLogBlocked(t *testing.T, service *Service) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- service.WaitCommands(context.Background()) }()
	select {
	case waitErr := <-waited:
		if waitErr != nil {
			t.Fatalf("wait = %v", waitErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitCommands did not complete while command.created was blocked")
	}
}

func requireTerminalExecutionError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("terminal error = %v, want %v", err, wantErr)
	}
	var executionError *CommandExecutionError
	if !errors.As(err, &executionError) || executionError.CommandID != commandTestID {
		t.Fatalf("execution error = %#v", err)
	}
}

// Pre-creation failures must never retain lifecycle tracking, and after
// admission closes only the explicit automation Step method still admits:
// direct Commands are rejected even when both reserved identities are supplied.
func TestCommandErrorPathsReleaseWorkerWithoutLeak(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	dispatches := 0
	sender := commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		dispatches++
		return CommandAcceptance{Accepted: true}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandDependencies())

	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":1}`),
	}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("invalid parameters error = %v", err)
	}
	assertCommandWorkerIdle(t, service)

	repository.createErr = errors.New("SQLite unavailable")
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, repository.createErr) {
		t.Fatalf("creation error = %v", err)
	}
	repository.createErr = nil
	assertCommandWorkerIdle(t, service)
	if dispatches != 0 {
		t.Fatalf("dispatches = %d, want 0 before durable creation", dispatches)
	}

	// Reserved automation Step calls without both identities are caller bugs,
	// not admission: they must fail without retaining tracking.
	if _, err := service.ExecuteAutomationStepCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("reserved without identities error = %v", err)
	}
	assertCommandWorkerIdle(t, service)

	service.StopCommandAdmission()
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrCommandUnavailable) {
		t.Fatalf("direct reserved-identities error after closure = %v, want %v", err, ErrCommandUnavailable)
	}
	assertCommandWorkerIdle(t, service)

	// The explicit Step method still admits after closure: the disabled
	// terminal record commits without dispatch and releases tracking.
	repository.view.Entity.Enabled = false
	if _, err := service.ExecuteAutomationStepCommand(context.Background(), CommandInput{
		ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrEntityDisabled) {
		t.Fatalf("reserved Step after closure error = %v, want %v", err, ErrEntityDisabled)
	}
	if stored := repository.command(commandTestID); stored.Status != CommandStatusEntityDisabled {
		t.Fatalf("stored Step command = %#v, want entity_disabled", stored)
	}
	assertCommandWorkerIdle(t, service)
}

func assertCommandWorkerIdle(t *testing.T, service *Service) {
	t.Helper()
	service.lifecycleMu.Lock()
	workers := service.commandWorkers
	idle := service.commandIdle
	service.lifecycleMu.Unlock()
	if workers != 0 {
		t.Fatalf("workers = %d, want 0", workers)
	}
	select {
	case <-idle:
	default:
		t.Fatal("idle channel stayed open with no workers")
	}
	service.waiters.mutex.Lock()
	waiters := len(service.waiters.byID)
	service.waiters.mutex.Unlock()
	if waiters != 0 {
		t.Fatalf("waiters = %d, want 0", waiters)
	}
	if err := service.WaitCommands(context.Background()); err != nil {
		t.Fatalf("wait = %v", err)
	}
}
