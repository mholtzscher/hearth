package devices //nolint:testpackage // Tests drive package-private fact notification seams and barriers.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingDeviceFactNotifier counts the wake hints a service sends and exposes
// an optional hook so a test can observe the exact moment one arrives.
type recordingDeviceFactNotifier struct {
	mutex    sync.Mutex
	notified int
	onNotify func()
}

func (notifier *recordingDeviceFactNotifier) NotifyPendingDeviceFacts() {
	notifier.mutex.Lock()
	notifier.notified++
	hook := notifier.onNotify
	notifier.mutex.Unlock()
	if hook != nil {
		hook()
	}
}

func (notifier *recordingDeviceFactNotifier) count() int {
	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()
	return notifier.notified
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

// TestProjectObservationNotifiesOnlyForQueuedPendingFact pins the service side
// of the eligibility contract: the repository decides eligibility and reports
// the queued fact identity, and the service wakes the relay exactly when that
// identity is present. Rejected and duplicate outcomes carry no identity, so
// they wake nobody.
func TestProjectObservationNotifiesOnlyForQueuedPendingFact(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pendingFactID := DeviceFactID("fct_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	tests := []struct {
		name       string
		result     ProjectionResult
		wantNotify int
	}{
		{
			name: "applied with queued fact",
			result: ProjectionResult{
				Disposition:   DispositionApplied,
				State:         &State{EntityID: factScriptEntityID(), Value: Value(`{"on":true}`)},
				PendingFactID: &pendingFactID,
			},
			wantNotify: 1,
		},
		{
			name: "unchanged with queued fact",
			result: ProjectionResult{
				Disposition:   DispositionUnchanged,
				State:         &State{EntityID: factScriptEntityID(), Value: Value(`true`)},
				PendingFactID: &pendingFactID,
			},
			wantNotify: 1,
		},
		{
			// Eligibility is the repository's committed pending fact, never the
			// presence of a disposition or a State the repository happened to
			// return.
			name: "applied without queued fact",
			result: ProjectionResult{
				Disposition: DispositionApplied,
				State:       &State{EntityID: factScriptEntityID(), Value: Value(`true`)},
			},
			wantNotify: 0,
		},
		{
			name:       "rejected",
			result:     ProjectionResult{Disposition: DispositionRejected},
			wantNotify: 0,
		},
		{
			name:       "duplicate",
			result:     ProjectionResult{Disposition: DispositionDuplicate},
			wantNotify: 0,
		},
		{
			name: "rejected with state",
			result: ProjectionResult{
				Disposition: DispositionRejected,
				State:       &State{EntityID: factScriptEntityID(), Value: Value(`true`)},
			},
			wantNotify: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &scriptedObservationRepository{result: test.result}
			notifier := &recordingDeviceFactNotifier{}
			service := newTestService(repository, nil, nil, Dependencies{
				DeviceFacts: notifier,
				Now:         func() time.Time { return observedAt },
			})
			observation := Observation{
				ID: factScriptObservationID(), EntityID: factScriptEntityID(),
				Value:         Value(`"raw report value"`),
				CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt.Add(-time.Second),
			}
			if _, err := service.ProjectObservation(
				context.Background(), "simulator", factScriptRuntimeID(), observation, observedAt,
			); err != nil {
				t.Fatal(err)
			}
			if got := notifier.count(); got != test.wantNotify {
				t.Fatalf("notifications = %d, want %d", got, test.wantNotify)
			}
		})
	}
}

// TestProjectObservationRejectsNoncanonicalCorrelation proves the wire
// correlation is validated before persistence: a rejected input never reaches
// the transaction, so it can queue no pending fact and wake nobody.
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
			notifier := &recordingDeviceFactNotifier{}
			service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: notifier})
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
			if got := notifier.count(); got != 0 {
				t.Fatalf("notifications after rejection = %d, want 0", got)
			}
		})
	}
}

// TestObservationAndEntityEventRejectUnboundedTrace proves the persistence
// bound on the carried trace context is enforced before any transaction, so an
// oversized or non-printable header can never reach SQLite and never queues a
// pending fact.
func TestObservationAndEntityEventRejectUnboundedTrace(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	traces := []DeviceFactTraceContext{
		{Traceparent: "00-" + strings.Repeat("a", 256) + "-0000000000000001-01"},
		{Tracestate: strings.Repeat("a", 513)},
		{Traceparent: "00-00000000000000000000000000000001-0000000000000001-01\n"},
		{Tracestate: "vendor=value\x00"},
	}
	for _, trace := range traces {
		observationRepository := &scriptedObservationRepository{
			result: ProjectionResult{Disposition: DispositionApplied, PendingFactID: deviceFactIDPointer()},
		}
		notifier := &recordingDeviceFactNotifier{}
		service := newTestService(
			observationRepository, nil, nil, Dependencies{DeviceFacts: notifier},
		)
		_, err := service.ProjectObservation(
			context.Background(), "simulator", factScriptRuntimeID(), Observation{
				ID: factScriptObservationID(), EntityID: factScriptEntityID(), Value: Value(`true`),
				CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt, Trace: trace,
			}, observedAt,
		)
		if !errors.Is(err, ErrInvalidDeviceFactTrace) {
			t.Fatalf("observation trace %#v error = %v", trace, err)
		}
		if observationRepository.calls != 0 || notifier.count() != 0 {
			t.Fatalf("bounded trace rejection still reached persistence or the relay: %#v", trace)
		}

		eventRepository := &scriptedEntityEventRepository{results: []EntityEventRecordResult{{
			Outcome: EntityEventOutcomeAccepted, RecordedAt: observedAt,
		}}}
		eventNotifier := &recordingDeviceFactNotifier{}
		eventService := newTestService(eventRepository, nil, nil, Dependencies{DeviceFacts: eventNotifier})
		event := factScriptEntityEvent(observedAt)
		event.Trace = trace
		_, eventErr := eventService.RecordEntityEvent(
			context.Background(), "simulator", factScriptRuntimeID(), event, observedAt,
		)
		if !errors.Is(eventErr, ErrInvalidEntityEvent) || !errors.Is(eventErr, ErrInvalidDeviceFactTrace) {
			t.Fatalf("entity event trace %#v error = %v", trace, eventErr)
		}
		if eventRepository.calls != 0 || eventNotifier.count() != 0 {
			t.Fatalf("bounded trace rejection still reached persistence or the relay: %#v", trace)
		}
	}
}

// TestProjectObservationFailureCommitsNoFact proves the post-commit placement: a
// projection that fails, including a commit failure reported by persistence,
// wakes no relay because nothing committed.
func TestProjectObservationFailureCommitsNoFact(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	commitErr := errors.New("commit observation projection: SQLite write failed")
	repository := &scriptedObservationRepository{err: commitErr}
	notifier := &recordingDeviceFactNotifier{}
	service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: notifier})
	_, err := service.ProjectObservation(context.Background(), "simulator", factScriptRuntimeID(), Observation{
		ID: factScriptObservationID(), EntityID: factScriptEntityID(), Value: Value(`true`),
		CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: observedAt,
	}, observedAt)
	if !errors.Is(err, commitErr) {
		t.Fatalf("projection error = %v", err)
	}
	if got := notifier.count(); got != 0 {
		t.Fatalf("notifications after failure = %d, want 0", got)
	}
}

// TestRecordEntityEventNotifiesOnlyForQueuedPendingFact pins the Entity Event
// side of the relay hint: only a first-seen accepted report carries a queued
// fact identity, so rejected, duplicate and identity-conflict outcomes and a
// failed recording all wake nobody.
func TestRecordEntityEventNotifiesOnlyForQueuedPendingFact(t *testing.T) {
	t.Parallel()
	recordedAt := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	emittedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	receivedAt := time.Date(2026, 9, 1, 12, 0, 0, 500_000_000, time.UTC)
	rejection := EntityEventRejectionUnsupportedEvent
	tests := []struct {
		name       string
		result     EntityEventRecordResult
		err        error
		wantNotify int
	}{
		{
			name: "accepted",
			result: EntityEventRecordResult{
				Outcome: EntityEventOutcomeAccepted, RecordedAt: recordedAt,
				PendingFactID: deviceFactIDPointer(),
			},
			wantNotify: 1,
		},
		{
			name: "accepted without queued fact",
			result: EntityEventRecordResult{
				Outcome: EntityEventOutcomeAccepted, RecordedAt: recordedAt,
			},
			wantNotify: 0,
		},
		{
			name: "rejected",
			result: EntityEventRecordResult{
				Outcome:    EntityEventOutcomeRejected,
				Rejection:  &rejection,
				RecordedAt: recordedAt,
			},
			wantNotify: 0,
		},
		{
			name:       "duplicate",
			result:     EntityEventRecordResult{Outcome: EntityEventOutcomeDuplicate},
			wantNotify: 0,
		},
		{
			name:       "identity conflict",
			result:     EntityEventRecordResult{Outcome: EntityEventOutcomeIdentityConflict},
			wantNotify: 0,
		},
		{
			name:       "recording failure",
			err:        errors.New("insert entity event: SQLite write failed"),
			wantNotify: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &scriptedEntityEventRepository{
				results: []EntityEventRecordResult{test.result}, err: test.err,
			}
			notifier := &recordingDeviceFactNotifier{}
			service := newTestService(repository, nil, nil, Dependencies{DeviceFacts: notifier})
			event := factScriptEntityEvent(emittedAt)
			_, recordErr := service.RecordEntityEvent(
				context.Background(), "simulator", factScriptRuntimeID(), event, receivedAt,
			)
			if !errors.Is(recordErr, test.err) {
				t.Fatalf("record error = %v, want %v", recordErr, test.err)
			}
			if got := notifier.count(); got != test.wantNotify {
				t.Fatalf("notifications = %d, want %d", got, test.wantNotify)
			}
		})
	}
}

// TestObservationSatisfactionNotifiesWaiterBeforeDeviceFactNotifier pins the
// ordering guarantee for the one transaction that both commits an Observation
// and satisfies a linked Command: the waiter is notified before the relay hint,
// because the hint is transport latency and Command completion is not.
func TestObservationSatisfactionNotifiesWaiterBeforeDeviceFactNotifier(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	result := ProjectionResult{
		Disposition: DispositionApplied,
		State:       &State{EntityID: commandTestEntityID, Value: Value(`true`)},
		SatisfiedCommand: &CommandResult{
			CommandID: commandTestID, Outcome: OutcomeObserved,
			ObservationID: new(commandTestObservationID), Value: new(Value(`true`)),
		},
		PendingFactID: deviceFactIDPointer(),
	}
	repository := &scriptedObservationRepository{result: result}
	var service *Service
	notifier := &recordingDeviceFactNotifier{}
	waiterNotifiedBeforeHint := false
	notifier.onNotify = func() {
		service.waiters.mutex.Lock()
		waiter := service.waiters.byID[commandTestID]
		service.waiters.mutex.Unlock()
		waiterNotifiedBeforeHint = len(waiter) == 1
	}
	dependencies := commandDependencies()
	dependencies.DeviceFacts = notifier
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
	if projected.PendingFactID == nil {
		t.Fatal("projection result carries no pending fact identity")
	}
	select {
	case delivered := <-waiter:
		if delivered.CommandID != commandTestID {
			t.Fatalf("waiter result = %#v", delivered)
		}
	default:
		t.Fatal("waiter was not notified")
	}
	if notifier.count() != 1 {
		t.Fatalf("notifications = %d, want 1", notifier.count())
	}
	if !waiterNotifiedBeforeHint {
		t.Fatal("the relay was woken before the Command waiter was notified")
	}
}

func deviceFactIDPointer() *DeviceFactID {
	factID := DeviceFactID("fct_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	return &factID
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
