package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	entityEventTestRuntimeID = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	entityEventTestAdapter   = "simulator"
)

func entityEventRegistration() Registration {
	externalID := "sim-buttons"
	return Registration{
		BindingKey: "office-buttons",
		Device: DeviceDescriptor{
			ExternalID: &externalID, Name: "Office buttons", Kind: DeviceKindSensor,
		},
		Entities: []EntityDescriptor{{
			Key: "buttons", ExternalID: "sim.buttons", Name: "Buttons",
			TypeID: EntityTypeEnumeventV1,
			Support: EntitySupport(
				`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
			),
		}},
	}
}

func newEntityEventTestService(
	t *testing.T,
	database *sql.DB,
	now *time.Time,
) (*Service, *SQLiteRepository) {
	t.Helper()
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{
		Now: func() time.Time { return *now },
	})
	return service, repository
}

func registerEntityEventEntity(t *testing.T, service *Service) EntityID {
	t.Helper()
	binding, err := service.Register(
		context.Background(), entityEventTestAdapter, entityEventTestRuntimeID, entityEventRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return binding.Entities[0].EntityID
}

func newTestEntityID(t *testing.T) EntityID {
	t.Helper()
	id, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func parseTestRuntimeID(t *testing.T, value string) RuntimeID {
	t.Helper()
	id, err := ParseRuntimeID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func newEntityEvent(t *testing.T, entityID EntityID, name string, emittedAt time.Time) EntityEvent {
	t.Helper()
	eventID, err := NewEntityEventID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	return EntityEvent{
		ID: eventID, EntityID: entityID, Name: EntityEventName(name),
		CorrelationID: correlationID, EmittedAt: emittedAt,
	}
}

type storedEntityEvent struct {
	receiveOrder  int64
	adapterID     string
	runtimeID     string
	entityID      string
	correlationID string
	name          string
	disposition   string
	rejection     *string
	fingerprint   []byte
	emittedAt     string
	receivedAt    string
	recordedAt    string
}

func readStoredEntityEvent(t *testing.T, database *sql.DB, eventID EntityEventID) storedEntityEvent {
	t.Helper()
	row, err := readStoredEntityEventErr(t, database, eventID)
	if err != nil {
		t.Fatalf("read stored entity event %s: %v", eventID, err)
	}
	return row
}

func countEntityEvents(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(), "SELECT count(*) FROM entity_events",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// This test protects first-seen identity semantics and fails if a distinct ID
// overwrites or reuses another report's row, if a duplicate or identity
// conflict rewrites the stored row (including its timestamps and disposition),
// or if either case adds a row.
func TestEntityEventRecordingSeparatesIDsAndPreservesFirstSeenRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)

	first := newEntityEvent(t, entityID, "single_press", now.Add(-2*time.Minute))
	result, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, first, now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeAccepted || result.Rejection != nil {
		t.Fatalf("first recording = %#v", result)
	}
	second := newEntityEvent(t, entityID, "single_press", now.Add(-3*time.Minute))
	if second.ID == first.ID {
		t.Fatal("distinct reports reused one event ID")
	}
	result, err = service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, second, now.Add(-30*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeAccepted {
		t.Fatalf("second recording = %#v", result)
	}
	if countEntityEvents(t, database) != 2 {
		t.Fatalf("row count after two IDs = %d, want 2", countEntityEvents(t, database))
	}
	firstRow := readStoredEntityEvent(t, database, first.ID)
	secondRow := readStoredEntityEvent(t, database, second.ID)
	if firstRow.receiveOrder >= secondRow.receiveOrder || firstRow.entityID != string(entityID) ||
		firstRow.name != "single_press" || firstRow.disposition != "accepted" ||
		firstRow.rejection != nil || len(firstRow.fingerprint) != 32 ||
		firstRow.adapterID != entityEventTestAdapter || firstRow.runtimeID != entityEventTestRuntimeID ||
		firstRow.correlationID != string(first.CorrelationID) {
		t.Fatalf("stored first row = %#v", firstRow)
	}

	// Identical immutable input is the same event, so the row is untouched.
	before := firstRow
	result, err = service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, first, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeDuplicate || result.Rejection != nil {
		t.Fatalf("duplicate recording = %#v", result)
	}
	if got := readStoredEntityEvent(t, database, first.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("duplicate rewrote the first row:\n got %#v\nwant %#v", got, before)
	}

	// Changed immutable input for a known ID is a conflict: still no rewrite.
	changed := first
	changed.Name = EntityEventName("double_press")
	result, err = service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, changed, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeIdentityConflict || result.Rejection != nil {
		t.Fatalf("identity conflict recording = %#v", result)
	}
	changed = first
	changed.EmittedAt = first.EmittedAt.Add(time.Second)
	result, err = service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, changed, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeIdentityConflict {
		t.Fatalf("changed emitted_at outcome = %#v", result)
	}
	if got := readStoredEntityEvent(t, database, first.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("identity conflict rewrote the first row:\n got %#v\nwant %#v", got, before)
	}
	if count := countEntityEvents(t, database); count != 2 {
		t.Fatalf("row count after duplicate and conflict = %d, want 2", count)
	}
}

// This test protects the persisted rejection precedence and fails if a
// rejection is misclassified, skipped, or not durable, and if Adapter health or
// Entity availability gate historical input.
//
//nolint:gocognit // One table keeps every rejection and precedence case under the same Entity fixture.
func TestEntityEventRecordingRejectionPrecedence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	service, repository := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)
	unknownEntityID := newTestEntityID(t)
	staleRuntimeID := parseTestRuntimeID(t, "run_01890f47-7a6b-7c4d-8e9f-0123456789af")
	homeAssistantRuntime := RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")

	// Health and availability are not gates: an unhealthy owner and an
	// unavailable Entity still record historical input. Availability reports
	// themselves need a healthy owner, so the healthy heartbeat and the
	// unavailable report come first.
	if _, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: entityEventTestAdapter, RuntimeID: entityEventTestRuntimeID,
		ExternalStatus: AdapterHealthHealthy, SourceObservedAt: now.Add(-2 * time.Second),
		ReceivedAt: now.Add(-time.Second), LeaseExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, AvailabilityBatchWrite{
		AdapterID: entityEventTestAdapter, RuntimeID: entityEventTestRuntimeID,
		Reports: []EntityAvailabilityReport{{
			EntityID: entityID, Status: EntityAvailabilityUnavailable,
			SourceObservedAt: now.Add(-time.Second), Reason: &HealthReason{Code: "hearth.upstream_offline"},
		}},
		ReportedAt: now,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: entityEventTestAdapter, RuntimeID: entityEventTestRuntimeID,
		ExternalStatus: AdapterHealthUnhealthy, Reason: &HealthReason{Code: "hearth.network_unreachable"},
		SourceObservedAt: now.Add(-time.Second), ReceivedAt: now, LeaseExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ungated := newEntityEvent(t, entityID, "single_press", now)
	result, recordErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, ungated, now,
	)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeAccepted || result.Rejection != nil {
		t.Fatalf("ungated recording = %#v", result)
	}

	// A disabled Entity rejects even a supported name.
	if _, err := service.SetEntityEnabled(ctx, entityID, false); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		adapterID  string
		runtimeID  RuntimeID
		entityID   EntityID
		eventName  string
		wantReason EntityEventRejection
	}{
		{
			"stale runtime", entityEventTestAdapter, staleRuntimeID, entityID,
			"single_press", EntityEventRejectionStaleRuntime,
		},
		{
			"stale runtime outranks unknown Entity", entityEventTestAdapter, staleRuntimeID,
			unknownEntityID, "not_supported", EntityEventRejectionStaleRuntime,
		},
		{
			"stale runtime outranks disabled and unsupported", entityEventTestAdapter, staleRuntimeID,
			entityID, "not_supported", EntityEventRejectionStaleRuntime,
		},
		{
			"unknown Entity", entityEventTestAdapter, entityEventTestRuntimeID, unknownEntityID,
			"single_press", EntityEventRejectionUnknownEntity,
		},
		{
			"wrong adapter", "homeassistant", homeAssistantRuntime, entityID,
			"not_supported", EntityEventRejectionWrongAdapter,
		},
		{
			"disabled Entity", entityEventTestAdapter, entityEventTestRuntimeID, entityID,
			"single_press", EntityEventRejectionEntityDisabled,
		},
		{
			"disabled Entity outranks unsupported name", entityEventTestAdapter, entityEventTestRuntimeID,
			entityID, "not_supported", EntityEventRejectionEntityDisabled,
		},
	}
	for index, test := range tests {
		event := newEntityEvent(t, test.entityID, test.eventName, now.Add(-time.Duration(index)*time.Second))
		outcome, outcomeErr := service.RecordEntityEvent(
			ctx, test.adapterID, test.runtimeID, event, now,
		)
		if outcomeErr != nil {
			t.Fatalf("%s: %v", test.name, outcomeErr)
		}
		if outcome.Outcome != EntityEventOutcomeRejected || outcome.Rejection == nil ||
			*outcome.Rejection != test.wantReason {
			t.Fatalf("%s: recording = %#v, want %q", test.name, outcome, test.wantReason)
		}
		row := readStoredEntityEvent(t, database, event.ID)
		if row.disposition != "rejected" || row.rejection == nil || *row.rejection != string(test.wantReason) ||
			row.name != test.eventName {
			t.Fatalf("%s: stored row = %#v", test.name, row)
		}
	}

	// An enabled Entity with a supported type rejects only an unlisted name.
	if _, err := service.SetEntityEnabled(ctx, entityID, true); err != nil {
		t.Fatal(err)
	}
	unsupported := newEntityEvent(t, entityID, "triple_press", now)
	result, recordErr = service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, unsupported, now,
	)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeRejected || result.Rejection == nil ||
		*result.Rejection != EntityEventRejectionUnsupportedEvent {
		t.Fatalf("unsupported name recording = %#v", result)
	}

	// A corrupt persisted descriptor is an infrastructure failure, not a
	// rejection: validation never persists a guessed disposition.
	if _, err := database.ExecContext(ctx,
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{}}`, string(entityID),
	); err != nil {
		t.Fatal(err)
	}
	corrupt := newEntityEvent(t, entityID, "single_press", now)
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, corrupt, now,
	); err == nil {
		t.Fatal("corrupt descriptor error = nil, want catalog failure")
	}
	if count := countEntityEvents(t, database); count != len(tests)+2 {
		t.Fatalf("row count with corrupt descriptor = %d, want %d", count, len(tests)+2)
	}
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID,
		newEntityEvent(t, entityID, "single_press", now), now,
	); err == nil {
		t.Fatal("second corrupt descriptor error = nil, want catalog failure")
	}
}

// This test protects trusted-argument validation and fails if the service
// writes a row for a zero or malformed argument.
func TestEntityEventRecordingRejectsInvalidTrustedInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)
	valid := newEntityEvent(t, entityID, "single_press", now)

	tests := []struct {
		name       string
		adapterID  string
		runtimeID  RuntimeID
		event      EntityEvent
		receivedAt time.Time
	}{
		{"invalid adapter", "Simulator", entityEventTestRuntimeID, valid, now},
		{"invalid runtime", entityEventTestAdapter, "run_not-a-uuid", valid, now},
		{"invalid event ID", entityEventTestAdapter, entityEventTestRuntimeID,
			func() EntityEvent { event := valid; event.ID = "evt_not-a-uuid"; return event }(), now},
		{"invalid entity", entityEventTestAdapter, entityEventTestRuntimeID,
			func() EntityEvent { event := valid; event.EntityID = "nope"; return event }(), now},
		{"invalid correlation", entityEventTestAdapter, entityEventTestRuntimeID,
			func() EntityEvent { event := valid; event.CorrelationID = "cor_x"; return event }(), now},
		{"invalid name", entityEventTestAdapter, entityEventTestRuntimeID,
			func() EntityEvent { event := valid; event.Name = "SinglePress"; return event }(), now},
		{"missing emitted_at", entityEventTestAdapter, entityEventTestRuntimeID,
			func() EntityEvent { event := valid; event.EmittedAt = time.Time{}; return event }(), now},
		{"missing received_at", entityEventTestAdapter, entityEventTestRuntimeID, valid, time.Time{}},
	}
	for _, test := range tests {
		if _, err := service.RecordEntityEvent(
			ctx, test.adapterID, test.runtimeID, test.event, test.receivedAt,
		); !errors.Is(err, ErrInvalidEntityEvent) {
			t.Fatalf("%s: error = %v, want invalid entity event", test.name, err)
		}
	}
	if count := countEntityEvents(t, database); count != 0 {
		t.Fatalf("row count after invalid input = %d, want 0", count)
	}
}

// This test protects the record read path and fails if history ordering,
// rejection mapping, keyset continuation, or parent validation regresses.
func TestEntityEventHistoryPagesNewestFirstByReceiveOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)

	accepted := newEntityEvent(t, entityID, "single_press", now.Add(-2*time.Hour))
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, accepted, now.Add(-2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEntityEnabled(ctx, entityID, false); err != nil {
		t.Fatal(err)
	}
	rejected := newEntityEvent(t, entityID, "double_press", now.Add(-time.Hour))
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, rejected, now.Add(-time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEntityEnabled(ctx, entityID, true); err != nil {
		t.Fatal(err)
	}
	newest := newEntityEvent(t, entityID, "double_press", now)
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, newest, now,
	); err != nil {
		t.Fatal(err)
	}

	firstPage, listErr := service.ListEntityEvents(ctx, ListEntityEventsParams{
		EntityID: entityID, Limit: 2,
	})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if !firstPage.HasMore || len(firstPage.Items) != 2 {
		t.Fatalf("first page = %#v", firstPage)
	}
	if firstPage.Items[0].EventID != newest.ID || firstPage.Items[1].EventID != rejected.ID ||
		firstPage.Items[0].Disposition != EntityEventDispositionAccepted ||
		firstPage.Items[0].Rejection != nil ||
		firstPage.Items[1].Disposition != EntityEventDispositionRejected ||
		firstPage.Items[1].Rejection == nil ||
		*firstPage.Items[1].Rejection != EntityEventRejectionEntityDisabled ||
		firstPage.Items[1].Name != EntityEventName("double_press") ||
		firstPage.Items[0].ReceiveOrder <= firstPage.Items[1].ReceiveOrder {
		t.Fatalf("first page items = %#v", firstPage.Items)
	}
	if !firstPage.Items[0].EmittedAt.Equal(newest.EmittedAt) ||
		firstPage.Items[0].ReceivedAt.IsZero() || firstPage.Items[0].RecordedAt.IsZero() {
		t.Fatalf("first page timestamps = %#v", firstPage.Items[0])
	}

	cursor := firstPage.Items[len(firstPage.Items)-1].ReceiveOrder
	secondPage, listErr := service.ListEntityEvents(ctx, ListEntityEventsParams{
		EntityID: entityID, BeforeReceiveOrder: &cursor, Limit: 2,
	})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if secondPage.HasMore || len(secondPage.Items) != 1 || secondPage.Items[0].EventID != accepted.ID {
		t.Fatalf("second page = %#v", secondPage)
	}

	if _, err := service.ListEntityEvents(ctx, ListEntityEventsParams{
		EntityID: entityID, Limit: 2,
	}); err != nil {
		t.Fatal(err)
	}
	unknown := newTestEntityID(t)
	if _, err := service.ListEntityEvents(ctx, ListEntityEventsParams{
		EntityID: unknown, Limit: 2,
	}); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("unknown Entity error = %v, want entity not found", err)
	}
	for name, params := range map[string]ListEntityEventsParams{
		"zero limit":       {EntityID: entityID},
		"excessive limit":  {EntityID: entityID, Limit: 201},
		"invalid position": {EntityID: entityID, Limit: 2, BeforeReceiveOrder: new(int64)},
		"invalid Entity":   {EntityID: "nope", Limit: 2},
	} {
		if _, err := service.ListEntityEvents(ctx, params); !errors.Is(err, ErrInvalidPage) {
			t.Fatalf("%s error = %v, want invalid page", name, err)
		}
	}
}

// This test protects Entity Event isolation from State, Commands, health, and
// availability, and fails if recording an old Event mutates or satisfies any
// of them.
func TestEntityEventRecordingLeavesStateCommandsHealthAndAvailabilityUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)
	assertTableCount(t, database, "observations", 0)
	assertTableCount(t, database, "entity_states", 0)
	assertTableCount(t, database, "commands", 0)

	before, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	adapterBefore, err := service.GetAdapter(ctx, entityEventTestAdapter)
	if err != nil {
		t.Fatal(err)
	}

	// Backlog is consumed: an Event reported long before Core restarted is
	// recorded, and age is never a rejection reason.
	backlog := newEntityEvent(t, entityID, "single_press", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	result, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, backlog, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != EntityEventOutcomeAccepted {
		t.Fatalf("backlog recording = %#v", result)
	}

	after, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	adapterAfter, err := service.GetAdapter(ctx, entityEventTestAdapter)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != nil {
		t.Fatalf("Entity Event wrote State: %#v", after.State)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(adapterBefore, adapterAfter) {
		t.Fatalf("recording changed health or availability:\nbefore %#v\n after %#v", before, after)
	}
	history, err := service.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterAll, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 0 {
		t.Fatalf("Entity Event entered State history: %#v", history.Items)
	}
	assertTableCount(t, database, "observations", 0)
	assertTableCount(t, database, "entity_states", 0)
	assertTableCount(t, database, "commands", 0)
}

// This test protects the immutable-identity rule across later metadata changes
// and fails if a reprocessed ID is reclassified after a runtime takeover,
// enablement change, or narrowed support.
//
//nolint:gocognit // One flow proves identity stability across runtime, support, and enablement changes.
func TestEntityEventIdentityOutcomeSurvivesRuntimeAndSupportChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 6, 7, 0, 0, 0, time.UTC)
	service, repository := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)

	accepted := newEntityEvent(t, entityID, "double_press", now.Add(-time.Minute))
	if _, err := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, accepted, now.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	before := readStoredEntityEvent(t, database, accepted.ID)

	// Runtime takeover: the original runtime is released and a new one claims
	// the Adapter. A later report from the fenced runtime is stale, and a fresh
	// ID from the new runtime is recorded.
	if err := repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: entityEventTestAdapter, RuntimeID: entityEventTestRuntimeID,
		ReleasedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	replacement := parseTestRuntimeID(t, "run_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	if err := repository.ClaimAdapterRuntime(ctx, ClaimRuntimeWrite{
		RuntimeID: replacement, AdapterID: entityEventTestAdapter,
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: now, LeaseExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	stale := newEntityEvent(t, entityID, "single_press", now)
	result, recordErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, stale, now,
	)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeRejected || result.Rejection == nil ||
		*result.Rejection != EntityEventRejectionStaleRuntime {
		t.Fatalf("fenced runtime recording = %#v", result)
	}
	fresh := newEntityEvent(t, entityID, "single_press", now)
	result, recordErr = service.RecordEntityEvent(ctx, entityEventTestAdapter, replacement, fresh, now)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeAccepted {
		t.Fatalf("replacement runtime recording = %#v", result)
	}

	// Narrowed support while Core was offline rejects a newly reported name.
	narrowed := entityEventRegistration()
	narrowed.Entities[0].Support = EntitySupport(
		`{"state":{},"operations":{},"events":{"names":["single_press"]}}`,
	)
	if _, registerErr := service.Register(
		ctx, entityEventTestAdapter, replacement, narrowed,
	); registerErr != nil {
		t.Fatal(registerErr)
	}
	unsupported := newEntityEvent(t, entityID, "double_press", now)
	result, recordErr = service.RecordEntityEvent(ctx, entityEventTestAdapter, replacement, unsupported, now)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeRejected || result.Rejection == nil ||
		*result.Rejection != EntityEventRejectionUnsupportedEvent {
		t.Fatalf("narrowed support recording = %#v", result)
	}

	// Nothing reclassifies the already recorded ID. The event ID lookup comes
	// first, so a reprocessed report stays a duplicate even after the runtime
	// is fenced, the Entity is disabled, and its support is narrowed.
	result, recordErr = service.RecordEntityEvent(ctx, entityEventTestAdapter, entityEventTestRuntimeID, accepted, now)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeDuplicate || result.Rejection != nil {
		t.Fatalf("reprocessed ID outcome = %#v", result)
	}
	// The fingerprint covers the reporting runtime, so the same ID from a
	// replacement runtime is a changed tuple rather than a duplicate; the first
	// row still stands unchanged.
	result, recordErr = service.RecordEntityEvent(ctx, entityEventTestAdapter, replacement, accepted, now)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeIdentityConflict || result.Rejection != nil {
		t.Fatalf("replacement runtime reprocessed ID outcome = %#v", result)
	}
	if _, err := service.SetEntityEnabled(ctx, entityID, false); err != nil {
		t.Fatal(err)
	}
	result, recordErr = service.RecordEntityEvent(ctx, entityEventTestAdapter, entityEventTestRuntimeID, accepted, now)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if result.Outcome != EntityEventOutcomeDuplicate {
		t.Fatalf("disabled reprocessed ID outcome = %#v", result)
	}
	if got := readStoredEntityEvent(t, database, accepted.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("reprocessed ID changed its row:\n got %#v\nwant %#v", got, before)
	}
}

// This test protects the fixed retention sweep and fails if it prunes at the
// boundary, ignores the batch size, or removes a record that is not expired.
func TestEntityEventRetentionDeletesOnlyRecordsOlderThanCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	sweepTime := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := sweepTime
	service, repository := newEntityEventTestService(t, database, &now)
	entityID := registerEntityEventEntity(t, service)

	seed := func(recordedAt time.Time, name string) EntityEvent {
		event := newEntityEvent(t, entityID, name, recordedAt)
		now = recordedAt
		if _, err := service.RecordEntityEvent(
			ctx, entityEventTestAdapter, entityEventTestRuntimeID, event, recordedAt,
		); err != nil {
			t.Fatal(err)
		}
		return event
	}
	expired := []EntityEvent{
		seed(sweepTime.Add(-31*24*time.Hour), "single_press"),
		seed(sweepTime.Add(-40*24*time.Hour), "single_press"),
		seed(sweepTime.Add(-90*24*time.Hour), "double_press"),
	}
	// Strictly older than the cutoff: a record exactly on the boundary and a
	// newer record both survive.
	boundary := seed(sweepTime.Add(-EntityEventHistoryRetention), "single_press")
	retained := seed(sweepTime.Add(-time.Hour), "single_press")

	if err := service.DeleteExpiredEntityEvents(ctx, time.Time{}); err == nil {
		t.Fatal("retention sweep without a sweep time unexpectedly succeeded")
	}
	if err := service.DeleteExpiredEntityEvents(ctx, sweepTime); err != nil {
		t.Fatal(err)
	}
	if count := countEntityEvents(t, database); count != 2 {
		t.Fatalf("row count after sweep = %d, want 2", count)
	}
	for _, event := range expired {
		if _, err := readStoredEntityEventErr(t, database, event.ID); err == nil {
			t.Fatalf("expired entity event %s survived the sweep", event.ID)
		}
	}
	readStoredEntityEvent(t, database, boundary.ID)
	readStoredEntityEvent(t, database, retained.ID)

	// One repository call is one bounded transaction, so a caller can sweep
	// until a short batch proves the cutoff is exhausted.
	_, batchErr := repository.DeleteEntityEventsBefore(ctx, sweepTime, 0)
	if batchErr == nil {
		t.Fatal("retention batch size 0 unexpectedly succeeded")
	}
	if _, cutoffErr := repository.DeleteEntityEventsBefore(ctx, time.Time{}, 1); cutoffErr == nil {
		t.Fatal("retention cutoff 0 unexpectedly succeeded")
	}
	batched := []EntityEvent{
		seed(sweepTime.Add(-60*24*time.Hour), "single_press"),
		seed(sweepTime.Add(-61*24*time.Hour), "single_press"),
		seed(sweepTime.Add(-62*24*time.Hour), "single_press"),
	}
	cutoff := sweepTime.Add(-EntityEventHistoryRetention)
	deleted, deleteErr := repository.DeleteEntityEventsBefore(ctx, cutoff, 2)
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if deleted != 2 {
		t.Fatalf("first batch deleted %d rows, want 2", deleted)
	}
	deleted, deleteErr = repository.DeleteEntityEventsBefore(ctx, cutoff, 2)
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if deleted != 1 {
		t.Fatalf("second batch deleted %d rows, want 1", deleted)
	}
	for _, event := range batched {
		if _, err := readStoredEntityEventErr(t, database, event.ID); err == nil {
			t.Fatalf("batched entity event %s survived the sweep", event.ID)
		}
	}
	if count := countEntityEvents(t, database); count != 2 {
		t.Fatalf("row count after batched sweep = %d, want 2", count)
	}
}

func readStoredEntityEventErr(
	t *testing.T,
	database *sql.DB,
	eventID EntityEventID,
) (storedEntityEvent, error) {
	t.Helper()
	var row storedEntityEvent
	var rejection sql.NullString
	err := database.QueryRowContext(context.Background(), `
		SELECT receive_order, adapter_id, runtime_id, entity_id, correlation_id, name,
		       disposition, rejection_code, fingerprint, emitted_at, received_at, recorded_at
		FROM entity_events
		WHERE event_id = ?`, string(eventID)).
		Scan(
			&row.receiveOrder, &row.adapterID, &row.runtimeID, &row.entityID, &row.correlationID,
			&row.name, &row.disposition, &rejection, &row.fingerprint, &row.emittedAt,
			&row.receivedAt, &row.recordedAt,
		)
	if err != nil {
		return storedEntityEvent{}, err
	}
	if rejection.Valid {
		code := rejection.String
		row.rejection = &code
	}
	return row, nil
}

// This test protects the retention sweep against a State anchor and against
// losing duplicate evidence. It fails if an expired row survives because its
// Entity has current State, if an expired rejected row survives, or if a
// retained row stops detecting its duplicate.
func TestEntityEventRetentionIgnoresStateAnchorAndKeepsDuplicateEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	sweepTime := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := sweepTime
	service, _ := newEntityEventTestService(t, database, &now)
	eventEntityID := registerEntityEventEntity(t, service)
	powerBinding, err := service.Register(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID := powerBinding.Entities[0].EntityID
	// The power Entity has current State: unlike Observation pruning, Entity
	// Event retention has no anchor to exempt.
	observedAt := sweepTime.Add(-31 * 24 * time.Hour)
	if _, projectionErr := service.ProjectObservation(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID,
		newObservation(t, powerEntityID, `true`, observedAt), observedAt,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	assertTableCount(t, database, "entity_states", 1)

	now = observedAt
	expiredAccepted := newEntityEvent(t, eventEntityID, "single_press", now)
	if _, expiredErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, expiredAccepted, now,
	); expiredErr != nil {
		t.Fatal(expiredErr)
	}
	// A rejected expired row is pruned under the same cutoff: retention has no
	// disposition filter.
	expiredRejected := newEntityEvent(t, powerEntityID, "single_press", now)
	outcome, rejectedErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, expiredRejected, now,
	)
	if rejectedErr != nil {
		t.Fatal(rejectedErr)
	}
	if outcome.Outcome != EntityEventOutcomeRejected {
		t.Fatalf("power Entity recording = %#v, want rejected", outcome)
	}
	now = sweepTime
	retained := newEntityEvent(t, eventEntityID, "double_press", now)
	if _, retainedErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, retained, now,
	); retainedErr != nil {
		t.Fatal(retainedErr)
	}
	before := readStoredEntityEvent(t, database, retained.ID)

	if sweepErr := service.DeleteExpiredEntityEvents(ctx, sweepTime); sweepErr != nil {
		t.Fatal(sweepErr)
	}
	for _, expired := range []EntityEvent{expiredAccepted, expiredRejected} {
		if _, readErr := readStoredEntityEventErr(t, database, expired.ID); readErr == nil {
			t.Fatalf("expired Entity Event %s survived the sweep", expired.ID)
		}
	}
	assertTableCount(t, database, "entity_states", 1)
	if count := countEntityEvents(t, database); count != 1 {
		t.Fatalf("entity_events count after sweep = %d, want 1", count)
	}

	// The retained row still provides duplicate evidence and stays untouched.
	duplicate, duplicateErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, retained, now,
	)
	if duplicateErr != nil {
		t.Fatal(duplicateErr)
	}
	if duplicate.Outcome != EntityEventOutcomeDuplicate || duplicate.Rejection != nil {
		t.Fatalf("retained Entity Event duplicate = %#v", duplicate)
	}
	if got := readStoredEntityEvent(t, database, retained.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("retained row changed:\n got %#v\nwant %#v", got, before)
	}
}

// This test protects the independence of the two retention policies. It fails
// if an Entity Event sweep prunes Observations, if the Observation window
// changes, or if Observation pruning removes Entity Event rows.
func TestEntityEventRetentionLeavesObservationRetentionUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	sweepTime := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	now := sweepTime
	service, _ := newEntityEventTestService(t, database, &now)
	eventEntityID := registerEntityEventEntity(t, service)
	powerBinding, err := service.Register(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID := powerBinding.Entities[0].EntityID
	expiredObservation := newObservation(t, powerEntityID, `false`, sweepTime.Add(-40*24*time.Hour))
	if _, projectionErr := service.ProjectObservation(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, expiredObservation,
		sweepTime.Add(-40*24*time.Hour),
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	currentObservation := newObservation(t, powerEntityID, `true`, sweepTime.Add(-time.Hour))
	if _, projectionErr := service.ProjectObservation(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, currentObservation,
		sweepTime.Add(-time.Hour),
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}

	now = sweepTime.Add(-40 * 24 * time.Hour)
	expiredEvent := newEntityEvent(t, eventEntityID, "single_press", now)
	if _, recordErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, expiredEvent, now,
	); recordErr != nil {
		t.Fatal(recordErr)
	}
	now = sweepTime
	retainedEvent := newEntityEvent(t, eventEntityID, "double_press", now)
	if _, recordErr := service.RecordEntityEvent(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, retainedEvent, now,
	); recordErr != nil {
		t.Fatal(recordErr)
	}

	if sweepErr := service.DeleteExpiredEntityEvents(ctx, sweepTime); sweepErr != nil {
		t.Fatal(sweepErr)
	}
	assertObservationCount(t, database, 2)
	assertTableCount(t, database, "entity_states", 1)
	if _, readErr := readStoredEntityEventErr(t, database, expiredEvent.ID); readErr == nil {
		t.Fatal("expired Entity Event survived the sweep")
	}
	readStoredEntityEvent(t, database, retainedEvent.ID)

	// Observation retention still applies its own window and leaves the
	// retained Entity Event alone.
	if pruneErr := service.DeleteExpiredObservations(
		ctx, sweepTime, 30*24*time.Hour,
	); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, []ObservationID{currentObservation.ID})
	assertTableCount(t, database, "entity_events", 1)
	readStoredEntityEvent(t, database, retainedEvent.ID)
}

// This test protects both Entity Event indexes and fails if the history or
// retention index disappears, changes column order, or stops serving its
// query.
func TestEntityEventIndexesBackHistoryAndRetentionQueries(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	for _, test := range []struct {
		index   string
		query   string
		columns []string
	}{
		{
			"entity_events_entity_history_idx",
			"PRAGMA index_info('entity_events_entity_history_idx')",
			[]string{"entity_id", "receive_order"},
		},
		{
			"entity_events_retention_idx",
			"PRAGMA index_info('entity_events_retention_idx')",
			[]string{"recorded_at", "receive_order"},
		},
	} {
		if got := indexColumnNames(t, database, test.query); !slices.Equal(got, test.columns) {
			t.Fatalf("%s columns = %v, want %v", test.index, got, test.columns)
		}
	}
	historyPlan := entityEventQueryPlan(
		t, database,
		`SELECT event_id, entity_id, name, disposition, rejection_code, emitted_at,
		        received_at, recorded_at, receive_order
		 FROM entity_events
		 WHERE entity_id = ?
		 ORDER BY receive_order DESC
		 LIMIT ?`,
		"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", 51,
	)
	if !slices.ContainsFunc(historyPlan, func(detail string) bool {
		return strings.Contains(detail, "entity_events_entity_history_idx")
	}) {
		t.Fatalf("history query plan = %v, want the history index", historyPlan)
	}
	retentionPlan := entityEventQueryPlan(
		t, database,
		`DELETE FROM entity_events
		 WHERE receive_order IN (
		     SELECT receive_order
		     FROM entity_events
		     WHERE recorded_at < CAST(? AS TEXT)
		     ORDER BY recorded_at, receive_order
		     LIMIT CAST(? AS INTEGER)
		 )`,
		"2026-09-01T00:00:00.000000000Z", 500,
	)
	if !slices.ContainsFunc(retentionPlan, func(detail string) bool {
		return strings.Contains(detail, "entity_events_retention_idx")
	}) {
		t.Fatalf("retention query plan = %v, want the retention index", retentionPlan)
	}
}

func indexColumnNames(t *testing.T, database *sql.DB, query string) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var sequence, columnID int
		var name string
		if scanErr := rows.Scan(&sequence, &columnID, &name); scanErr != nil {
			t.Fatal(scanErr)
		}
		names = append(names, name)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	return names
}

func entityEventQueryPlan(t *testing.T, database *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if scanErr := rows.Scan(&id, &parent, &notUsed, &detail); scanErr != nil {
			t.Fatal(scanErr)
		}
		details = append(details, detail)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	return details
}

// This test protects the service-level 500-row sweep and fails if one pass
// stops after a single full batch, leaving expired rows for the next hour.
func TestEntityEventRetentionSweepsMoreThanOneBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	sweepTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := sweepTime
	service, _ := newEntityEventTestService(t, database, &now)
	expiredAt := sweepTime.Add(-40 * 24 * time.Hour)
	sortableTimestamp := func(value time.Time) string {
		return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	// More than two full batches, so the sweep must iterate until a short batch.
	const expiredCount = 2*entityEventDeleteBatchSize + 1
	if _, err := database.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < ?
		)
		INSERT INTO entity_events (
			event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
			fingerprint, disposition, rejection_code, emitted_at, received_at, recorded_at
		)
		SELECT printf('evt_01890f47-7a6b-7c4d-8e9f-%012d', value), 'simulator',
			'run_01890f47-7a6b-7c4d-8e9f-0123456789ab',
			'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
			printf('cor_01890f47-7a6b-7c4d-8e9f-%012d', value), 'single_press',
			zeroblob(32), 'accepted', NULL, ?, ?, ?
		FROM sequence`,
		expiredCount,
		sortableTimestamp(expiredAt), sortableTimestamp(expiredAt), sortableTimestamp(expiredAt),
	); err != nil {
		t.Fatal(err)
	}
	const retainedEventID = EntityEventID("evt_01890f47-7a6b-7c4d-8e9f-000000999999")
	if _, err := database.ExecContext(ctx, `
		INSERT INTO entity_events (
			event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
			fingerprint, disposition, rejection_code, emitted_at, received_at, recorded_at
		) VALUES (?, 'simulator', 'run_01890f47-7a6b-7c4d-8e9f-0123456789ab',
			'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
			'cor_01890f47-7a6b-7c4d-8e9f-000000999999', 'double_press',
			zeroblob(32), 'accepted', NULL, ?, ?, ?)`,
		string(retainedEventID),
		sortableTimestamp(sweepTime), sortableTimestamp(sweepTime), sortableTimestamp(sweepTime),
	); err != nil {
		t.Fatal(err)
	}
	if count := countEntityEvents(t, database); count != expiredCount+1 {
		t.Fatalf("seeded entity events = %d, want %d", count, expiredCount+1)
	}

	if sweepErr := service.DeleteExpiredEntityEvents(ctx, sweepTime); sweepErr != nil {
		t.Fatal(sweepErr)
	}
	if count := countEntityEvents(t, database); count != 1 {
		t.Fatalf("entity events after multi-batch sweep = %d, want 1", count)
	}
	readStoredEntityEvent(t, database, retainedEventID)
}

// entityEventDirectRow is one direct entity_events row used to exercise the
// SQLite shape constraints without the service's own validation path.
type entityEventDirectRow struct {
	eventID       string
	adapterID     string
	runtimeID     string
	entityID      string
	correlationID string
	name          string
}

func insertDirectEntityEvent(ctx context.Context, database *sql.DB, row entityEventDirectRow) error {
	_, err := database.ExecContext(ctx, `
		INSERT INTO entity_events (
			event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
			fingerprint, disposition, rejection_code, emitted_at, received_at, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, zeroblob(32), 'accepted', NULL, ?, ?, ?)`,
		row.eventID, row.adapterID, row.runtimeID, row.entityID, row.correlationID, row.name,
		"2026-09-01T00:00:00.000000000Z",
		"2026-09-01T00:00:00.000000000Z",
		"2026-09-01T00:00:00.000000000Z",
	)
	return err
}

// This test protects the fixed entity_events identity shape at the SQLite
// boundary and fails if a malformed event, entity, correlation, or runtime ID,
// or an adapter ID outside the canonical slug, can be inserted directly. Wire
// and service parsers enforce canonical UUIDv7 IDs, so these CHECKs are the
// last line of defense for rows written by any other path.
func TestEntityEventSchemaEnforcesCanonicalIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	valid := entityEventDirectRow{
		eventID:       "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		adapterID:     entityEventTestAdapter,
		runtimeID:     entityEventTestRuntimeID,
		entityID:      "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		correlationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		name:          "single_press",
	}
	if err := insertDirectEntityEvent(ctx, database, valid); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(row *entityEventDirectRow)
	}{
		{"short event ID", func(row *entityEventDirectRow) { row.eventID = "evt_01890f47" }},
		{"long event ID", func(row *entityEventDirectRow) { row.eventID += "0" }},
		{"wrong event prefix", func(row *entityEventDirectRow) {
			row.eventID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"short entity ID", func(row *entityEventDirectRow) { row.entityID = "ent_01890f47" }},
		{"long entity ID", func(row *entityEventDirectRow) { row.entityID += "0" }},
		{"wrong entity prefix", func(row *entityEventDirectRow) {
			row.entityID = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"short correlation ID", func(row *entityEventDirectRow) { row.correlationID = "cor_01890f47" }},
		{"long correlation ID", func(row *entityEventDirectRow) { row.correlationID += "0" }},
		{"wrong correlation prefix", func(row *entityEventDirectRow) {
			row.correlationID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"short runtime ID", func(row *entityEventDirectRow) { row.runtimeID = "run_01890f47" }},
		{"wrong runtime prefix", func(row *entityEventDirectRow) {
			row.runtimeID = "xun_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"empty adapter", func(row *entityEventDirectRow) { row.adapterID = "" }},
		{"uppercase adapter", func(row *entityEventDirectRow) { row.adapterID = "Simulator" }},
		{"adapter with space", func(row *entityEventDirectRow) { row.adapterID = "sim ulated" }},
		{"adapter leading dash", func(row *entityEventDirectRow) { row.adapterID = "-simulator" }},
		{"overlong adapter", func(row *entityEventDirectRow) { row.adapterID = strings.Repeat("a", 64) }},
	}
	for index, test := range tests {
		row := valid
		// A distinct valid event ID keeps the UNIQUE constraint out of the
		// way so the mutated column's CHECK is the failing rule.
		row.eventID = fmt.Sprintf("evt_01890f47-7a6b-7c4d-8e9f-%012d", index+1)
		test.mutate(&row)
		if err := insertDirectEntityEvent(ctx, database, row); err == nil {
			t.Fatalf("%s: malformed row was accepted: %#v", test.name, row)
		}
	}
	if count := countEntityEvents(t, database); count != 1 {
		t.Fatalf("entity_events count = %d, want only the valid row", count)
	}
}
