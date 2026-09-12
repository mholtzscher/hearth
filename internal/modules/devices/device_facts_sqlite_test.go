package devices //nolint:testpackage // Tests verify post-commit fact evidence through real migrated SQLite.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestAcceptedObservationFactsMatchCommittedSQLiteState proves the Observation
// fact mirrors the committed row: one fact per first-seen applied or unchanged
// Observation, carrying the value SQLite stores and the wire correlation, and
// none for a duplicate or a rejected report even though both are still
// recorded in history.
func TestAcceptedObservationFactsMatchCommittedSQLiteState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := NewSQLiteRepository(database, catalog)
	sink := &recordingDeviceFactSink{}
	service := newTestService(repository, nil, catalog, Dependencies{
		DeviceFacts: sink,
		Now:         func() time.Time { return observedAt },
	})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	correlationID, err := NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	sourceUpdatedAt := observedAt.Add(-3 * time.Second)
	adapterReceivedAt := observedAt.Add(-2 * time.Second)

	applied := newFactObservation(t, entityID, `true`, correlationID, adapterReceivedAt, &sourceUpdatedAt)
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, applied, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied {
		t.Fatalf("first projection disposition = %q", result.Disposition)
	}
	facts := sink.observationFacts()
	if len(facts) != 1 {
		t.Fatalf("facts after applied observation = %#v", facts)
	}
	fact := facts[0]
	var committedValue string
	if queryErr := database.QueryRowContext(
		ctx, `SELECT value_json FROM entity_states WHERE entity_id = ?`, entityID,
	).Scan(&committedValue); queryErr != nil {
		t.Fatal(queryErr)
	}
	if fact.ObservationID != applied.ID || fact.EntityID != entityID ||
		fact.Disposition != DispositionApplied || string(fact.Value) != committedValue ||
		fact.CorrelationID != correlationID || !fact.AdapterReceivedAt.Equal(adapterReceivedAt) ||
		!fact.ObservedAt.Equal(observedAt) || fact.SourceUpdatedAt == nil ||
		!fact.SourceUpdatedAt.Equal(sourceUpdatedAt) {
		t.Fatalf("applied observation fact = %#v, committed value = %q", fact, committedValue)
	}

	if _, duplicateErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, applied, observedAt.Add(time.Second),
	); duplicateErr != nil {
		t.Fatal(duplicateErr)
	}
	if got := sink.observationFacts(); len(got) != 1 {
		t.Fatalf("a duplicate Observation published facts: %#v", got)
	}

	unchanged := newFactObservation(
		t, entityID, `true`, correlationID, adapterReceivedAt, &sourceUpdatedAt,
	)
	if _, unchangedErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, unchanged, observedAt.Add(2*time.Second),
	); unchangedErr != nil {
		t.Fatal(unchangedErr)
	}
	facts = sink.observationFacts()
	if len(facts) != 2 || facts[1].Disposition != DispositionUnchanged ||
		facts[1].ObservationID != unchanged.ID || string(facts[1].Value) != committedValue {
		t.Fatalf("facts after unchanged observation = %#v", facts)
	}

	rejected := newFactObservation(t, entityID, `1`, correlationID, adapterReceivedAt, nil)
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, observedAt.Add(3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionInvalidValue {
		t.Fatalf("rejected projection = %#v", result)
	}
	if got := sink.observationFacts(); len(got) != 2 {
		t.Fatalf("a rejected Observation published facts: %#v", got)
	}
	// Every attempt is still durable history; only the accepted ones are facts.
	var observationRows int
	if queryErr := database.QueryRowContext(
		ctx, `SELECT count(*) FROM observations WHERE entity_id = ?`, entityID,
	).Scan(&observationRows); queryErr != nil {
		t.Fatal(queryErr)
	}
	if observationRows != 3 {
		t.Fatalf("observation rows = %d, want 3", observationRows)
	}
}

// TestAcceptedEntityEventFactRecordedAtMatchesCommittedSQLiteRow proves the
// Entity Event fact reports the recorded_at SQLite committed, and that only a
// first-seen accepted report is a fact.
func TestAcceptedEntityEventFactRecordedAtMatchesCommittedSQLiteRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	recordedAt := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	receivedAt := time.Date(2026, 9, 1, 12, 0, 0, 500_000_000, time.UTC)
	repository := NewSQLiteRepository(database, catalog)
	sink := &recordingDeviceFactSink{}
	service := newTestService(repository, nil, catalog, Dependencies{
		DeviceFacts: sink,
		Now:         func() time.Time { return recordedAt },
	})
	entityID := registerEntityEventEntity(t, service)
	emittedAt := recordedAt.Add(-time.Minute)
	event := newEntityEvent(t, entityID, "single_press", emittedAt)

	result, err := service.RecordEntityEvent(ctx, entityEventTestAdapter, testRuntimeID, event, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeAccepted || !result.RecordedAt.Equal(recordedAt) {
		t.Fatalf("recording result = %#v", result)
	}
	facts := sink.entityEventFacts()
	if len(facts) != 1 {
		t.Fatalf("facts after accepted entity event = %#v", facts)
	}
	stored := readStoredEntityEvent(t, database, event.ID)
	committedRecordedAt, parseErr := parseTime(stored.recordedAt)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	fact := facts[0]
	if fact.EventID != event.ID || fact.EntityID != entityID || fact.Name != event.Name ||
		fact.CorrelationID != event.CorrelationID || !fact.ReportedAt.Equal(emittedAt) ||
		!fact.ReceivedAt.Equal(receivedAt) || !fact.RecordedAt.Equal(committedRecordedAt) {
		t.Fatalf("entity event fact = %#v, committed recorded_at = %s", fact, stored.recordedAt)
	}

	if _, duplicateErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, event, receivedAt.Add(time.Second),
	); duplicateErr != nil {
		t.Fatal(duplicateErr)
	}
	conflict := event
	conflict.Name = "double_press"
	if _, conflictErr := service.RecordEntityEvent(
		ctx,
		entityEventTestAdapter,
		testRuntimeID,
		conflict,
		receivedAt,
	); conflictErr != nil {
		t.Fatal(conflictErr)
	}
	rejected := newEntityEvent(t, entityID, "triple_press", emittedAt)
	rejectedResult, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, rejected, receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedResult.Outcome != EntityEventOutcomeRejected || rejectedResult.RecordedAt.IsZero() {
		t.Fatalf("rejected recording result = %#v", rejectedResult)
	}
	if got := sink.entityEventFacts(); len(got) != 1 {
		t.Fatalf("non-first-seen-accepted reports published facts: %#v", got)
	}
	if got := countEntityEvents(t, database); got != 2 {
		t.Fatalf("entity event rows = %d, want the accepted and rejected rows", got)
	}
}

// TestCommandFactsFollowEveryCommittedSQLiteTransition drives real SQLite
// lifecycle paths and proves facts are gated on durable transitions: every
// committed transition is published once, in durable order, and an
// Observation-driven satisfaction publishes no acceptance fact of its own.
//
//nolint:gocognit // The transition matrix is clearer as one real-SQLite test.
func TestCommandFactsFollowEveryCommittedSQLiteTransition(t *testing.T) {
	t.Parallel()
	// One shared dispatch failure that is neither a classified availability
	// failure nor a rejection, so the Command commits internal_failure.
	internalFailureErr := errors.New("dispatch command: connection closed")
	tests := []struct {
		name         string
		deadline     time.Duration
		configure    func(*Service, *SQLiteRepository, EntityID)
		send         func(*testing.T, *Service, CommandRequest) (CommandAcceptance, error)
		wantErr      error
		wantStatuses []CommandStatus
	}{
		{
			name:     "satisfied by linked observation",
			deadline: 5 * time.Second,
			send: func(t *testing.T, service *Service, request CommandRequest) (CommandAcceptance, error) {
				t.Helper()
				// The Observation commits satisfaction before acceptance, so
				// the acceptance write is a no-op and publishes nothing.
				if _, err := service.ProjectObservation(context.Background(), "simulator", testRuntimeID, Observation{
					ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
					CorrelationID: factScriptCorrelationID(), AdapterReceivedAt: time.Now().UTC(),
					RefreshForCommand: &request.ID,
				}, time.Now().UTC()); err != nil {
					return CommandAcceptance{}, err
				}
				return CommandAcceptance{Accepted: true}, nil
			},
			wantStatuses: []CommandStatus{
				CommandStatusRequested, CommandStatusSatisfied,
			},
		},
		{
			name:     "accepted then outcome timeout",
			deadline: 25 * time.Millisecond,
			send: func(
				*testing.T,
				*Service,
				CommandRequest,
			) (CommandAcceptance, error) {
				return CommandAcceptance{Accepted: true}, nil
			},
			wantErr: ErrOutcomeTimeout,
			wantStatuses: []CommandStatus{
				CommandStatusRequested, CommandStatusAccepted, CommandStatusOutcomeTimeout,
			},
		},
		{
			name:     "entity unavailable at dispatch",
			deadline: 5 * time.Second,
			send: func(
				*testing.T,
				*Service,
				CommandRequest,
			) (CommandAcceptance, error) {
				return CommandAcceptance{}, ErrEntityUnavailable
			},
			wantErr: ErrEntityUnavailable,
			wantStatuses: []CommandStatus{
				CommandStatusRequested, CommandStatusEntityUnavailable,
			},
		},
		{
			name:     "internal failure at dispatch",
			deadline: 5 * time.Second,
			send: func(
				*testing.T,
				*Service,
				CommandRequest,
			) (CommandAcceptance, error) {
				return CommandAcceptance{}, internalFailureErr
			},
			wantErr:      internalFailureErr,
			wantStatuses: []CommandStatus{CommandStatusRequested, CommandStatusInternalFailure},
		},
		{
			name:     "rejected by adapter",
			deadline: 5 * time.Second,
			send: func(
				*testing.T,
				*Service,
				CommandRequest,
			) (CommandAcceptance, error) {
				return CommandAcceptance{Accepted: false}, nil
			},
			wantErr: ErrUpstreamRejected,
			wantStatuses: []CommandStatus{
				CommandStatusRequested, CommandStatusRejected,
			},
		},
		{
			name:     "entity disabled at creation",
			deadline: 5 * time.Second,
			configure: func(service *Service, _ *SQLiteRepository, entityID EntityID) {
				if _, err := service.SetEntityEnabled(context.Background(), entityID, false); err != nil {
					t.Fatalf("disable entity: %v", err)
				}
			},
			send: func(*testing.T, *Service, CommandRequest) (CommandAcceptance, error) {
				t.Fatal("a disabled entity Command was dispatched")
				return CommandAcceptance{}, nil
			},
			wantErr:      ErrEntityDisabled,
			wantStatuses: []CommandStatus{CommandStatusEntityDisabled},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			catalog := commandCatalog(t, test.deadline)
			repository := NewSQLiteRepository(database, catalog)
			sink := &recordingDeviceFactSink{}
			service := newTestService(repository, nil, catalog, Dependencies{DeviceFacts: sink})
			binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
			if err != nil {
				t.Fatal(err)
			}
			entityID := binding.Entities[0].EntityID
			if test.configure != nil {
				test.configure(service, repository, entityID)
			}
			service.sender = commandSenderFunc(
				func(
					_ context.Context,
					_ string,
					_ RuntimeID,
					request CommandRequest,
				) (CommandAcceptance, error) {
					return test.send(t, service, request)
				},
			)
			result, err := service.ExecuteCommand(ctx, CommandInput{
				ID: commandTestID, CorrelationID: commandTestCorrelationID, EntityID: entityID,
				OperationName: OperationNameSet, Parameters: CommandParameters(`{"value":true}`),
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("execution = %#v, %v, want %v", result, err, test.wantErr)
			}
			facts := sink.commandFacts()
			statuses := make([]CommandStatus, len(facts))
			for index, fact := range facts {
				statuses[index] = fact.Record.Status
			}
			if !equalCommandStatuses(statuses, test.wantStatuses) {
				t.Fatalf("published statuses = %v, want %v", statuses, test.wantStatuses)
			}
			stored, err := repository.GetCommand(ctx, commandTestID)
			if err != nil {
				t.Fatal(err)
			}
			if last := facts[len(facts)-1].Record; last.Status != stored.Status ||
				last.CompletedAt == nil || stored.CompletedAt == nil ||
				!last.CompletedAt.Equal(*stored.CompletedAt) ||
				!sameFailureCode(last.FailureCode, stored.FailureCode) ||
				last.OutcomeObservationID != nil && stored.OutcomeObservationID == nil {
				t.Fatalf("last fact = %#v, committed command = %#v", last, stored)
			}
		})
	}
}

// TestCommandFactsIncludeTheDispatchedTerminalTransition pins the fact for the
// one terminal status that neither an Observation nor a classified failure
// produces: an accepted dispatch effect commits `dispatched` and publishes
// exactly that transition once, in durable order, carrying completed_at with no
// failure or outcome evidence.
func TestCommandFactsIncludeTheDispatchedTerminalTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	sink := &recordingDeviceFactSink{}
	service := newTestService(repository, nil, catalog, Dependencies{DeviceFacts: sink})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = EntityTypeEnumactionV1
	registration.Entities[0].Support = EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	service.sender = commandSenderFunc(
		func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
			return CommandAcceptance{Accepted: true}, nil
		},
	)

	result, err := service.ExecuteCommand(ctx, CommandInput{
		ID: commandTestID, CorrelationID: commandTestCorrelationID, EntityID: entityID,
		OperationName: "trigger", Parameters: CommandParameters(`{"name":"blink"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeDispatched || result.CommandID != commandTestID {
		t.Fatalf("dispatched result = %#v", result)
	}

	facts := sink.commandFacts()
	statuses := make([]CommandStatus, len(facts))
	for index, fact := range facts {
		statuses[index] = fact.Record.Status
	}
	want := []CommandStatus{CommandStatusRequested, CommandStatusAccepted, CommandStatusDispatched}
	if !equalCommandStatuses(statuses, want) {
		t.Fatalf("published statuses = %v, want %v", statuses, want)
	}
	stored, err := repository.GetCommand(ctx, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	last := facts[len(facts)-1].Record
	if last.Status != CommandStatusDispatched || last.CompletedAt == nil || stored.CompletedAt == nil ||
		!last.CompletedAt.Equal(*stored.CompletedAt) || last.FailureCode != nil ||
		stored.FailureCode != nil || last.OutcomeObservationID != nil ||
		stored.Status != CommandStatusDispatched {
		t.Fatalf("dispatched fact = %#v, committed command = %#v", last, stored)
	}
}

func newFactObservation(
	t *testing.T,
	entityID EntityID,
	value string,
	correlationID CorrelationID,
	adapterReceivedAt time.Time,
	sourceUpdatedAt *time.Time,
) Observation {
	t.Helper()
	id, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return Observation{
		ID: id, EntityID: entityID, Value: Value(value), CorrelationID: correlationID,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: copyTimePointer(sourceUpdatedAt),
	}
}

func equalCommandStatuses(got, want []CommandStatus) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// sameFailureCode compares two optional failure codes by value.
func sameFailureCode(got, want *CommandFailureCode) bool {
	switch {
	case got == nil && want == nil:
		return true
	case got == nil || want == nil:
		return false
	default:
		return *got == *want
	}
}
