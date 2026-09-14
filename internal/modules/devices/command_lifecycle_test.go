package devices //nolint:testpackage // Tests exercise the command admission gate and worker drain.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Shutdown must reject new Commands while an admitted worker drains, and
// WaitCommands must join the detached lifecycle even when the caller already
// returned.
func TestCommandAdmissionGateRejectsDirectWhileAdmittedDrains(t *testing.T) {
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
	// A new Command is rejected without touching persistence, even when
	// both reserved identities are supplied.
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
	// The waiter-blocked worker still drains: satisfy it through the retained
	// observation path after admission closed.
	refreshFor := commandTestID
	if _, err := service.ProjectObservation(
		context.Background(),
		"simulator",
		commandTestRuntimeID,
		Observation{
			ID: commandTestObservationID, EntityID: commandTestEntityID, Value: Value(`true`),
			CorrelationID: commandTestCorrelationID, AdapterReceivedAt: time.Now().UTC(),
			RefreshForCommand: &refreshFor,
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
	requireCommandsIdle(t, service)
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

// A canceled wait reports the caller's error without disturbing the workers:
// the admitted lifecycle stays live until its sender releases it.
func TestWaitCommandsCanceledWaitKeepsWorkers(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		entered <- struct{}{}
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
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not enter sender")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.WaitCommands(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v, want context.Canceled", err)
	}
	// The canceled wait left the admitted lifecycle registered, so a fresh
	// bounded wait still observes the group as live.
	bounded, cancelBounded := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelBounded()
	if err := service.WaitCommands(bounded); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait after canceled wait = %v, want %v while the worker was live", err, context.DeadlineExceeded)
	}
	// Releasing the sender lets the detached lifecycle finish and the next wait
	// succeed.
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
	requireCommandsIdle(t, service)
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
				CorrelationID: commandTestCorrelationID, AdapterReceivedAt: time.Now().UTC(),
				RefreshForCommand: &request.ID,
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
	requireCommandsIdle(t, service)
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
// admission closes Commands are rejected even when both reserved identities
// are supplied.
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
	requireCommandsIdle(t, service)

	repository.createErr = errors.New("SQLite unavailable")
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, repository.createErr) {
		t.Fatalf("creation error = %v", err)
	}
	repository.createErr = nil
	requireCommandsIdle(t, service)
	if dispatches != 0 {
		t.Fatalf("dispatches = %d, want 0 before durable creation", dispatches)
	}

	service.StopCommandAdmission()
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		ID: commandTestID, CorrelationID: commandTestCorrelationID,
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrCommandUnavailable) {
		t.Fatalf("direct reserved-identities error after closure = %v, want %v", err, ErrCommandUnavailable)
	}
	requireCommandsIdle(t, service)
}

// requireCommandsIdle checks that every admitted Command has drained and no
// outcome waiter remains registered.
func requireCommandsIdle(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.WaitCommands(ctx); err != nil {
		t.Fatalf("WaitCommands = %v, want nil while idle", err)
	}
	service.waiters.mutex.Lock()
	waiters := len(service.waiters.byID)
	service.waiters.mutex.Unlock()
	if waiters != 0 {
		t.Fatalf("waiters = %d, want 0", waiters)
	}
}
