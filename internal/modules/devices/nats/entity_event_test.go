package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const (
	testEntityEventID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testOtherEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ac"
)

type testEntityEventRecorder struct {
	mutex   sync.Mutex
	events  []devices.EntityEvent
	result  devices.EntityEventRecordResult
	err     error
	failFor devices.EntityEventID
}

func (recorder *testEntityEventRecorder) RecordEntityEvent(
	_ context.Context,
	_ string,
	_ devices.RuntimeID,
	event devices.EntityEvent,
	_ time.Time,
) (devices.EntityEventRecordResult, error) {
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mutex.Unlock()
	if recorder.failFor != "" && event.ID == recorder.failFor {
		return devices.EntityEventRecordResult{}, errors.New("temporary SQLite failure")
	}
	return recorder.result, recorder.err
}

func (recorder *testEntityEventRecorder) recorded() []devices.EntityEvent {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]devices.EntityEvent(nil), recorder.events...)
}

// gatedEntityEventRecorder blocks inside the record call so a test can prove
// JetStream acknowledgement waits for the commit.
type gatedEntityEventRecorder struct {
	inner   EntityEventRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (recorder *gatedEntityEventRecorder) RecordEntityEvent(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	event devices.EntityEvent,
	receivedAt time.Time,
) (devices.EntityEventRecordResult, error) {
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return recorder.inner.RecordEntityEvent(ctx, adapterID, runtimeID, event, receivedAt)
}

// committedEntityEventRecorder commits the row and then blocks, so a test can
// prove acknowledgement follows the commit rather than the call boundary.
type committedEntityEventRecorder struct {
	inner   EntityEventRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (recorder *committedEntityEventRecorder) RecordEntityEvent(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	event devices.EntityEvent,
	receivedAt time.Time,
) (devices.EntityEventRecordResult, error) {
	result, err := recorder.inner.RecordEntityEvent(ctx, adapterID, runtimeID, event, receivedAt)
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return result, err
}

// coreEntityEvents is a real SQLite-backed Core stack: one registered event
// source Entity, the devices service, and no HTTP or automation layer.
type coreEntityEvents struct {
	database   *sql.DB
	path       string
	repository *devices.SQLiteRepository
	service    *devices.Service
	entityID   devices.EntityID
}

func openCoreEntityEvents(t *testing.T, path string) *coreEntityEvents {
	t.Helper()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	core := &coreEntityEvents{
		database: database, path: path, repository: repository,
		service: devices.NewService(devices.SQLiteStores(repository), nil, catalog, devices.Dependencies{}),
	}
	core.entityID = core.register(t)
	return core
}

func startCoreEntityEvents(t *testing.T) *coreEntityEvents {
	t.Helper()
	return openCoreEntityEvents(t, filepath.Join(t.TempDir(), "hearth.db"))
}

func (core *coreEntityEvents) register(t *testing.T) devices.EntityID {
	t.Helper()
	ctx := context.Background()
	claimedAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if err := core.repository.ClaimAdapterRuntime(ctx, devices.ClaimRuntimeWrite{
		RuntimeID: devices.RuntimeID(testRuntimeID), AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	externalID := "sim-buttons"
	binding, err := core.service.Register(
		ctx, "simulator", devices.RuntimeID(testRuntimeID), devices.Registration{
			BindingKey: "office-buttons",
			Device: devices.DeviceDescriptor{
				ExternalID: &externalID, Name: "Office buttons", Kind: devices.DeviceKindSensor,
			},
			Entities: []devices.EntityDescriptor{{
				Key: "buttons", ExternalID: "sim.buttons", Name: "Buttons",
				TypeID: devices.EntityTypeEnumeventV1,
				Support: devices.EntitySupport(
					`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
				),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return binding.Entities[0].EntityID
}

// restart closes and reopens the database and rebuilds the Core stack, so a
// test can prove that a redelivered event finds the committed row instead of
// writing a second one.
func (core *coreEntityEvents) restart(t *testing.T) *coreEntityEvents {
	t.Helper()
	if err := core.database.Close(); err != nil {
		t.Fatal(err)
	}
	return openCoreEntityEvents(t, core.path)
}

func (core *coreEntityEvents) entityEventCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := core.database.QueryRowContext(
		context.Background(), "SELECT count(*) FROM entity_events",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// entityEventRecordFailures returns the record-failure diagnostics of one
// report identity, so a test can count its redeliveries.
func entityEventRecordFailures(
	t *testing.T,
	logs *lockedTestLogWriter,
	eventID string,
) []map[string]any {
	t.Helper()
	var failures []map[string]any
	for _, failure := range logEvents(logs.records(t), "entity_event.processing_failed") {
		if failure["entity_event_id"] == eventID {
			failures = append(failures, failure)
		}
	}
	return failures
}

// waitForRecordFailures polls until one report has failed recording at least
// want times. Only a redelivery can produce another failure for an identity
// that was neither acknowledged nor terminated, so the count also shows which
// failure policy the handler chose.
func waitForRecordFailures(
	t *testing.T,
	logs *lockedTestLogWriter,
	eventID string,
	want int,
	timeout time.Duration,
) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var failures []map[string]any
	for time.Now().Before(deadline) {
		failures = entityEventRecordFailures(t, logs, eventID)
		if len(failures) >= want {
			return failures
		}
		time.Sleep(10 * time.Millisecond)
	}
	return failures
}

// assertSafeRecordFailures fails if a record-failure diagnostic lacks its safe
// structured classification or carries raw subject or error text.
func assertSafeRecordFailures(t *testing.T, failures []map[string]any) {
	t.Helper()
	for _, failure := range failures {
		if failure["stage"] != "record" || failure["error_code"] != "record_failed" {
			t.Fatalf("failed record diagnostic = %#v", failure)
		}
		for _, key := range []string{"subject", "error"} {
			if _, present := failure[key]; present {
				t.Fatalf("failed record diagnostic logs unsafe %q (record = %#v)", key, failure)
			}
		}
	}
}

// registerCorruptEventSource registers a second event source Entity and then
// corrupts its persisted support, so recording its reports fails at the catalog
// seam. The healthy Entity from startCoreEntityEvents keeps committing, so one
// test can prove the permanently unrecordable report is terminated instead of
// blocking a later valid report.
func registerCorruptEventSource(t *testing.T, core *coreEntityEvents) devices.EntityID {
	t.Helper()
	ctx := context.Background()
	externalID := "sim-poison-buttons"
	binding, err := core.service.Register(
		ctx, "simulator", devices.RuntimeID(testRuntimeID), devices.Registration{
			BindingKey: "office-poison-buttons",
			Device: devices.DeviceDescriptor{
				ExternalID: &externalID, Name: "Office poison buttons", Kind: devices.DeviceKindSensor,
			},
			Entities: []devices.EntityDescriptor{{
				Key: "poison-buttons", ExternalID: "sim.poison-buttons", Name: "Poison buttons",
				TypeID: devices.EntityTypeEnumeventV1,
				Support: devices.EntitySupport(
					`{"state":{},"operations":{},"events":{"names":["single_press"]}}`,
				),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	poisonEntityID := binding.Entities[0].EntityID
	// Dropping the events member leaves a descriptor no event-source support
	// validator accepts, which the catalog reports as a failure rather than a
	// domain rejection.
	if _, execErr := core.database.ExecContext(
		ctx, "UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{}}`, string(poisonEntityID),
	); execErr != nil {
		t.Fatal(execErr)
	}
	return poisonEntityID
}

func (core *coreEntityEvents) entityEventRow(t *testing.T, eventID string) storedEntityEventRow {
	t.Helper()
	var row storedEntityEventRow
	var rejection sql.NullString
	err := core.database.QueryRowContext(context.Background(), `
		SELECT disposition, rejection_code, recorded_at
		FROM entity_events
		WHERE event_id = ?`, eventID).
		Scan(&row.disposition, &rejection, &row.recordedAt)
	if err != nil {
		t.Fatalf("read entity event %s: %v", eventID, err)
	}
	row.rejection = rejection.String
	return row
}

type storedEntityEventRow struct {
	disposition string
	rejection   string
	recordedAt  string
}

func entityEventValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func entityEventEnvelope(
	t *testing.T,
	eventID string,
	entityID devices.EntityID,
	name string,
	emittedAt time.Time,
) []byte {
	t.Helper()
	payload, err := natswire.Encode(entityEventValidator(t), contractsv1.EntityEventSchemaID,
		natswire.Envelope[entityEvent]{
			ID: eventID, Schema: contractsv1.EntityEventSchemaID,
			EmittedAt:     emittedAt.UTC().Format(time.RFC3339Nano),
			CorrelationID: testCorrelationID,
			Data:          entityEvent{EntityID: string(entityID), Name: name},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// publishEntityEventMessage publishes one raw payload with explicit headers and
// subject, so tests can spoof identity, route, and causation.
func publishEntityEventMessage(
	t *testing.T,
	js jetstream.JetStream,
	subject string,
	headers natsgo.Header,
	payload []byte,
) {
	t.Helper()
	if _, err := js.PublishMsg(context.Background(), &natsgo.Msg{
		Subject: subject, Header: headers, Data: payload,
	}); err != nil {
		t.Fatal(err)
	}
}

func publishEntityEventEnvelope(
	t *testing.T,
	js jetstream.JetStream,
	entityID devices.EntityID,
	eventID, name string,
	emittedAt time.Time,
) {
	t.Helper()
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{eventID}}
	natswire.InjectTrace(testTraceContext(t), headers)
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, entityID),
		headers, entityEventEnvelope(t, eventID, entityID, name, emittedAt),
	)
}

func mustEntityEventSubject(t *testing.T, entityID devices.EntityID) string {
	t.Helper()
	subject, err := natswire.EntityEventSubject("simulator", testRuntimeID, string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// newEntityEventTestMessage builds one JetStream message whose MsgId header
// agrees with its envelope, exactly as the SDK publishes it. Tests that need a
// mismatch or a missing header publish raw messages instead.
func newEntityEventTestMessage(
	t *testing.T,
	entityID devices.EntityID,
	payload []byte,
	ackErr error,
) *ackOrderingTestMessage {
	t.Helper()
	envelope, err := natswire.Decode[entityEvent](
		entityEventValidator(t), contractsv1.EntityEventSchemaID, payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{envelope.ID}}
	natswire.InjectTrace(testTraceContext(t), headers)
	return &ackOrderingTestMessage{
		metadata: &jetstream.MsgMetadata{
			Sequence:  jetstream.SequencePair{Stream: 4},
			Timestamp: time.Now().UTC(),
		},
		data:    payload,
		headers: headers,
		subject: mustEntityEventSubject(t, entityID),
		acked:   make(chan struct{}),
		ackErr:  ackErr,
	}
}

func newEntityEventID(t *testing.T) string {
	t.Helper()
	id, err := devices.NewEntityEventID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

// This test protects the committed recording path and fails if a broker
// acknowledged Entity Event is not persisted, is not acknowledged, or loses
// its reported identity.
func TestEntityEventConsumerRecordsCommittedInputAndAcknowledges(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionEntityEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreEntityEvents(t)
	logs, logger := newTestLogSink()
	running, err := StartEntityEventConsumer(
		context.Background(), consumer, entityEventValidator(t), core.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)
	if !running.Active() {
		t.Fatal("entity event consumer is not active")
	}

	emittedAt := time.Now().UTC().Add(2 * time.Minute)
	publishEntityEventEnvelope(t, js, core.entityID, testEntityEventID, "single_press", emittedAt)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 1 && info.NumAckPending == 0
	})
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("entity event rows = %d, want 1", count)
	}
	firstRow := core.entityEventRow(t, testEntityEventID)
	if firstRow.disposition != "accepted" || firstRow.rejection != "" || firstRow.recordedAt == "" {
		t.Fatalf("stored row = %#v", firstRow)
	}
	recorded := logEvents(logs.records(t), "entity_event.recorded")
	if len(recorded) != 1 {
		t.Fatalf("entity_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}
	if recorded[0]["entity_event_id"] != testEntityEventID ||
		recorded[0]["entity_id"] != string(core.entityID) ||
		recorded[0]["name"] != "single_press" ||
		recorded[0]["outcome"] != "accepted" {
		t.Fatalf("recorded record = %#v", recorded[0])
	}
	if failures := logEvents(logs.records(t), "entity_event.processing_failed"); len(failures) != 0 {
		t.Fatalf("entity_event.processing_failed events = %d, want 0:\n%s", len(failures), logs.output())
	}
	// Skew is diagnostic only: the report stays recorded with no age gate.
	skew := logEvents(logs.records(t), "entity_event.clock_skew")
	if len(skew) != 1 || skew[0]["level"] != "WARN" ||
		skew[0]["entity_event_id"] != testEntityEventID {
		t.Fatalf("clock skew records = %#v\n%s", skew, logs.output())
	}
	if validationErr := ValidateEntityEventResources(context.Background(), js); validationErr != nil {
		t.Fatal(validationErr)
	}

	running.Stop()
	select {
	case <-running.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("entity event consumer did not stop")
	}
	if running.Active() {
		t.Fatal("stopped entity event consumer remains active")
	}
}

// This test protects permanent wire classification and fails if malformed,
// misrouted, spoofed, or caused input reaches recording, writes a row, blocks
// acknowledgement, exposes raw payloads in logs, or stops valid input behind it.
//
//nolint:gocognit // One flow keeps every permanent wire class and its acknowledgement evidence together.
func TestEntityEventConsumerAcknowledgesPermanentWireErrorsWithoutRows(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionEntityEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreEntityEvents(t)
	logs, logger := newTestLogSink()
	recorder := &testEntityEventRecorder{}
	running, err := StartEntityEventConsumer(
		context.Background(), consumer, entityEventValidator(t), recorder, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	// Malformed payload.
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{newEntityEventID(t)}}, []byte(`{}`),
	)

	// Spoofed MsgId: a valid envelope whose header identity disagrees.
	spoofedHeaderID := newEntityEventID(t)
	spoofedPayloadID := newEntityEventID(t)
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{spoofedHeaderID}},
		entityEventEnvelope(t, spoofedPayloadID, core.entityID, "single_press", time.Now().UTC()),
	)

	// Route mismatch: the subject names another Entity than the payload.
	routedID := newEntityEventID(t)
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, devices.EntityID(testOtherEntityID)),
		natsgo.Header{natsgo.MsgIdHdr: []string{routedID}},
		entityEventEnvelope(t, routedID, core.entityID, "single_press", time.Now().UTC()),
	)

	// Missing MsgId: without header identity Core cannot form trustworthy
	// domain input, so the report is permanent and never recorded.
	missingIDHeader := newEntityEventID(t)
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, core.entityID),
		natsgo.Header{},
		entityEventEnvelope(t, missingIDHeader, core.entityID, "single_press", time.Now().UTC()),
	)

	// Unexpected causation: a report never carries causation, and the strict
	// envelope rejects the field as wire-invalid.
	causedID := newEntityEventID(t)
	caused := `{"id":"` + causedID + `",` +
		`"schema":"urn:hearth:schema:entity-event:v1",` +
		`"emitted_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `",` +
		`"correlation_id":"` + testCorrelationID + `",` +
		`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"data":{"entity_id":"` + string(core.entityID) + `","name":"single_press"}}`
	publishEntityEventMessage(
		t, js, mustEntityEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{causedID}}, []byte(caused),
	)

	// A permanent rejection must not block valid input behind it.
	publishEntityEventEnvelope(t, js, core.entityID, testEntityEventID, "double_press", time.Now().UTC())
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 6 && info.NumAckPending == 0
	})

	recorded := recorder.recorded()
	if len(recorded) != 1 || string(recorded[0].ID) != testEntityEventID {
		t.Fatalf("recorded events = %#v, want only the valid report", recorded)
	}
	if core.entityEventCount(t) != 0 {
		t.Fatalf("rows written by a fake recorder = %d, want 0", core.entityEventCount(t))
	}
	invalid := logEvents(logs.records(t), "entity_event.invalid")
	if len(invalid) != 5 {
		t.Fatalf("entity_event.invalid events = %d, want 5:\n%s", len(invalid), logs.output())
	}
	wantCodes := map[string]bool{
		"entity_event_decode_failed":   false,
		"entity_event_msg_id_mismatch": false,
		"entity_event_entity_mismatch": false,
	}
	for _, record := range invalid {
		if record["level"] != "WARN" {
			t.Fatalf("invalid level = %#v, want WARN (record = %#v)", record["level"], record)
		}
		code, ok := record["error_code"].(string)
		if !ok {
			t.Fatalf("invalid record lacks error_code (record = %#v)", record)
		}
		if _, tracked := wantCodes[code]; tracked {
			wantCodes[code] = true
		}
		for _, key := range []string{"subject", "error", "name"} {
			if _, present := record[key]; present {
				t.Fatalf("invalid record logs unsafe %q (record = %#v)", key, record)
			}
		}
	}
	for code, seen := range wantCodes {
		if !seen {
			t.Fatalf("invalid records lack %q:\n%s", code, logs.output())
		}
	}
}

// This test protects the production retry window and fails if a report Core
// cannot record is positively acknowledged, persisted, terminated, or retried
// in a hot loop instead of after the named redelivery delay. It also pins the
// safe structured diagnostics of the record failure.
func TestEntityEventConsumerDelaysFailedRecordWithoutAckOrRow(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionEntityEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreEntityEvents(t)
	logs, logger := newTestLogSink()
	recorder := &testEntityEventRecorder{
		failFor: devices.EntityEventID(testEntityEventID),
		result:  devices.EntityEventRecordResult{Outcome: devices.EntityEventOutcomeAccepted},
	}
	running, err := StartEntityEventConsumer(
		context.Background(), consumer, entityEventValidator(t), recorder, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	publishEntityEventEnvelope(t, js, core.entityID, testEntityEventID, "single_press", time.Now().UTC())
	failures := waitForRecordFailures(t, logs, testEntityEventID, 1, 3*time.Second)
	if len(failures) != 1 {
		t.Fatalf("entity_event.processing_failed events = %d, want 1:\n%s", len(failures), logs.output())
	}
	if failures[0]["level"] != "ERROR" {
		t.Fatalf("record failure = %#v, want level ERROR", failures[0])
	}
	assertSafeRecordFailures(t, failures)
	// The delayed negative acknowledgement keeps the report pending but
	// postpones its redelivery, so Core retries once inside the production
	// retry window rather than in a hot loop, and never acknowledges it.
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 1
	})
	info, infoErr := consumer.Info(context.Background())
	if infoErr != nil {
		t.Fatal(infoErr)
	}
	if info.AckFloor.Consumer != 0 {
		t.Fatalf("AckFloor after the failed record = %#v, want no acknowledgement", info.AckFloor)
	}
	if info.NumRedelivered != 0 {
		t.Fatalf("redeliveries inside the production retry window = %d, want 0", info.NumRedelivered)
	}
	if attempts := recorder.recorded(); len(attempts) != 1 {
		t.Fatalf("recording attempts within the production retry window = %d, want 1", len(attempts))
	}
	if count := core.entityEventCount(t); count != 0 {
		t.Fatalf("rows for the failed record = %d, want 0", count)
	}
	if !running.Active() {
		t.Fatal("consumer stopped after a failed record")
	}
}

// This test protects the finite per-report failure policy against a real
// consumer with one pending slot: it fails if a report whose persisted
// descriptor Core cannot interpret blocks the next valid report, writes a row,
// is positively acknowledged, or is redelivered. The termination advisory is
// the server's own record that Core terminated rather than acknowledged the
// corrupt report, which is what distinguishes the finite policy from an
// acknowledgement that would also free the slot.
func TestEntityEventConsumerTerminatesCorruptDescriptorWithoutBlockingNextReport(t *testing.T) {
	t.Parallel()
	_, connection, js := startJetStream(t)
	consumer, err := ProvisionEntityEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	terminated, err := connection.SubscribeSync(
		"$JS.EVENT.ADVISORY.CONSUMER.MSG_TERMINATED." +
			EntityEventStreamName + "." + EntityEventConsumerName,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminated.Unsubscribe() })
	core := startCoreEntityEvents(t)
	poisonEntityID := registerCorruptEventSource(t, core)
	poisonEventID := newEntityEventID(t)
	validEventID := newEntityEventID(t)
	logs, logger := newTestLogSink()
	running, err := StartEntityEventConsumer(
		context.Background(), consumer, entityEventValidator(t), core.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	// The corrupt report is first in the stream, so it takes the only pending
	// slot. Core must terminate it before JetStream can deliver the valid
	// report behind it.
	publishEntityEventEnvelope(t, js, poisonEntityID, poisonEventID, "single_press", time.Now().UTC())
	publishEntityEventEnvelope(t, js, core.entityID, validEventID, "single_press", time.Now().UTC())
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 2 && info.NumAckPending == 0
	})
	info, infoErr := consumer.Info(context.Background())
	if infoErr != nil {
		t.Fatal(infoErr)
	}
	if info.NumRedelivered != 0 {
		t.Fatalf("redeliveries after terminating the corrupt report = %d, want 0", info.NumRedelivered)
	}

	// The valid report is the only retained row, and the corrupt report's bytes
	// remain only in the bounded stream.
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("entity event rows = %d, want only the valid report:\n%s", count, logs.output())
	}
	if row := core.entityEventRow(t, validEventID); row.disposition != "accepted" || row.rejection != "" {
		t.Fatalf("later valid row = %#v", row)
	}
	// The corrupt report is the first message of this fresh stream, and exactly
	// one termination means the valid report was acknowledged, not terminated.
	advisory, err := terminated.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatalf("termination advisory for the corrupt report: %v\n%s", err, logs.output())
	}
	var termination struct {
		StreamSeq uint64 `json:"stream_seq"`
	}
	if unmarshalErr := json.Unmarshal(advisory.Data, &termination); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if termination.StreamSeq != 1 {
		t.Fatalf("terminated stream sequence = %d, want the corrupt report (1)", termination.StreamSeq)
	}
	if _, secondErr := terminated.NextMsg(250 * time.Millisecond); secondErr == nil {
		t.Fatalf("a second report was terminated:\n%s", logs.output())
	}
	// Termination replaces redelivery, so the corrupt report produced exactly
	// one record failure even though the valid report behind it completed.
	poisonFailures := waitForRecordFailures(t, logs, poisonEventID, 1, 3*time.Second)
	if len(poisonFailures) != 1 {
		t.Fatalf("corrupt report record failures = %d, want 1:\n%s", len(poisonFailures), logs.output())
	}
	assertSafeRecordFailures(t, poisonFailures)
	if !running.Active() {
		t.Fatal("consumer stopped after terminating a corrupt report")
	}
}

// This test protects commit-before-acknowledgement and fails if JetStream is
// acknowledged while the SQLite transaction is still open.
func TestEntityEventHandlerAcknowledgesOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	recorder := &gatedEntityEventRecorder{
		inner: core.service, entered: make(chan struct{}), release: make(chan struct{}),
	}
	message := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleEntityEventMessage(context.Background(), message, validator, recorder, logger)
	}()
	select {
	case <-recorder.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("recording was never attempted")
	}
	if got := message.ackCount(); got != 0 {
		t.Fatalf("Ack calls while the transaction is open = %d, want 0", got)
	}
	if count := core.entityEventCount(t); count != 0 {
		t.Fatalf("rows before commit = %d, want 0", count)
	}
	close(recorder.release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish after the transaction committed")
	}
	if got := message.ackCount(); got != 1 {
		t.Fatalf("Ack calls after commit = %d, want 1", got)
	}
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows after commit = %d, want 1", count)
	}
	if recorded := logEvents(logs.records(t), "entity_event.recorded"); len(recorded) != 1 {
		t.Fatalf("entity_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}

	// A committed row must still be unacknowledged until the recording call
	// returns, so JetStream never advances past an uncommitted transaction.
	secondID := newEntityEventID(t)
	committed := &committedEntityEventRecorder{
		inner: core.service, entered: make(chan struct{}), release: make(chan struct{}),
	}
	second := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, secondID, core.entityID, "double_press", time.Now().UTC()),
		nil,
	)
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		handleEntityEventMessage(context.Background(), second, validator, committed, logger)
	}()
	select {
	case <-committed.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("second recording was never attempted")
	}
	if count := core.entityEventCount(t); count != 2 {
		t.Fatalf("rows after the second commit = %d, want 2", count)
	}
	if got := second.ackCount(); got != 0 {
		t.Fatalf("Ack calls before the recording call returned = %d, want 0", got)
	}
	close(committed.release)
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish after the second commit")
	}
	if got := second.ackCount(); got != 1 {
		t.Fatalf("Ack calls after the second commit = %d, want 1", got)
	}
}

// This test protects exactly-once durable recording across an acknowledgement
// loss and a Core restart, and fails if a redelivered event writes a second row
// or rewrites the first one.
func TestEntityEventHandlerSurvivesAcknowledgementLossAfterCommit(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	payload := entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC())

	handleEntityEventMessage(
		context.Background(),
		newEntityEventTestMessage(t, core.entityID, payload, errors.New("JetStream unavailable")),
		validator, core.service, logger,
	)
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows after lost acknowledgement = %d, want 1", count)
	}
	firstRow := core.entityEventRow(t, testEntityEventID)
	if firstRow.disposition != "accepted" || firstRow.rejection != "" {
		t.Fatalf("stored row = %#v", firstRow)
	}
	failures := logEvents(logs.records(t), "entity_event.processing_failed")
	if len(failures) != 1 || failures[0]["stage"] != "ack" || failures[0]["error_code"] != "ack_failed" {
		t.Fatalf("ack failure records = %#v\n%s", failures, logs.output())
	}
	if recorded := logEvents(logs.records(t), "entity_event.recorded"); len(recorded) != 1 {
		t.Fatalf("entity_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}

	// Core restarts and JetStream redelivers the same report.
	restarted := core.restart(t)
	redelivered := newEntityEventTestMessage(t, restarted.entityID, payload, nil)
	handleEntityEventMessage(
		context.Background(), redelivered, validator, restarted.service, logger,
	)
	if got := redelivered.ackCount(); got != 1 {
		t.Fatalf("redelivered Ack calls = %d, want 1", got)
	}
	if count := restarted.entityEventCount(t); count != 1 {
		t.Fatalf("rows after redelivery = %d, want 1", count)
	}
	redeliveredRow := restarted.entityEventRow(t, testEntityEventID)
	if redeliveredRow.disposition != "accepted" || redeliveredRow.rejection != "" ||
		redeliveredRow.recordedAt != firstRow.recordedAt {
		t.Fatalf("redelivery rewrote the row: %#v, want %#v", redeliveredRow, firstRow)
	}
}

// This test protects the finite per-report failure policy and fails if a
// report whose persisted descriptor Core cannot interpret is positively
// acknowledged, negatively acknowledged, or persisted as a guessed
// disposition, and if a descriptor repaired afterwards stops committing new
// input. Termination is not a repair path: the terminated report is never
// redelivered to this consumer, so only a newly published report can commit.
func TestEntityEventHandlerTerminatesCorruptDescriptorWithoutRowOrAck(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	if _, err := core.database.ExecContext(context.Background(),
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{}}`, string(core.entityID),
	); err != nil {
		t.Fatal(err)
	}
	corrupt := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)
	handleEntityEventMessage(context.Background(), corrupt, validator, core.service, logger)
	if got := corrupt.ackCount(); got != 0 {
		t.Fatalf("Ack calls for corrupt descriptor = %d, want 0", got)
	}
	if got := corrupt.termCount(); got != 1 {
		t.Fatalf("Term calls for corrupt descriptor = %d, want 1", got)
	}
	if got := corrupt.nakWithDelayCount(); got != 0 {
		t.Fatalf("delayed Nak calls for corrupt descriptor = %d, want 0", got)
	}
	if got := corrupt.plainNakCount(); got != 0 {
		t.Fatalf("immediate Nak calls for corrupt descriptor = %d, want 0", got)
	}
	if count := core.entityEventCount(t); count != 0 {
		t.Fatalf("rows for corrupt descriptor = %d, want 0", count)
	}
	failures := logEvents(logs.records(t), "entity_event.processing_failed")
	if len(failures) != 1 || failures[0]["stage"] != "record" ||
		failures[0]["error_code"] != "record_failed" ||
		failures[0]["entity_event_id"] != testEntityEventID {
		t.Fatalf("record failure records = %#v\n%s", failures, logs.output())
	}
	assertSafeRecordFailures(t, failures)

	// Repairing the descriptor commits a newly published report, not the
	// terminated one.
	if _, err := core.database.ExecContext(context.Background(),
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
		string(core.entityID),
	); err != nil {
		t.Fatal(err)
	}
	repairedID := newEntityEventID(t)
	repaired := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, repairedID, core.entityID, "double_press", time.Now().UTC()),
		nil,
	)
	handleEntityEventMessage(context.Background(), repaired, validator, core.service, logger)
	if got := repaired.ackCount(); got != 1 {
		t.Fatalf("Ack calls after repair = %d, want 1", got)
	}
	if got := repaired.termCount(); got != 0 {
		t.Fatalf("Term calls after repair = %d, want 0", got)
	}
	if got := repaired.nakWithDelayCount(); got != 0 {
		t.Fatalf("delayed Nak calls after repair = %d, want 0", got)
	}
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows after repair = %d, want 1", count)
	}
	row := core.entityEventRow(t, repairedID)
	if row.disposition != "accepted" || row.rejection != "" {
		t.Fatalf("row after repair = %#v", row)
	}
}

// This test protects the failed-Term diagnostic and fails if a Term that the
// server rejects is silent, logged with the wrong class, or logged with raw
// payload or error text.
func TestEntityEventHandlerLogsFailedTerm(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	if _, err := core.database.ExecContext(context.Background(),
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{}}`, string(core.entityID),
	); err != nil {
		t.Fatal(err)
	}
	message := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)
	message.termErr = errors.New("JetStream unavailable")
	handleEntityEventMessage(context.Background(), message, validator, core.service, logger)
	if got := message.ackCount(); got != 0 {
		t.Fatalf("Ack calls after a failed Term = %d, want 0", got)
	}
	if got := message.termCount(); got != 1 {
		t.Fatalf("Term calls = %d, want 1", got)
	}
	if got := message.nakWithDelayCount(); got != 0 {
		t.Fatalf("delayed Nak calls after a failed Term = %d, want 0", got)
	}
	if count := core.entityEventCount(t); count != 0 {
		t.Fatalf("rows for a terminated report = %d, want 0", count)
	}
	failures := logEvents(logs.records(t), "entity_event.processing_failed")
	if len(failures) != 2 {
		t.Fatalf("entity_event.processing_failed events = %d, want 2:\n%s", len(failures), logs.output())
	}
	stages := map[string]string{}
	for _, failure := range failures {
		stage, _ := failure["stage"].(string)
		code, _ := failure["error_code"].(string)
		stages[stage] = code
		for _, key := range []string{"subject", "error"} {
			if _, present := failure[key]; present {
				t.Fatalf("failed term logs unsafe %q (record = %#v)", key, failure)
			}
		}
	}
	if stages["record"] != "record_failed" || stages["term"] != "term_failed" {
		t.Fatalf("processing failure stages = %#v, want record=record_failed, term=term_failed:\n%s",
			stages, logs.output())
	}
}

// This test protects the transient record-failure policy and fails if a report
// Core could not record for a storage reason is acknowledged, terminated,
// redelivered immediately, or redelivered with any wait other than the named
// bounded delay, and if a negative acknowledgement that itself fails is silent,
// logged with the wrong class, or logged with raw payload or error text.
func TestEntityEventHandlerDelaysFailedRecordAndLogsFailedNak(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	recorder := &testEntityEventRecorder{
		failFor: devices.EntityEventID(testEntityEventID),
		result:  devices.EntityEventRecordResult{Outcome: devices.EntityEventOutcomeAccepted},
	}
	message := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)
	message.nakErr = errors.New("JetStream unavailable")
	handleEntityEventMessage(context.Background(), message, validator, recorder, logger)
	if got := message.ackCount(); got != 0 {
		t.Fatalf("Ack calls for a failed record = %d, want 0", got)
	}
	if got := message.termCount(); got != 0 {
		t.Fatalf("Term calls for a transient failure = %d, want 0", got)
	}
	if got := message.plainNakCount(); got != 0 {
		t.Fatalf("immediate Nak calls for a failed record = %d, want 0", got)
	}
	if got := message.nakWithDelayCount(); got != 1 {
		t.Fatalf("delayed Nak calls for a failed record = %d, want 1", got)
	}
	if got := message.nakDelayRequested(); got != EntityEventRedeliveryDelay {
		t.Fatalf("redelivery delay = %v, want %v", got, EntityEventRedeliveryDelay)
	}
	failures := logEvents(logs.records(t), "entity_event.processing_failed")
	if len(failures) != 2 {
		t.Fatalf("entity_event.processing_failed events = %d, want 2:\n%s", len(failures), logs.output())
	}
	stages := map[string]string{}
	for _, failure := range failures {
		stage, _ := failure["stage"].(string)
		code, _ := failure["error_code"].(string)
		stages[stage] = code
		for _, key := range []string{"subject", "error"} {
			if _, present := failure[key]; present {
				t.Fatalf("failed record logs unsafe %q (record = %#v)", key, failure)
			}
		}
	}
	if stages["record"] != "record_failed" || stages["nak"] != "nak_failed" {
		t.Fatalf("processing failure stages = %#v, want record=record_failed, nak=nak_failed:\n%s",
			stages, logs.output())
	}
}

// This test protects prompt acknowledgement and fails if a slow log
// destination delays the Ack for a committed Entity Event, or if the committed
// disposition diagnostic is lost.
func TestEntityEventHandlerAcknowledgesBeforeRecordedLogsUnderBlockedWriter(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	message := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)
	logs, logger, entered, release := newBlockingObservationSink()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleEntityEventMessage(
			context.Background(), message, validator, core.service, logger,
		)
	}()
	select {
	case <-message.acked:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("Ack was not attempted while recorded diagnostics were blocked")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("blocked recorded diagnostic was never attempted")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish after the log writer was released")
	}
	if got := message.ackCount(); got != 1 {
		t.Fatalf("Ack calls = %d, want 1", got)
	}
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
	recorded := logEvents(logs.records(t), "entity_event.recorded")
	if len(recorded) != 1 {
		t.Fatalf("entity_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}
}

// This test protects identity-conflict logging and fails if a changed tuple for
// a known event ID is acknowledged without the diagnostic, or is recorded.
func TestEntityEventHandlerLogsIdentityConflictWithoutRecording(t *testing.T) {
	t.Parallel()
	core := startCoreEntityEvents(t)
	validator := entityEventValidator(t)
	logs, logger := newTestLogSink()
	emittedAt := time.Now().UTC()
	handleEntityEventMessage(
		context.Background(),
		newEntityEventTestMessage(
			t, core.entityID,
			entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", emittedAt),
			nil,
		),
		validator, core.service, logger,
	)
	changed := newEntityEventTestMessage(
		t, core.entityID,
		entityEventEnvelope(t, testEntityEventID, core.entityID, "double_press", emittedAt),
		nil,
	)
	handleEntityEventMessage(context.Background(), changed, validator, core.service, logger)
	if got := changed.ackCount(); got != 1 {
		t.Fatalf("conflict Ack calls = %d, want 1", got)
	}
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows after conflict = %d, want 1", count)
	}
	conflicts := logEvents(logs.records(t), "entity_event.identity_conflict")
	if len(conflicts) != 1 || conflicts[0]["level"] != "WARN" ||
		conflicts[0]["entity_event_id"] != testEntityEventID ||
		conflicts[0]["name"] != "double_press" {
		t.Fatalf("identity conflict records = %#v\n%s", conflicts, logs.output())
	}
}

// This test protects wire identity mapping and fails if a non-canonical ID
// becomes trusted domain input.
func TestDomainEntityEventRequiresCanonicalIdentities(t *testing.T) {
	t.Parallel()
	emittedAt := time.Now().UTC()
	valid := natswire.Envelope[entityEvent]{
		ID: testEntityEventID, CorrelationID: testCorrelationID,
		Data: entityEvent{EntityID: testEntityID, Name: "single_press"},
	}
	mapped, err := domainEntityEvent(valid, emittedAt)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.ID != devices.EntityEventID(testEntityEventID) ||
		mapped.EntityID != devices.EntityID(testEntityID) ||
		mapped.Name != devices.EntityEventName("single_press") ||
		mapped.CorrelationID != devices.CorrelationID(testCorrelationID) ||
		!mapped.EmittedAt.Equal(emittedAt) {
		t.Fatalf("mapped event = %#v", mapped)
	}

	invalid := []struct {
		name   string
		mutate func(*natswire.Envelope[entityEvent])
	}{
		{"event ID", func(envelope *natswire.Envelope[entityEvent]) { envelope.ID = "evt_nope" }},
		{"entity ID", func(envelope *natswire.Envelope[entityEvent]) { envelope.Data.EntityID = "nope" }},
		{"correlation", func(envelope *natswire.Envelope[entityEvent]) { envelope.CorrelationID = "cor_nope" }},
	}
	for _, test := range invalid {
		envelope := valid
		test.mutate(&envelope)
		if _, mappingErr := domainEntityEvent(envelope, emittedAt); mappingErr == nil {
			t.Fatalf("%s mapping error = nil, want validation failure", test.name)
		}
	}
}
