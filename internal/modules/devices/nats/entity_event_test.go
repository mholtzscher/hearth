package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"database/sql"
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

// This test protects redeliverability and fails if an infrastructure failure is
// acknowledged or partially persisted.
func TestEntityEventConsumerLeavesRecordFailuresUnacknowledged(t *testing.T) {
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
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 1
	})
	if info, infoErr := consumer.Info(context.Background()); infoErr != nil ||
		info.AckFloor.Consumer != 0 {
		t.Fatalf("consumer info = %#v, %v", info, infoErr)
	}
	deadline := time.Now().Add(3 * time.Second)
	var failures []map[string]any
	for time.Now().Before(deadline) {
		failures = logEvents(logs.records(t), "entity_event.processing_failed")
		if len(failures) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(failures) != 1 {
		t.Fatalf("entity_event.processing_failed events = %d, want 1:\n%s", len(failures), logs.output())
	}
	if failures[0]["level"] != "ERROR" || failures[0]["stage"] != "record" ||
		failures[0]["error_code"] != "record_failed" ||
		failures[0]["entity_event_id"] != testEntityEventID {
		t.Fatalf("record failure = %#v", failures[0])
	}
	for _, key := range []string{"subject", "error"} {
		if _, present := failures[0][key]; present {
			t.Fatalf("record failure logs unsafe %q (record = %#v)", key, failures[0])
		}
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
	handleEntityEventMessage(context.Background(), redelivered, validator, restarted.service, logger)
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

// This test protects descriptor-corruption handling and fails if a corrupt
// persisted descriptor is acknowledged, persisted as a guessed disposition, or
// permanently blocks valid input after repair.
func TestEntityEventHandlerLeavesDescriptorCorruptionUnacknowledged(t *testing.T) {
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
	payload := entityEventEnvelope(t, testEntityEventID, core.entityID, "single_press", time.Now().UTC())
	corrupt := newEntityEventTestMessage(t, core.entityID, payload, nil)
	handleEntityEventMessage(context.Background(), corrupt, validator, core.service, logger)
	if got := corrupt.ackCount(); got != 0 {
		t.Fatalf("Ack calls for corrupt descriptor = %d, want 0", got)
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

	// Repairing the descriptor lets the redelivered report commit and ack.
	if _, err := core.database.ExecContext(context.Background(),
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
		string(core.entityID),
	); err != nil {
		t.Fatal(err)
	}
	repaired := newEntityEventTestMessage(t, core.entityID, payload, nil)
	handleEntityEventMessage(context.Background(), repaired, validator, core.service, logger)
	if got := repaired.ackCount(); got != 1 {
		t.Fatalf("Ack calls after repair = %d, want 1", got)
	}
	if count := core.entityEventCount(t); count != 1 {
		t.Fatalf("rows after repair = %d, want 1", count)
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
		handleEntityEventMessage(context.Background(), message, validator, core.service, logger)
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
