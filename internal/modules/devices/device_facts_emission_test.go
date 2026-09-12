package devices //nolint:testpackage // Tests drive package-private fact emission seams and barriers.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// recordingDeviceFactSink records every fact a service enqueues, in call order,
// and exposes optional hooks so a test can observe a fact at the exact moment
// the service enqueues it.
type recordingDeviceFactSink struct {
	mutex         sync.Mutex
	observations  []ObservationFact
	entityEvents  []EntityEventFact
	commands      []CommandFact
	order         []string
	onObservation func(ObservationFact)
	onCommand     func(CommandFact)
}

func (sink *recordingDeviceFactSink) ObservationAccepted(_ context.Context, fact ObservationFact) {
	sink.mutex.Lock()
	sink.observations = append(sink.observations, fact)
	sink.order = append(sink.order, "observation."+string(fact.Disposition))
	hook := sink.onObservation
	sink.mutex.Unlock()
	if hook != nil {
		hook(fact)
	}
}

func (sink *recordingDeviceFactSink) EntityEventAccepted(_ context.Context, fact EntityEventFact) {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	sink.entityEvents = append(sink.entityEvents, fact)
	sink.order = append(sink.order, "entity-event."+string(fact.Name))
}

func (sink *recordingDeviceFactSink) CommandTransitioned(_ context.Context, fact CommandFact) {
	sink.mutex.Lock()
	sink.commands = append(sink.commands, fact)
	sink.order = append(sink.order, "command."+string(fact.Record.Status))
	hook := sink.onCommand
	sink.mutex.Unlock()
	if hook != nil {
		hook(fact)
	}
}

func (sink *recordingDeviceFactSink) observationFacts() []ObservationFact {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]ObservationFact(nil), sink.observations...)
}

func (sink *recordingDeviceFactSink) entityEventFacts() []EntityEventFact {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]EntityEventFact(nil), sink.entityEvents...)
}

func (sink *recordingDeviceFactSink) commandFacts() []CommandFact {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]CommandFact(nil), sink.commands...)
}

func (sink *recordingDeviceFactSink) factOrder() []string {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]string(nil), sink.order...)
}

type scriptedObservationRepository struct {
	mutex  sync.Mutex
	result ProjectionResult
	err    error
	calls  int
}

func (repository *scriptedObservationRepository) ProjectObservation(
	_ context.Context,
	_ ProjectObservationParams,
) (ProjectionResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.calls++
	return repository.result, repository.err
}

func (*scriptedObservationRepository) DeleteExpiredObservations(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservations call")
}

type scriptedEntityEventRepository struct {
	mutex   sync.Mutex
	results []EntityEventRecordResult
	err     error
	calls   int
}

func (repository *scriptedEntityEventRepository) RecordEntityEvent(
	_ context.Context,
	_ RecordEntityEventParams,
) (EntityEventRecordResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return EntityEventRecordResult{}, repository.err
	}
	result := repository.results[repository.calls]
	repository.calls++
	return result, nil
}

func (*scriptedEntityEventRepository) ListEntityEvents(
	context.Context,
	ListEntityEventsParams,
) (Page[EntityEventHistoryEntry], error) {
	panic("unexpected ListEntityEvents call")
}

func (*scriptedEntityEventRepository) DeleteEntityEventsBefore(context.Context, time.Time, int) (int64, error) {
	panic("unexpected DeleteEntityEventsBefore call")
}

// TestProjectObservationFactsCoverOnlyAcceptedDispositions pins the accepted
// Observation eligibility rule: applied and unchanged commit one fact carrying
// the committed normalized State value and the wire correlation, while
// rejected, duplicate and every result that is not an accepted disposition
// commit none.
func TestProjectObservationFactsCoverOnlyAcceptedDispositions(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	adapterReceivedAt := observedAt.Add(-2 * time.Second)
	sourceUpdatedAt := observedAt.Add(-3 * time.Second)
	tests := []struct {
		name         string
		result       ProjectionResult
		wantFacts    int
		wantValue    string
		wantDisposal ObservationDisposition
	}{
		{
			name: "applied",
			result: ProjectionResult{
				Disposition: DispositionApplied,
				State:       &State{EntityID: factScriptEntityID(), Value: Value(`{"on":true}`)},
			},
			wantFacts: 1, wantValue: `{"on":true}`, wantDisposal: DispositionApplied,
		},
		{
			name: "unchanged",
			result: ProjectionResult{
				Disposition: DispositionUnchanged,
				State:       &State{EntityID: factScriptEntityID(), Value: Value(`true`)},
			},
			wantFacts: 1, wantValue: `true`, wantDisposal: DispositionUnchanged,
		},
		{
			name:      "rejected",
			result:    ProjectionResult{Disposition: DispositionRejected},
			wantFacts: 0,
		},
		{
			name:      "duplicate",
			result:    ProjectionResult{Disposition: DispositionDuplicate},
			wantFacts: 0,
		},
		{
			// A rejected verdict is never a fact even if a repository result
			// inconsistently carries State: eligibility is the disposition, not
			// the presence of a value.
			name: "rejected with state",
			result: ProjectionResult{
				Disposition: DispositionRejected,
				State:       &State{EntityID: factScriptEntityID(), Value: Value(`true`)},
			},
			wantFacts: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &scriptedObservationRepository{result: test.result}
			sink := &recordingDeviceFactSink{}
			service := newTestService(repository, nil, nil, Dependencies{
				DeviceFacts: sink,
				Now:         func() time.Time { return observedAt },
			})
			observation := Observation{
				ID: factScriptObservationID(), EntityID: factScriptEntityID(),
				// The raw report differs from the committed State on purpose:
				// the fact must project the committed value.
				Value:             Value(`"raw report value"`),
				CorrelationID:     factScriptCorrelationID(),
				AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: &sourceUpdatedAt,
			}
			if _, err := service.ProjectObservation(
				context.Background(), "simulator", factScriptRuntimeID(), observation, observedAt,
			); err != nil {
				t.Fatal(err)
			}
			facts := sink.observationFacts()
			if len(facts) != test.wantFacts {
				t.Fatalf("observation facts = %#v, want %d", facts, test.wantFacts)
			}
			if test.wantFacts == 0 {
				return
			}
			fact := facts[0]
			if fact.ObservationID != observation.ID || fact.EntityID != observation.EntityID ||
				fact.Disposition != test.wantDisposal || string(fact.Value) != test.wantValue ||
				fact.CorrelationID != observation.CorrelationID ||
				!fact.AdapterReceivedAt.Equal(adapterReceivedAt) ||
				!fact.ObservedAt.Equal(observedAt) ||
				fact.SourceUpdatedAt == nil || !fact.SourceUpdatedAt.Equal(sourceUpdatedAt) {
				t.Fatalf("observation fact = %#v", fact)
			}
			// The enqueued fact owns its bytes: mutating the repository result
			// after the call must not reach the sink.
			test.result.State.Value[0] = 'x'
			if got := sink.observationFacts()[0]; string(got.Value) != test.wantValue {
				t.Fatalf("fact value changed with the repository result: %s", got.Value)
			}
		})
	}
}

// TestProjectObservationRejectsNoncanonicalCorrelation proves the wire
// correlation is validated before persistence: an Observation fact always
// carries a canonical cor_ identity, so a report without one never reaches the
// transaction and publishes nothing.
func TestProjectObservationRejectsNoncanonicalCorrelation(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, correlationID := range []CorrelationID{
		"",
		"not-a-correlation",
		"cor_01890F47-7a6b-7c4d-8e9f-0123456789ab",
		"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
	} {
		t.Run(string(correlationID), func(t *testing.T) {
			t.Parallel()
			repository := &scriptedObservationRepository{
				result: ProjectionResult{Disposition: DispositionApplied},
			}
			sink := &recordingDeviceFactSink{}
			service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: sink})
			if _, err := service.ProjectObservation(
				context.Background(), "simulator", factScriptRuntimeID(), Observation{
					ID: factScriptObservationID(), EntityID: factScriptEntityID(), Value: Value(`true`),
					CorrelationID: correlationID, AdapterReceivedAt: observedAt,
				}, observedAt,
			); err == nil {
				t.Fatal("noncanonical correlation was accepted")
			}
			if repository.calls != 0 {
				t.Fatalf("persistence calls = %d, want 0", repository.calls)
			}
			if facts := sink.observationFacts(); len(facts) != 0 {
				t.Fatalf("observation facts after rejection = %#v", facts)
			}
		})
	}
}

// TestProjectObservationFailureCommitsNoFact proves the post-commit placement:
// a projection that fails, including a commit failure reported by persistence,
// publishes nothing.
func TestProjectObservationFailureCommitsNoFact(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	commitErr := errors.New("commit observation projection: SQLite write failed")
	repository := &scriptedObservationRepository{err: commitErr}
	sink := &recordingDeviceFactSink{}
	service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: sink})
	_, err := service.ProjectObservation(context.Background(), "simulator", factScriptRuntimeID(), Observation{
		ID: factScriptObservationID(), EntityID: factScriptEntityID(), Value: Value(`true`),
		CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt,
	}, observedAt)
	if !errors.Is(err, commitErr) {
		t.Fatalf("projection error = %v", err)
	}
	if facts := sink.observationFacts(); len(facts) != 0 {
		t.Fatalf("observation facts after failure = %#v", facts)
	}
}

// TestRecordEntityEventFactsCoverOnlyFirstSeenAccepted pins the Entity Event
// eligibility rule and the record time the fact carries.
func TestRecordEntityEventFactsCoverOnlyFirstSeenAccepted(t *testing.T) {
	t.Parallel()
	recordedAt := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	emittedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	receivedAt := time.Date(2026, 9, 1, 12, 0, 0, 500_000_000, time.UTC)
	rejection := EntityEventRejectionUnsupportedEvent
	tests := []struct {
		name      string
		result    EntityEventRecordResult
		err       error
		wantFacts int
	}{
		{
			name:      "accepted",
			result:    EntityEventRecordResult{Outcome: EntityEventOutcomeAccepted, RecordedAt: recordedAt},
			wantFacts: 1,
		},
		{
			name: "rejected",
			result: EntityEventRecordResult{
				Outcome:    EntityEventOutcomeRejected,
				Rejection:  &rejection,
				RecordedAt: recordedAt,
			},
			wantFacts: 0,
		},
		{
			name:      "duplicate",
			result:    EntityEventRecordResult{Outcome: EntityEventOutcomeDuplicate},
			wantFacts: 0,
		},
		{
			name:      "identity conflict",
			result:    EntityEventRecordResult{Outcome: EntityEventOutcomeIdentityConflict},
			wantFacts: 0,
		},
		{
			name:      "recording failure",
			err:       errors.New("insert entity event: SQLite write failed"),
			wantFacts: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &scriptedEntityEventRepository{
				results: []EntityEventRecordResult{test.result}, err: test.err,
			}
			sink := &recordingDeviceFactSink{}
			service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: sink})
			event := factScriptEntityEvent(emittedAt)
			_, recordErr := service.RecordEntityEvent(
				context.Background(), "simulator", factScriptRuntimeID(), event, receivedAt,
			)
			if !errors.Is(recordErr, test.err) {
				t.Fatalf("record error = %v, want %v", recordErr, test.err)
			}
			facts := sink.entityEventFacts()
			if len(facts) != test.wantFacts {
				t.Fatalf("entity event facts = %#v, want %d", facts, test.wantFacts)
			}
			if test.wantFacts == 0 {
				return
			}
			fact := facts[0]
			if fact.EventID != event.ID || fact.EntityID != event.EntityID || fact.Name != event.Name ||
				fact.CorrelationID != event.CorrelationID || !fact.ReportedAt.Equal(emittedAt) ||
				!fact.ReceivedAt.Equal(receivedAt) || !fact.RecordedAt.Equal(recordedAt) {
				t.Fatalf("entity event fact = %#v", fact)
			}
		})
	}
}

// TestCommandCreationFactsCarryOnlyThePersistedStatus proves creation reports
// exactly the committed row: a dispatchable Command publishes requested, and an
// immediate terminal insert publishes only its terminal status without
// inventing a preceding requested transition.
func TestCommandCreationFactsCarryOnlyThePersistedStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		configure  func(*commandRepository)
		wantStatus CommandStatus
		wantErr    error
	}{
		{
			name:       "dispatchable",
			configure:  func(*commandRepository) {},
			wantStatus: CommandStatusRequested,
			wantErr:    ErrOutcomeTimeout,
		},
		{
			name: "disabled entity",
			configure: func(repository *commandRepository) {
				repository.view.Entity.Enabled = false
			},
			wantStatus: CommandStatusEntityDisabled,
			wantErr:    ErrEntityDisabled,
		},
		{
			name: "unhealthy adapter",
			configure: func(repository *commandRepository) {
				repository.forceUnhealthy = true
			},
			wantStatus: CommandStatusAdapterUnhealthy,
			wantErr:    ErrAdapterUnhealthy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newCommandRepository()
			test.configure(repository)
			sink := &recordingDeviceFactSink{}
			dependencies := commandDependencies()
			dependencies.DeviceFacts = sink
			deadline := time.Second
			if test.wantStatus == CommandStatusRequested {
				deadline = 15 * time.Millisecond
			}
			service := newTestService(
				repository,
				commandSenderFunc(
					func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
						return CommandAcceptance{Accepted: true}, nil
					},
				),
				commandCatalog(t, deadline),
				dependencies,
			)
			if _, err := service.ExecuteCommand(context.Background(), CommandInput{
				EntityID:      commandTestEntityID,
				OperationName: OperationNameSet,
				Parameters:    CommandParameters(`{"value":true}`),
			}); !errors.Is(err, test.wantErr) {
				t.Fatalf("command error = %v, want %v", err, test.wantErr)
			}
			facts := sink.commandFacts()
			if test.wantStatus == CommandStatusRequested {
				// The requested fact precedes acceptance and the outcome
				// timeout, so the first fact is creation and it is published
				// before any post-creation transition.
				if len(facts) == 0 || facts[0].Record.Status != CommandStatusRequested {
					t.Fatalf("command facts = %#v", facts)
				}
				return
			}
			if len(facts) != 1 || facts[0].Record.Status != test.wantStatus {
				t.Fatalf("terminal creation facts = %#v, want one %q", facts, test.wantStatus)
			}
		})
	}
}

// TestCommandPersistenceFailuresPublishNoFact proves the post-commit rule on
// the Command path: a creation, acceptance or completion write that fails
// commits nothing, so it publishes nothing, and a failed terminal write leaves
// only the facts of the transitions that really committed. Moving any emission
// before its error check fails this test.
func TestCommandPersistenceFailuresPublishNoFact(t *testing.T) {
	t.Parallel()
	acceptedSender := func() CommandSender {
		return commandSenderFunc(
			func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
				return CommandAcceptance{Accepted: true}, nil
			},
		)
	}
	input := func() CommandInput {
		return CommandInput{
			EntityID:      commandTestEntityID,
			OperationName: OperationNameSet,
			Parameters:    CommandParameters(`{"value":true}`),
		}
	}

	t.Run("creation commit failure", func(t *testing.T) {
		t.Parallel()
		repository := newCommandRepository()
		repository.createErr = errors.New("SQLite unavailable at creation")
		sink := &recordingDeviceFactSink{}
		dependencies := commandDependencies()
		dependencies.DeviceFacts = sink
		service := newTestService(repository, acceptedSender(), commandCatalog(t, time.Second), dependencies)
		if _, err := service.ExecuteCommand(context.Background(), input()); !errors.Is(err, repository.createErr) {
			t.Fatalf("creation error = %v", err)
		}
		if facts := sink.commandFacts(); len(facts) != 0 {
			t.Fatalf("a failed creation published facts: %#v", facts)
		}
	})

	t.Run("acceptance commit failure", func(t *testing.T) {
		t.Parallel()
		ledger := &scriptedCommandLedger{
			commandRepository: newCommandRepository(),
			acceptErr:         errors.New("SQLite unavailable at acceptance"),
		}
		sink := &recordingDeviceFactSink{}
		dependencies := commandDependencies()
		dependencies.DeviceFacts = sink
		service := newTestService(ledger, acceptedSender(), commandCatalog(t, time.Second), dependencies)
		if _, err := service.ExecuteCommand(context.Background(), input()); !errors.Is(err, ledger.acceptErr) {
			t.Fatalf("acceptance error = %v", err)
		}
		assertCommandFactStatuses(t, sink, []CommandStatus{CommandStatusRequested})
		if stored := ledger.command(commandTestID); stored.Status != CommandStatusRequested {
			t.Fatalf("dataless acceptance changed the stored command: %#v", stored)
		}
	})

	t.Run("terminal commit failure", func(t *testing.T) {
		t.Parallel()
		repository := newCommandRepository()
		repository.completeErr = errors.New("SQLite unavailable at completion")
		sink := &recordingDeviceFactSink{}
		dependencies := commandDependencies()
		dependencies.DeviceFacts = sink
		service := newTestService(repository, acceptedSender(), commandCatalog(t, 15*time.Millisecond), dependencies)
		if _, err := service.ExecuteCommand(context.Background(), input()); !errors.Is(err, repository.completeErr) {
			t.Fatalf("completion error = %v", err)
		}
		assertCommandFactStatuses(t, sink, []CommandStatus{CommandStatusRequested, CommandStatusAccepted})
		if stored := repository.command(commandTestID); stored.Status != CommandStatusAccepted {
			t.Fatalf("a failed terminal write fabricated a durable outcome: %#v", stored)
		}
	})
}

// assertCommandFactStatuses pins the exact published Command statuses in order.
func assertCommandFactStatuses(t *testing.T, sink *recordingDeviceFactSink, want []CommandStatus) {
	t.Helper()
	facts := sink.commandFacts()
	statuses := make([]CommandStatus, len(facts))
	for index, fact := range facts {
		statuses[index] = fact.Record.Status
	}
	if !equalStrings(commandStatusStrings(statuses), commandStatusStrings(want)) {
		t.Fatalf("published command statuses = %v, want %v", statuses, want)
	}
}

func commandStatusStrings(statuses []CommandStatus) []string {
	values := make([]string, len(statuses))
	for index, status := range statuses {
		values[index] = string(status)
	}
	return values
}

// scriptedCommandLedger reports transitions chosen by the test while delegating
// Command creation and linked-Observation satisfaction to the command fake.
type scriptedCommandLedger struct {
	*commandRepository

	acceptance  CommandTransition
	complete    CommandTransition
	acceptErr   error
	completeErr error
}

func (ledger *scriptedCommandLedger) MarkCommandAccepted(
	context.Context,
	CommandID,
	time.Time,
) (CommandTransition, error) {
	if ledger.acceptErr != nil {
		return CommandTransition{}, ledger.acceptErr
	}
	return ledger.acceptance, nil
}

func (ledger *scriptedCommandLedger) CompleteCommand(context.Context, CommandCompletion) (CommandTransition, error) {
	if ledger.completeErr != nil {
		return CommandTransition{}, ledger.completeErr
	}
	return ledger.complete, nil
}

// TestCommandNoOpTransitionsPublishNothing proves a transition that reports
// Changed false is not a fact, even when persistence also returns the unchanged
// record, and that the Command still reaches its existing terminal outcome.
func TestCommandNoOpTransitionsPublishNothing(t *testing.T) {
	t.Parallel()
	stale := CommandRecord{
		ID: commandTestID, EntityID: commandTestEntityID, Status: CommandStatusOutcomeTimeout,
		Parameters: CommandParameters(`{"value":true}`), FailureCode: new(CommandFailureOutcomeTimeout),
	}
	ledger := &scriptedCommandLedger{
		commandRepository: newCommandRepository(),
		acceptance:        CommandTransition{Record: CommandRecord{ID: commandTestID, Status: CommandStatusAccepted}},
		complete:          CommandTransition{Record: stale},
	}
	sink := &recordingDeviceFactSink{}
	dependencies := commandDependencies()
	dependencies.DeviceFacts = sink
	service := newTestService(
		ledger,
		commandSenderFunc(
			func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
				return CommandAcceptance{Accepted: true}, nil
			},
		),
		commandCatalog(t, 15*time.Millisecond),
		dependencies,
	)
	if _, err := service.ExecuteCommand(context.Background(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(`{"value":true}`),
	}); !errors.Is(err, ErrOutcomeTimeout) {
		t.Fatalf("command error = %v", err)
	}
	facts := sink.commandFacts()
	if len(facts) != 1 || facts[0].Record.Status != CommandStatusRequested {
		t.Fatalf("command facts for no-op transitions = %#v, want only the creation fact", facts)
	}
}

// TestObservationSatisfactionPublishesObservationBeforeCommandAndNotifiesWaiterFirst
// pins the ordering guarantee for the one transaction that both commits an
// Observation and satisfies a linked Command: the waiter is notified before any
// transport work, the Observation fact is enqueued before the Command fact, and
// both facts carry the committed records rather than repository buffers.
func TestObservationSatisfactionPublishesObservationBeforeCommandAndNotifiesWaiterFirst(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	satisfiedRecord := CommandRecord{
		ID: commandTestID, EntityID: commandTestEntityID, AdapterID: "simulator",
		Status: CommandStatusSatisfied, Parameters: CommandParameters(`{"value":true}`),
		CompletedAt: &observedAt, OutcomeObservationID: new(commandTestObservationID),
	}
	result := ProjectionResult{
		Disposition: DispositionApplied,
		State:       &State{EntityID: commandTestEntityID, Value: Value(`true`)},
		SatisfiedCommand: &CommandResult{
			CommandID: commandTestID, Outcome: OutcomeObserved,
			ObservationID: new(commandTestObservationID), Value: new(Value(`true`)),
		},
		SatisfiedCommandRecord: &satisfiedRecord,
	}
	repository := &scriptedObservationRepository{result: result}
	var service *Service
	sink := &recordingDeviceFactSink{}
	waiterNotifiedBeforeFacts := false
	sink.onObservation = func(ObservationFact) {
		service.waiters.mutex.Lock()
		waiter := service.waiters.byID[commandTestID]
		service.waiters.mutex.Unlock()
		waiterNotifiedBeforeFacts = len(waiter) == 1
	}
	dependencies := commandDependencies()
	dependencies.DeviceFacts = sink
	service = newTestService(repository, nil, nil, dependencies)
	waiter := service.addCommandWaiter(commandTestID)
	defer service.removeCommandWaiter(commandTestID)

	projected, err := service.ProjectObservation(
		context.Background(), "simulator", commandTestRuntimeID, Observation{
			ID: commandTestObservationID, EntityID: commandTestEntityID, Value: Value(`true`),
			CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt,
		}, observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if projected.SatisfiedCommandRecord == nil || projected.SatisfiedCommandRecord.Status != CommandStatusSatisfied {
		t.Fatalf("projection result = %#v", projected)
	}
	select {
	case delivered := <-waiter:
		if delivered.CommandID != commandTestID {
			t.Fatalf("waiter result = %#v", delivered)
		}
	default:
		t.Fatal("waiter was not notified")
	}
	if !waiterNotifiedBeforeFacts {
		t.Fatal("facts were enqueued before the Command waiter was notified")
	}
	want := []string{"observation.applied", "command.satisfied"}
	if got := sink.factOrder(); !equalStrings(got, want) {
		t.Fatalf("fact order = %v, want %v", got, want)
	}
	// Both facts own their data: the committed records the repository still
	// holds cannot rewrite what was enqueued.
	satisfiedRecord.Parameters[0] = 'x'
	result.State.Value[0] = 'x'
	facts := sink.commandFacts()
	if string(facts[0].Record.Parameters) != `{"value":true}` {
		t.Fatalf("command fact parameters = %s", facts[0].Record.Parameters)
	}
	if got := string(sink.observationFacts()[0].Value); got != "true" {
		t.Fatalf("observation fact value = %s", got)
	}
}

// barrierCommandRepository holds acceptance inside the client's callback, which
// is the point at which the service holds the Command's transition stripe, and
// waits for the competing satisfaction fact.
type barrierCommandRepository struct {
	*commandRepository

	acceptanceCommitted chan struct{}
	satisfiedObserved   chan struct{}
	closeOnce           sync.Once
	barrier             time.Duration
}

func (repository *barrierCommandRepository) MarkCommandAccepted(
	ctx context.Context,
	id CommandID,
	acceptedAt time.Time,
) (CommandTransition, error) {
	transition, err := repository.commandRepository.MarkCommandAccepted(ctx, id, acceptedAt)
	if err != nil {
		return transition, err
	}
	repository.closeOnce.Do(func() { close(repository.acceptanceCommitted) })
	// Waiting here can only end early when the acceptance fact was not fenced
	// against the competing Observation, which is exactly the defect the stripe
	// prevents. The stripe itself decides the race, never this duration.
	select {
	case <-repository.satisfiedObserved:
	case <-time.After(repository.barrier):
	}
	return transition, nil
}

// TestCommandFactsFollowDurableOrderWhenSatisfactionRacesAcceptance pins the
// striped per-Command sequencing: an accepted transition already committed to
// SQLite is published before the satisfied transition of a competing
// Observation, so published facts for one Command follow durable order.
func TestCommandFactsFollowDurableOrderWhenSatisfactionRacesAcceptance(t *testing.T) {
	t.Parallel()
	satisfiedObserved := make(chan struct{})
	var signalSatisfied sync.Once
	observedAt := time.Now().UTC()
	sink := &recordingDeviceFactSink{}
	sink.onCommand = func(fact CommandFact) {
		if fact.Record.Status == CommandStatusSatisfied {
			signalSatisfied.Do(func() { close(satisfiedObserved) })
		}
	}
	repository := &barrierCommandRepository{
		commandRepository:   newCommandRepository(),
		acceptanceCommitted: make(chan struct{}),
		satisfiedObserved:   satisfiedObserved,
		barrier:             250 * time.Millisecond,
	}
	dependencies := commandDependencies()
	dependencies.DeviceFacts = sink
	dependencies.Now = func() time.Time { return observedAt }
	service := newTestService(
		repository,
		commandSenderFunc(
			func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
				return CommandAcceptance{Accepted: true}, nil
			},
		),
		commandCatalog(t, 10*time.Second),
		dependencies,
	)

	completed := make(chan CommandResult, 1)
	failures := make(chan error, 1)
	go func() {
		result, err := service.ExecuteCommand(context.Background(), CommandInput{
			EntityID:      commandTestEntityID,
			OperationName: OperationNameSet,
			Parameters:    CommandParameters(`{"value":true}`),
		})
		if err != nil {
			failures <- err
			return
		}
		completed <- result
	}()

	select {
	case <-repository.acceptanceCommitted:
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("acceptance was never committed")
	}
	// The Observation arrives only after acceptance is durable, so acceptance
	// precedes satisfaction in SQLite.
	if _, err := service.ProjectObservation(
		context.Background(), "simulator", commandTestRuntimeID, Observation{
			ID: commandTestObservationID, EntityID: commandTestEntityID, Value: Value(`true`),
			CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt,
			RefreshForCommand: new(commandTestID),
		}, observedAt,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-completed:
		if result.CommandID != commandTestID || result.Outcome != OutcomeObserved {
			t.Fatalf("command result = %#v", result)
		}
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("command did not complete")
	}

	// Durable order is requested, accepted, satisfied. The observation fact for
	// the same transaction is enqueued before the satisfied Command fact, and
	// the accepted fact can never be enqueued after satisfied.
	want := []string{
		"command.requested", "command.accepted", "observation.unchanged", "command.satisfied",
	}
	if got := sink.factOrder(); !equalStrings(got, want) {
		t.Fatalf("fact order = %v, want %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	return slices.Equal(got, want)
}

func factScriptEntityID() EntityID {
	return EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
}

func factScriptObservationID() ObservationID {
	return ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
}

func factScriptEventID() EntityEventID {
	return EntityEventID("evt_01890f47-7a6b-7c4d-8e9f-0123456789ab")
}

func factScriptRuntimeID() RuntimeID {
	return RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
}

func factScriptCorrelationID() CorrelationID {
	return CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")
}

func factScriptEntityEvent(emittedAt time.Time) EntityEvent {
	return EntityEvent{
		ID: factScriptEventID(), EntityID: factScriptEntityID(), Name: EntityEventName("single_press"),
		CorrelationID: factScriptCorrelationID(), EmittedAt: emittedAt,
	}
}
