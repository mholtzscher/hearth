package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	factSQLiteTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	factSQLiteTracestate  = "hearth=simulator,device=b0"
)

// TestAcceptedObservationCommitsExactlyOnePendingDeviceFact proves the
// Observation half of the eligibility contract against real SQLite: every
// first-seen applied or unchanged Observation commits exactly one durable
// pending fact carrying the committed normalized value and Core timestamps,
// while duplicate and rejected reports commit none even though all three remain
// recorded history.
//
//nolint:gocognit,gocyclo,cyclop // One ordered projection sequence proves queuing, exclusion and history together.
func TestAcceptedObservationCommitsExactlyOnePendingDeviceFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	coreCommitTime := time.Date(2026, 9, 1, 12, 0, 2, 0, time.UTC)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	notifier := &recordingDeviceFactNotifier{}
	service := newTestService(repository, nil, catalog, devices.Dependencies{
		DeviceFacts: notifier,
		Now:         func() time.Time { return coreCommitTime },
	})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	sourceUpdatedAt := observedAt.Add(-3 * time.Second)
	adapterReceivedAt := observedAt.Add(-2 * time.Second)
	trace := devices.DeviceFactTraceContext{Traceparent: factSQLiteTraceparent, Tracestate: factSQLiteTracestate}

	applied := newFactObservationWithTrace(
		t, entityID, `true`, correlationID, adapterReceivedAt, &sourceUpdatedAt, trace,
	)
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, applied, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionApplied || result.PendingFactID == nil {
		t.Fatalf("first projection result = %#v", result)
	}
	committedValue := readCommittedStateValue(t, database, entityID)
	pending := listPendingDeviceFacts(t, repository)
	if len(pending) != 1 {
		t.Fatalf("pending facts after applied observation = %#v", pending)
	}
	fact := requireObservationFact(t, pending[0])
	if fact.ID != *result.PendingFactID || fact.ObservationID != applied.ID || fact.EntityID != entityID ||
		fact.Disposition != devices.DispositionApplied || string(fact.Value) != committedValue ||
		fact.CorrelationID != correlationID ||
		!fact.AdapterReceivedAt.Equal(adapterReceivedAt) ||
		fact.SourceUpdatedAt == nil || !fact.SourceUpdatedAt.Equal(sourceUpdatedAt) ||
		!fact.ObservedAt.Equal(observedAt) || !fact.CreatedAt.Equal(coreCommitTime) ||
		fact.Trace != trace {
		t.Fatalf("applied observation fact = %#v, committed value = %q", fact, committedValue)
	}
	if notifier.count() != 1 {
		t.Fatalf("notifications after applied observation = %d, want 1", notifier.count())
	}

	duplicate, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, applied, observedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Disposition != devices.DispositionDuplicate || duplicate.PendingFactID != nil {
		t.Fatalf("duplicate projection result = %#v", duplicate)
	}
	if got := countPendingDeviceFacts(t, database); got != 1 || notifier.count() != 1 {
		t.Fatalf("a duplicate Observation queued a fact: rows = %d, notifications = %d", got, notifier.count())
	}

	unchangedObservation := newFactObservationWithTrace(
		t, entityID, `true`, correlationID, adapterReceivedAt, &sourceUpdatedAt, trace,
	)
	unchanged, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, unchangedObservation, observedAt.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Disposition != devices.DispositionUnchanged || unchanged.PendingFactID == nil {
		t.Fatalf("unchanged projection result = %#v", unchanged)
	}
	pending = listPendingDeviceFacts(t, repository)
	if len(pending) != 2 {
		t.Fatalf("pending facts after unchanged observation = %#v", pending)
	}
	unchangedFact := requireObservationFact(t, pending[1])
	if unchangedFact.Disposition != devices.DispositionUnchanged || unchangedFact.ID != *unchanged.PendingFactID ||
		unchangedFact.ObservationID != unchangedObservation.ID ||
		string(unchangedFact.Value) != committedValue ||
		!unchangedFact.ObservedAt.Equal(observedAt.Add(2*time.Second)) ||
		!unchangedFact.CreatedAt.Equal(coreCommitTime) {
		t.Fatalf("unchanged observation fact = %#v", unchangedFact)
	}

	rejected := newFactObservationWithTrace(t, entityID, `1`, correlationID, adapterReceivedAt, nil, trace)
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, observedAt.Add(3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionInvalidValue || result.PendingFactID != nil {
		t.Fatalf("rejected projection result = %#v", result)
	}
	if got := countPendingDeviceFacts(t, database); got != 2 {
		t.Fatalf("a rejected Observation queued a fact: rows = %d, want 2", got)
	}
	if notifier.count() != 2 {
		t.Fatalf("notifications = %d, want one per queued fact", notifier.count())
	}
	// Every attempt is still durable history; only the accepted ones are facts.
	if got := countTable(t, database, "observations"); got != 3 {
		t.Fatalf("observation rows = %d, want 3", got)
	}
}

// TestAcceptedEntityEventCommitsExactlyOnePendingDeviceFact proves the Entity
// Event half of the eligibility contract against real SQLite: only a first-seen
// accepted report commits one durable pending fact, and duplicate,
// identity-conflict and rejected reports commit none.
//
//nolint:gocognit // One recording sequence proves acceptance, exclusion and history together.
func TestAcceptedEntityEventCommitsExactlyOnePendingDeviceFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	recordedAt := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	receivedAt := time.Date(2026, 9, 1, 12, 0, 0, 500_000_000, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	notifier := &recordingDeviceFactNotifier{}
	service := newTestService(repository, nil, catalog, devices.Dependencies{
		DeviceFacts: notifier,
		Now:         func() time.Time { return recordedAt },
	})
	entityID := registerEntityEventEntity(t, service)
	emittedAt := recordedAt.Add(-time.Minute)
	event := newEntityEvent(t, entityID, "single_press", emittedAt)
	event.Trace = devices.DeviceFactTraceContext{Traceparent: factSQLiteTraceparent, Tracestate: factSQLiteTracestate}

	result, err := service.RecordEntityEvent(ctx, entityEventTestAdapter, testRuntimeID, event, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != devices.EntityEventOutcomeAccepted || result.PendingFactID == nil {
		t.Fatalf("recording result = %#v", result)
	}
	stored := readStoredEntityEvent(t, database, event.ID)
	committedRecordedAt, parseErr := parseTime(stored.recordedAt)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	pending := listPendingDeviceFacts(t, repository)
	if len(pending) != 1 {
		t.Fatalf("pending facts after accepted entity event = %#v", pending)
	}
	fact := requireEntityEventFact(t, pending[0])
	if fact.ID != *result.PendingFactID || fact.EventID != event.ID || fact.EntityID != entityID ||
		fact.Name != event.Name || fact.CorrelationID != event.CorrelationID ||
		!fact.ReportedAt.Equal(emittedAt) || !fact.ReceivedAt.Equal(receivedAt) ||
		!fact.RecordedAt.Equal(committedRecordedAt) || !fact.CreatedAt.Equal(recordedAt) ||
		fact.Trace != event.Trace {
		t.Fatalf("entity event fact = %#v, committed recorded_at = %s", fact, stored.recordedAt)
	}
	if notifier.count() != 1 {
		t.Fatalf("notifications after accepted entity event = %d, want 1", notifier.count())
	}

	duplicate, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, event, receivedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Outcome != devices.EntityEventOutcomeDuplicate || duplicate.PendingFactID != nil {
		t.Fatalf("duplicate result = %#v", duplicate)
	}
	conflict := event
	conflict.Name = "double_press"
	conflicted, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, conflict, receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Outcome != devices.EntityEventOutcomeIdentityConflict || conflicted.PendingFactID != nil {
		t.Fatalf("identity conflict result = %#v", conflicted)
	}
	rejected := newEntityEvent(t, entityID, "triple_press", emittedAt)
	rejectedResult, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, rejected, receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedResult.Outcome != devices.EntityEventOutcomeRejected || rejectedResult.PendingFactID != nil {
		t.Fatalf("rejected result = %#v", rejectedResult)
	}
	if got := countPendingDeviceFacts(t, database); got != 1 {
		t.Fatalf("non-first-seen-accepted reports queued facts: rows = %d, want 1", got)
	}
	if notifier.count() != 1 {
		t.Fatalf("notifications = %d, want only the accepted report's", notifier.count())
	}
	if got := countEntityEvents(t, database); got != 2 {
		t.Fatalf("entity event rows = %d, want the accepted and rejected rows", got)
	}
	if got := countTable(t, database, "commands"); got != 0 {
		t.Fatalf("command rows = %d, want 0 for fact-producing evidence", got)
	}
}

// TestPendingDeviceFactsKeepOneEnqueueOrderAcrossFamilies proves the single
// outbox table orders both families together, that a bounded read returns the
// oldest pending facts first, that deleting one published fact leaves the rest
// in order, and that a repeated or unknown delete is not an error.
//
//nolint:gocognit,gocyclo,cyclop // One interleaved enqueue sequence proves shared ordering and deletion together.
func TestPendingDeviceFactsKeepOneEnqueueOrderAcrossFamilies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	recordedAt := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	notifier := &recordingDeviceFactNotifier{}
	service := newTestService(repository, nil, catalog, devices.Dependencies{
		DeviceFacts: notifier,
		Now:         func() time.Time { return recordedAt },
	})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID := binding.Entities[0].EntityID
	eventEntityID := registerEntityEventEntity(t, service)

	traces := []devices.DeviceFactTraceContext{
		{Traceparent: factSQLiteTraceparent},
		{Traceparent: factSQLiteTraceparent, Tracestate: factSQLiteTracestate},
		{},
	}
	first := newFactObservationWithTrace(
		t, powerEntityID, `true`, commandTestCorrelationID, recordedAt, nil, traces[0],
	)
	if _, projectErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, first, recordedAt,
	); projectErr != nil {
		t.Fatal(projectErr)
	}
	event := newEntityEvent(t, eventEntityID, "single_press", recordedAt.Add(-time.Minute))
	event.Trace = traces[1]
	if _, recordErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, testRuntimeID, event, recordedAt,
	); recordErr != nil {
		t.Fatal(recordErr)
	}
	second := newFactObservationWithTrace(
		t, powerEntityID, `false`, commandTestCorrelationID, recordedAt, nil, traces[2],
	)
	if _, projectErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, second, recordedAt.Add(time.Second),
	); projectErr != nil {
		t.Fatal(projectErr)
	}

	facts := listPendingDeviceFacts(t, repository)
	if len(facts) != 3 {
		t.Fatalf("pending facts = %#v, want one row per accepted fact", facts)
	}
	for index := 1; index < len(facts); index++ {
		if facts[index].Sequence <= facts[index-1].Sequence {
			t.Fatalf("pending order is not increasing: %#v", facts)
		}
	}
	firstObservation := requireObservationFact(t, facts[0])
	middleEvent := requireEntityEventFact(t, facts[1])
	secondObservation := requireObservationFact(t, facts[2])
	if firstObservation.ObservationID != first.ID || string(firstObservation.Value) != "true" ||
		firstObservation.Trace != traces[0] {
		t.Fatalf("oldest pending fact = %#v", firstObservation)
	}
	if middleEvent.EventID != event.ID || middleEvent.Trace != traces[1] {
		t.Fatalf("middle pending fact = %#v", middleEvent)
	}
	if secondObservation.ObservationID != second.ID || string(secondObservation.Value) != "false" ||
		secondObservation.Trace != traces[2] {
		t.Fatalf("newest pending fact = %#v", secondObservation)
	}

	bounded, err := repository.ListPendingDeviceFacts(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 2 || bounded[0].Sequence != facts[0].Sequence ||
		bounded[1].Sequence != facts[1].Sequence {
		t.Fatalf("bounded pending read = %#v", bounded)
	}
	oldest, err := repository.ListPendingDeviceFacts(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldest) != 1 || oldest[0].Sequence != facts[0].Sequence {
		t.Fatalf("single pending read = %#v", oldest)
	}
	if _, limitErr := repository.ListPendingDeviceFacts(ctx, 0); !errors.Is(
		limitErr, devices.ErrInvalidDeviceFactLimit,
	) {
		t.Fatalf("zero-limit read error = %v", limitErr)
	}

	published := firstObservation.ID
	if deleteErr := repository.DeleteDeviceFact(ctx, published); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	remaining := listPendingDeviceFacts(t, repository)
	if len(remaining) != 2 || remaining[0].Sequence != facts[1].Sequence ||
		remaining[1].Sequence != facts[2].Sequence {
		t.Fatalf("pending facts after delete = %#v", remaining)
	}
	if repeatErr := repository.DeleteDeviceFact(ctx, published); repeatErr != nil {
		t.Fatalf("repeated delete = %v", repeatErr)
	}
	if unknownErr := repository.DeleteDeviceFact(ctx, devices.DeviceFactID("not-a-fact")); unknownErr == nil {
		t.Fatal("noncanonical fact ID was deleted")
	}
	if got := countPendingDeviceFacts(t, database); got != 2 {
		t.Fatalf("pending rows = %d, want 2", got)
	}
}

// TestDeviceFactOutboxRejectsMixedFamilyRows proves the storage-level CHECK
// constraints, not just the repository path: one row holds exactly one tagged
// family, so a mixed or unknown family can never be written by any caller.
//
//nolint:paralleltest,tparallel // Subtests share one migrated database and its insert order.
func TestDeviceFactOutboxRejectsMixedFamilyRows(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	statement := `INSERT INTO device_facts_outbox (
        fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
        traceparent, tracestate, value_json, adapter_received_at, observed_at,
        reported_at, received_at, recorded_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	observationRow := outboxInsert{
		factID: "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		family: "observation", entityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		variant: "applied", sourceID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		correlationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		createdAt:     "2026-09-01T12:00:00Z",
		valueJSON:     `true`, adapterReceivedAt: "2026-09-01T12:00:00Z", observedAt: "2026-09-01T12:00:00Z",
	}
	tests := []struct {
		name         string
		row          outboxInsert
		wantAccepted bool
	}{
		{name: "observation row", row: observationRow, wantAccepted: true},
		{
			name: "observation row with event times",
			row: mutateOutboxInsert(observationRow, func(row *outboxInsert) {
				row.reportedAt = "2026-09-01T12:00:00Z"
			}),
		},
		{
			name: "observation row without committed value",
			row:  mutateOutboxInsert(observationRow, func(row *outboxInsert) { row.valueJSON = nil }),
		},
		{
			name: "observation row with unknown disposition",
			row:  mutateOutboxInsert(observationRow, func(row *outboxInsert) { row.variant = "rejected" }),
		},
		{
			name: "observation row with event source",
			row: mutateOutboxInsert(observationRow, func(row *outboxInsert) {
				row.sourceID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
			}),
		},
		{
			name: "observation row with oversized traceparent",
			row: mutateOutboxInsert(observationRow, func(row *outboxInsert) {
				row.traceparent = strings.Repeat("a", 129)
			}),
		},
		{
			name: "observation row with non-printable tracestate",
			row: mutateOutboxInsert(observationRow, func(row *outboxInsert) {
				row.tracestate = "vendor=value\n"
			}),
		},
		{
			name: "unknown family",
			row:  mutateOutboxInsert(observationRow, func(row *outboxInsert) { row.family = "command" }),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := database.ExecContext(
				context.Background(), statement, test.row.insertArguments()...,
			)
			if test.wantAccepted && err != nil {
				t.Fatalf("valid row rejected: %v", err)
			}
			if !test.wantAccepted && err == nil {
				t.Fatal("invalid row was accepted")
			}
		})
	}
	if got := countPendingDeviceFacts(t, database); got != 1 {
		t.Fatalf("pending rows = %d, want only the one declared-valid row", got)
	}
}

// outboxInsert holds one raw outbox statement's variable columns in statement
// order, so a case states only the field it makes invalid.
type outboxInsert struct {
	factID            string
	family            string
	entityID          string
	variant           string
	sourceID          string
	correlationID     string
	createdAt         string
	traceparent       string
	tracestate        string
	valueJSON         any
	adapterReceivedAt any
	observedAt        any
	reportedAt        any
	receivedAt        any
	recordedAt        any
}

func mutateOutboxInsert(row outboxInsert, mutate func(*outboxInsert)) outboxInsert {
	mutate(&row)
	return row
}

func (row outboxInsert) insertArguments() []any {
	return []any{
		row.factID, row.family, row.entityID, row.variant, row.sourceID,
		row.correlationID, row.createdAt, row.traceparent, row.tracestate,
		row.valueJSON, row.adapterReceivedAt, row.observedAt,
		row.reportedAt, row.receivedAt, row.recordedAt,
	}
}

// TestDeviceFactIdentityFailureRollsBackEvidence proves "no eligible source can
// commit without its fact": a mint failure or a noncanonical minted identity
// fails the whole devices transaction, so the accepted Observation or Entity
// Event, its State and its pending fact are all absent and redelivery can retry
// them together.
//
//nolint:gocognit // One scenario matrix proves both families roll back and report the cause.
func TestDeviceFactIdentityFailureRollsBackEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mintErr := errors.New("entropy unavailable")
	tests := []struct {
		name      string
		generator func() (devices.DeviceFactID, error)
	}{
		{
			name:      "mint failure",
			generator: func() (devices.DeviceFactID, error) { return "", mintErr },
		},
		{
			name:      "noncanonical identity",
			generator: func() (devices.DeviceFactID, error) { return devices.DeviceFactID("not-a-fact-id"), nil },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			catalog := firstLightCatalog(t)
			repository := newDeviceRepository(database, catalog, test.generator)
			notifier := &recordingDeviceFactNotifier{}
			service := newTestService(repository, nil, catalog, devices.Dependencies{
				DeviceFacts: notifier,
				Now:         func() time.Time { return observedAt },
			})
			binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
			if err != nil {
				t.Fatal(err)
			}
			entityID := binding.Entities[0].EntityID
			observation := newFactObservationWithTrace(
				t, entityID, `true`, commandTestCorrelationID, observedAt, nil, devices.DeviceFactTraceContext{},
			)
			if _, projectErr := service.ProjectObservation(
				ctx, "simulator", testRuntimeID, observation, observedAt,
			); projectErr == nil {
				t.Fatal("eligible Observation committed without its pending fact")
			}
			if got := countTable(t, database, "observations"); got != 0 {
				t.Fatalf("observation rows = %d, want the failed transaction rolled back", got)
			}
			if got := countTable(t, database, "entity_states"); got != 0 {
				t.Fatalf("entity state rows = %d, want the failed transaction rolled back", got)
			}

			eventEntityID := registerEntityEventEntity(t, service)
			event := newEntityEvent(t, eventEntityID, "single_press", observedAt.Add(-time.Minute))
			if _, eventErr := service.RecordEntityEvent(
				ctx, entityEventTestAdapter, testRuntimeID, event, observedAt,
			); eventErr == nil {
				t.Fatal("accepted Entity Event committed without its pending fact")
			}
			if got := countTable(t, database, "entity_events"); got != 0 {
				t.Fatalf("entity event rows = %d, want the failed transaction rolled back", got)
			}
			if got := countPendingDeviceFacts(t, database); got != 0 {
				t.Fatalf("pending rows = %d, want none", got)
			}
			if notifier.count() != 0 {
				t.Fatalf("notifications = %d, want none", notifier.count())
			}
		})
	}

	t.Run("mint failure is reported", func(t *testing.T) {
		t.Parallel()
		database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
		catalog := firstLightCatalog(t)
		repository := newDeviceRepository(database, catalog, func() (devices.DeviceFactID, error) {
			return "", mintErr
		})
		service := newTestService(repository, nil, catalog, devices.Dependencies{
			Now: func() time.Time { return observedAt },
		})
		binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
		if err != nil {
			t.Fatal(err)
		}
		observation := newFactObservationWithTrace(
			t, binding.Entities[0].EntityID, `true`, commandTestCorrelationID, observedAt, nil,
			devices.DeviceFactTraceContext{},
		)
		_, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, observation, observedAt)
		if !errors.Is(err, mintErr) {
			t.Fatalf("projection error = %v, want %v", err, mintErr)
		}
	})
}

// TestCommandLifecycleQueuesNoDeviceFact pins the other half of the eligibility
// boundary: a Command lifecycle transition publishes no fact, so a terminal
// command commits its own history and leaves the outbox empty and the relay
// unwoken.
func TestCommandLifecycleQueuesNoDeviceFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	notifier := &recordingDeviceFactNotifier{}
	// Rejecting dispatch is what makes the Command terminal, so the sender seam
	// is wired before the Service is built.
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, devices.RuntimeID, devices.CommandRequest,
	) (devices.CommandAcceptance, error) {
		return devices.CommandAcceptance{Accepted: false}, nil
	}), catalog, devices.Dependencies{DeviceFacts: notifier})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecuteCommand(ctx, devices.CommandInput{
		EntityID: binding.Entities[0].EntityID, OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":true}`),
	})
	if !errors.Is(err, devices.ErrUpstreamRejected) {
		t.Fatalf("command execution error = %v", err)
	}
	if got := countTable(t, database, "commands"); got != 1 {
		t.Fatalf("command rows = %d, want the terminal record", got)
	}
	if got := countPendingDeviceFacts(t, database); got != 0 {
		t.Fatalf("pending rows = %d, want none for a Command lifecycle transition", got)
	}
	if notifier.count() != 0 {
		t.Fatalf("notifications = %d, want none for a Command lifecycle transition", notifier.count())
	}
}

// TestListPendingDeviceFactsFaultsCorruptRowAndRetriesReadFailure proves the two
// outbox error classes the relay depends on: a durable row whose stored commit
// time cannot be decoded is the permanent ErrInvalidDeviceFactRow class carrying
// the stored identity, while a storage failure of the read itself is an ordinary
// retryable error that never matches that class. Neither class discards a row.
func TestListPendingDeviceFactsFaultsCorruptRowAndRetriesReadFailure(t *testing.T) {
	t.Parallel()
	const insertStatement = `INSERT INTO device_facts_outbox (
        fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
        traceparent, tracestate, value_json, adapter_received_at, observed_at
    ) VALUES (?, 'observation', ?, 'applied', ?, ?, ?, '', '', 'true', ?, ?)`
	const factID = "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab"

	t.Run("corrupt row is permanent and preserved", func(t *testing.T) {
		t.Parallel()
		database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
		repository := NewDeviceRepository(database, nil)
		observedAt := "2026-09-01T12:00:00Z"
		if _, err := database.ExecContext(
			context.Background(), insertStatement,
			factID,
			"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			observedAt, observedAt, observedAt,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(
			context.Background(),
			`UPDATE device_facts_outbox SET created_at = 'not-a-timestamp' WHERE fact_id = ?`, factID,
		); err != nil {
			t.Fatal(err)
		}

		_, listErr := repository.ListPendingDeviceFacts(context.Background(), 10)
		if !errors.Is(listErr, devices.ErrInvalidDeviceFactRow) {
			t.Fatalf("corrupt row error = %v, want %v", listErr, devices.ErrInvalidDeviceFactRow)
		}
		var rowErr *devices.DeviceFactRowError
		if !errors.As(listErr, &rowErr) || rowErr.FactID != factID || rowErr.Cause == nil {
			t.Fatalf("corrupt row error = %#v, want the stored identity and a cause", rowErr)
		}
		if got := countPendingDeviceFacts(t, database); got != 1 {
			t.Fatalf("pending rows after a corrupt read = %d, want the row preserved", got)
		}
	})

	t.Run("storage read failure is transient", func(t *testing.T) {
		t.Parallel()
		database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
		repository := NewDeviceRepository(database, nil)
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		_, listErr := repository.ListPendingDeviceFacts(context.Background(), 10)
		if listErr == nil {
			t.Fatal("a closed outbox reported an empty pending set")
		}
		if errors.Is(listErr, devices.ErrInvalidDeviceFactRow) {
			t.Fatalf("transient read failure %v matched the permanent invalid-row class", listErr)
		}
	})
}

// TestListPendingDeviceFactsReturnsThePrefixOlderThanACorruptRow pins the outbox
// contract the relay's poison handling depends on: decoding stops at the first
// undecodable row and the call returns the valid older rows together with the
// permanent ErrInvalidDeviceFactRow error, so the relay can publish them. The
// corrupt row and every row behind it are neither decoded nor deleted, because
// ADR 0020 preserves the poison row and blocks only the facts queued behind it.
func TestListPendingDeviceFactsReturnsThePrefixOlderThanACorruptRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const insertStatement = `INSERT INTO device_facts_outbox (
        fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
        traceparent, tracestate, value_json, adapter_received_at, observed_at
    ) VALUES (?, 'observation', ?, 'applied', ?, ?, ?, '', '', 'true', ?, ?)`
	const (
		entityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		correlationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		committedAt   = "2026-09-01T12:00:00Z"
	)
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, nil)
	insertRow := func(factID string, sourceID string) {
		t.Helper()
		if _, err := database.ExecContext(
			ctx, insertStatement, factID, entityID, sourceID, correlationID,
			committedAt, committedAt, committedAt,
		); err != nil {
			t.Fatal(err)
		}
	}
	oldest := "fct_01890f47-7a6b-7c4d-8e9f-0123456789a1"
	poison := "fct_01890f47-7a6b-7c4d-8e9f-0123456789a2"
	behindPoison := "fct_01890f47-7a6b-7c4d-8e9f-0123456789a3"
	insertRow(oldest, "obs_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	insertRow(poison, "obs_01890f47-7a6b-7c4d-8e9f-0123456789a2")
	insertRow(behindPoison, "obs_01890f47-7a6b-7c4d-8e9f-0123456789a3")
	if _, err := database.ExecContext(
		ctx, `UPDATE device_facts_outbox SET created_at = 'not-a-timestamp' WHERE fact_id = ?`, poison,
	); err != nil {
		t.Fatal(err)
	}

	facts, listErr := repository.ListPendingDeviceFacts(ctx, 10)
	if !errors.Is(listErr, devices.ErrInvalidDeviceFactRow) {
		t.Fatalf("list error = %v, want %v", listErr, devices.ErrInvalidDeviceFactRow)
	}
	var rowErr *devices.DeviceFactRowError
	if !errors.As(listErr, &rowErr) || rowErr.FactID != poison {
		t.Fatalf("list error = %#v, want the corrupt row %s", rowErr, poison)
	}
	if len(facts) != 1 {
		t.Fatalf("decoded prefix = %#v, want only the row older than the corrupt row", facts)
	}
	decoded := requireObservationFact(t, facts[0])
	if string(decoded.ID) != oldest || facts[0].Sequence != 1 {
		t.Fatalf("decoded prefix = %#v, want the oldest row %s first", facts[0], oldest)
	}
	// Only the poison row and the rows behind it remain durable; the prefix is
	// returned for the relay to publish, not deleted by the read.
	if got := countPendingDeviceFacts(t, database); got != 3 {
		t.Fatalf("pending rows = %d, want every row preserved", got)
	}
}

func newFactObservationWithTrace(
	t *testing.T,
	entityID devices.EntityID,
	value string,
	correlationID devices.CorrelationID,
	adapterReceivedAt time.Time,
	sourceUpdatedAt *time.Time,
	trace devices.DeviceFactTraceContext,
) devices.Observation {
	t.Helper()
	id, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.Observation{
		ID: id, EntityID: entityID, Value: devices.Value(value), CorrelationID: correlationID,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: copyTimePointer(sourceUpdatedAt),
		Trace: trace,
	}
}

func listPendingDeviceFacts(t *testing.T, repository *DeviceRepository) []devices.PendingDeviceFact {
	t.Helper()
	facts, err := repository.ListPendingDeviceFacts(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func requireObservationFact(t *testing.T, pending devices.PendingDeviceFact) devices.ObservationFact {
	t.Helper()
	fact, ok := pending.Fact.(devices.ObservationFact)
	if !ok {
		t.Fatalf("pending fact %#v is not an ObservationFact", pending.Fact)
	}
	return fact
}

func requireEntityEventFact(t *testing.T, pending devices.PendingDeviceFact) devices.EntityEventFact {
	t.Helper()
	fact, ok := pending.Fact.(devices.EntityEventFact)
	if !ok {
		t.Fatalf("pending fact %#v is not an EntityEventFact", pending.Fact)
	}
	return fact
}

func readCommittedStateValue(t *testing.T, database *sql.DB, entityID devices.EntityID) string {
	t.Helper()
	var value string
	if err := database.QueryRowContext(
		context.Background(), `SELECT value_json FROM entity_states WHERE entity_id = ?`, entityID,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func countPendingDeviceFacts(t *testing.T, database *sql.DB) int {
	t.Helper()
	return countTable(t, database, "device_facts_outbox")
}

func countTable(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(), "SELECT count(*) FROM "+table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
