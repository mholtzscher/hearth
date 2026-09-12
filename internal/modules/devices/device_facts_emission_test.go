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
	order         []string
	onObservation func(ObservationFact)
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

// TestObservationSatisfactionNotifiesWaiterBeforeObservationFact pins the
// ordering guarantee for the one transaction that both commits an Observation
// and satisfies a linked Command: the waiter is notified before any transport
// work, and the Observation fact carries committed bytes rather than repository
// buffers.
func TestObservationSatisfactionNotifiesWaiterBeforeObservationFact(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	result := ProjectionResult{
		Disposition: DispositionApplied,
		State:       &State{EntityID: commandTestEntityID, Value: Value(`true`)},
		SatisfiedCommand: &CommandResult{
			CommandID: commandTestID, Outcome: OutcomeObserved,
			ObservationID: new(commandTestObservationID), Value: new(Value(`true`)),
		},
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
	if projected.SatisfiedCommand == nil || projected.SatisfiedCommand.Outcome != OutcomeObserved {
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
		t.Fatal("the Observation fact was enqueued before the Command waiter was notified")
	}
	want := []string{"observation.applied"}
	if got := sink.factOrder(); !equalStrings(got, want) {
		t.Fatalf("fact order = %v, want %v", got, want)
	}
	// The fact owns its data: the committed State the repository still holds
	// cannot rewrite what was enqueued.
	result.State.Value[0] = 'x'
	if got := string(sink.observationFacts()[0].Value); got != "true" {
		t.Fatalf("observation fact value = %s", got)
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
