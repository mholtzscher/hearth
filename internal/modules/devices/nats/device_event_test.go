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
	testDeviceEventID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testOtherEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ac"
)

type testDeviceEventRecorder struct {
	mutex   sync.Mutex
	events  []devices.DeviceEvent
	result  devices.DeviceEventRecordResult
	err     error
	failFor devices.DeviceEventID
}

func (recorder *testDeviceEventRecorder) RecordDeviceEvent(
	_ context.Context,
	_ string,
	_ devices.RuntimeID,
	event devices.DeviceEvent,
	_ time.Time,
) (devices.DeviceEventRecordResult, error) {
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mutex.Unlock()
	if recorder.failFor != "" && event.ID == recorder.failFor {
		return devices.DeviceEventRecordResult{}, errors.New("temporary SQLite failure")
	}
	return recorder.result, recorder.err
}

func (recorder *testDeviceEventRecorder) recorded() []devices.DeviceEvent {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]devices.DeviceEvent(nil), recorder.events...)
}

// gatedDeviceEventRecorder blocks inside the record call so a test can prove
// JetStream acknowledgement waits for the commit.
type gatedDeviceEventRecorder struct {
	inner   DeviceEventRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (recorder *gatedDeviceEventRecorder) RecordDeviceEvent(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	event devices.DeviceEvent,
	receivedAt time.Time,
) (devices.DeviceEventRecordResult, error) {
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return recorder.inner.RecordDeviceEvent(ctx, adapterID, runtimeID, event, receivedAt)
}

// committedDeviceEventRecorder commits the row and then blocks, so a test can
// prove acknowledgement follows the commit rather than the call boundary.
type committedDeviceEventRecorder struct {
	inner   DeviceEventRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (recorder *committedDeviceEventRecorder) RecordDeviceEvent(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	event devices.DeviceEvent,
	receivedAt time.Time,
) (devices.DeviceEventRecordResult, error) {
	result, err := recorder.inner.RecordDeviceEvent(ctx, adapterID, runtimeID, event, receivedAt)
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return result, err
}

// coreDeviceEvents is a real SQLite-backed Core stack: one registered event
// source Entity, the devices service, and no HTTP or automation layer.
type coreDeviceEvents struct {
	database   *sql.DB
	path       string
	repository *devices.SQLiteRepository
	service    *devices.Service
	entityID   devices.EntityID
}

func openCoreDeviceEvents(t *testing.T, path string) *coreDeviceEvents {
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
	core := &coreDeviceEvents{
		database: database, path: path, repository: repository,
		service: devices.NewService(devices.SQLiteStores(repository), nil, catalog, devices.Dependencies{}),
	}
	core.entityID = core.register(t)
	return core
}

func startCoreDeviceEvents(t *testing.T) *coreDeviceEvents {
	t.Helper()
	return openCoreDeviceEvents(t, filepath.Join(t.TempDir(), "hearth.db"))
}

func (core *coreDeviceEvents) register(t *testing.T) devices.EntityID {
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
func (core *coreDeviceEvents) restart(t *testing.T) *coreDeviceEvents {
	t.Helper()
	if err := core.database.Close(); err != nil {
		t.Fatal(err)
	}
	return openCoreDeviceEvents(t, core.path)
}

func (core *coreDeviceEvents) deviceEventCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := core.database.QueryRowContext(
		context.Background(), "SELECT count(*) FROM device_events",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (core *coreDeviceEvents) deviceEventRow(t *testing.T, eventID string) storedDeviceEventRow {
	t.Helper()
	var row storedDeviceEventRow
	var rejection sql.NullString
	err := core.database.QueryRowContext(context.Background(), `
		SELECT disposition, rejection_code, recorded_at
		FROM device_events
		WHERE event_id = ?`, eventID).
		Scan(&row.disposition, &rejection, &row.recordedAt)
	if err != nil {
		t.Fatalf("read device event %s: %v", eventID, err)
	}
	row.rejection = rejection.String
	return row
}

type storedDeviceEventRow struct {
	disposition string
	rejection   string
	recordedAt  string
}

func deviceEventValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func deviceEventEnvelope(
	t *testing.T,
	eventID string,
	entityID devices.EntityID,
	name string,
	emittedAt time.Time,
) []byte {
	t.Helper()
	payload, err := natswire.Encode(deviceEventValidator(t), contractsv1.DeviceEventSchemaID,
		natswire.Envelope[deviceEvent]{
			ID: eventID, Schema: contractsv1.DeviceEventSchemaID,
			EmittedAt:     emittedAt.UTC().Format(time.RFC3339Nano),
			CorrelationID: testCorrelationID,
			Data:          deviceEvent{EntityID: string(entityID), Name: name},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// publishDeviceEventMessage publishes one raw payload with explicit headers and
// subject, so tests can spoof identity, route, and causation.
func publishDeviceEventMessage(
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

func publishDeviceEventEnvelope(
	t *testing.T,
	js jetstream.JetStream,
	entityID devices.EntityID,
	eventID, name string,
	emittedAt time.Time,
) {
	t.Helper()
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{eventID}}
	natswire.InjectTrace(testTraceContext(t), headers)
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, entityID),
		headers, deviceEventEnvelope(t, eventID, entityID, name, emittedAt),
	)
}

func mustDeviceEventSubject(t *testing.T, entityID devices.EntityID) string {
	t.Helper()
	subject, err := natswire.DeviceEventSubject("simulator", testRuntimeID, string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// newDeviceEventTestMessage builds one JetStream message whose MsgId header
// agrees with its envelope, exactly as the SDK publishes it. Tests that need a
// mismatch or a missing header publish raw messages instead.
func newDeviceEventTestMessage(
	t *testing.T,
	entityID devices.EntityID,
	payload []byte,
	ackErr error,
) *ackOrderingTestMessage {
	t.Helper()
	envelope, err := natswire.Decode[deviceEvent](
		deviceEventValidator(t), contractsv1.DeviceEventSchemaID, payload,
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
		subject: mustDeviceEventSubject(t, entityID),
		acked:   make(chan struct{}),
		ackErr:  ackErr,
	}
}

func newDeviceEventID(t *testing.T) string {
	t.Helper()
	id, err := devices.NewDeviceEventID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

// This test protects the committed recording path and fails if a broker
// acknowledged Device Event is not persisted, is not acknowledged, or loses
// its reported identity.
func TestDeviceEventConsumerRecordsCommittedInputAndAcknowledges(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionDeviceEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreDeviceEvents(t)
	logs, logger := newTestLogSink()
	running, err := StartDeviceEventConsumer(
		context.Background(), consumer, deviceEventValidator(t), core.service, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)
	if !running.Active() {
		t.Fatal("device event consumer is not active")
	}

	emittedAt := time.Now().UTC().Add(2 * time.Minute)
	publishDeviceEventEnvelope(t, js, core.entityID, testDeviceEventID, "single_press", emittedAt)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 1 && info.NumAckPending == 0
	})
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("device event rows = %d, want 1", count)
	}
	firstRow := core.deviceEventRow(t, testDeviceEventID)
	if firstRow.disposition != "accepted" || firstRow.rejection != "" || firstRow.recordedAt == "" {
		t.Fatalf("stored row = %#v", firstRow)
	}
	recorded := logEvents(logs.records(t), "device_event.recorded")
	if len(recorded) != 1 {
		t.Fatalf("device_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}
	if recorded[0]["device_event_id"] != testDeviceEventID ||
		recorded[0]["entity_id"] != string(core.entityID) ||
		recorded[0]["name"] != "single_press" ||
		recorded[0]["outcome"] != "accepted" {
		t.Fatalf("recorded record = %#v", recorded[0])
	}
	if failures := logEvents(logs.records(t), "device_event.processing_failed"); len(failures) != 0 {
		t.Fatalf("device_event.processing_failed events = %d, want 0:\n%s", len(failures), logs.output())
	}
	// Skew is diagnostic only: the report stays recorded with no age gate.
	skew := logEvents(logs.records(t), "device_event.clock_skew")
	if len(skew) != 1 || skew[0]["level"] != "WARN" ||
		skew[0]["device_event_id"] != testDeviceEventID {
		t.Fatalf("clock skew records = %#v\n%s", skew, logs.output())
	}
	if validationErr := ValidateDeviceEventResources(context.Background(), js); validationErr != nil {
		t.Fatal(validationErr)
	}

	running.Stop()
	select {
	case <-running.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("device event consumer did not stop")
	}
	if running.Active() {
		t.Fatal("stopped device event consumer remains active")
	}
}

// This test protects permanent wire classification and fails if malformed,
// misrouted, spoofed, or caused input reaches recording, writes a row, blocks
// acknowledgement, exposes raw payloads in logs, or stops valid input behind it.
//
//nolint:gocognit // One flow keeps every permanent wire class and its acknowledgement evidence together.
func TestDeviceEventConsumerAcknowledgesPermanentWireErrorsWithoutRows(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionDeviceEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreDeviceEvents(t)
	logs, logger := newTestLogSink()
	recorder := &testDeviceEventRecorder{}
	running, err := StartDeviceEventConsumer(
		context.Background(), consumer, deviceEventValidator(t), recorder, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	// Malformed payload.
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{newDeviceEventID(t)}}, []byte(`{}`),
	)

	// Spoofed MsgId: a valid envelope whose header identity disagrees.
	spoofedHeaderID := newDeviceEventID(t)
	spoofedPayloadID := newDeviceEventID(t)
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{spoofedHeaderID}},
		deviceEventEnvelope(t, spoofedPayloadID, core.entityID, "single_press", time.Now().UTC()),
	)

	// Route mismatch: the subject names another Entity than the payload.
	routedID := newDeviceEventID(t)
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, devices.EntityID(testOtherEntityID)),
		natsgo.Header{natsgo.MsgIdHdr: []string{routedID}},
		deviceEventEnvelope(t, routedID, core.entityID, "single_press", time.Now().UTC()),
	)

	// Missing MsgId: without header identity Core cannot form trustworthy
	// domain input, so the report is permanent and never recorded.
	missingIDHeader := newDeviceEventID(t)
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, core.entityID),
		natsgo.Header{},
		deviceEventEnvelope(t, missingIDHeader, core.entityID, "single_press", time.Now().UTC()),
	)

	// Unexpected causation: a report never carries causation, and the strict
	// envelope rejects the field as wire-invalid.
	causedID := newDeviceEventID(t)
	caused := `{"id":"` + causedID + `",` +
		`"schema":"urn:hearth:schema:device-event:v1",` +
		`"emitted_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `",` +
		`"correlation_id":"` + testCorrelationID + `",` +
		`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"data":{"entity_id":"` + string(core.entityID) + `","name":"single_press"}}`
	publishDeviceEventMessage(
		t, js, mustDeviceEventSubject(t, core.entityID),
		natsgo.Header{natsgo.MsgIdHdr: []string{causedID}}, []byte(caused),
	)

	// A permanent rejection must not block valid input behind it.
	publishDeviceEventEnvelope(t, js, core.entityID, testDeviceEventID, "double_press", time.Now().UTC())
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 6 && info.NumAckPending == 0
	})

	recorded := recorder.recorded()
	if len(recorded) != 1 || string(recorded[0].ID) != testDeviceEventID {
		t.Fatalf("recorded events = %#v, want only the valid report", recorded)
	}
	if core.deviceEventCount(t) != 0 {
		t.Fatalf("rows written by a fake recorder = %d, want 0", core.deviceEventCount(t))
	}
	invalid := logEvents(logs.records(t), "device_event.invalid")
	if len(invalid) != 5 {
		t.Fatalf("device_event.invalid events = %d, want 5:\n%s", len(invalid), logs.output())
	}
	wantCodes := map[string]bool{
		"device_event_decode_failed":   false,
		"device_event_msg_id_mismatch": false,
		"device_event_entity_mismatch": false,
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
func TestDeviceEventConsumerLeavesRecordFailuresUnacknowledged(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionDeviceEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	core := startCoreDeviceEvents(t)
	logs, logger := newTestLogSink()
	recorder := &testDeviceEventRecorder{
		failFor: devices.DeviceEventID(testDeviceEventID),
		result:  devices.DeviceEventRecordResult{Outcome: devices.DeviceEventOutcomeAccepted},
	}
	running, err := StartDeviceEventConsumer(
		context.Background(), consumer, deviceEventValidator(t), recorder, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	publishDeviceEventEnvelope(t, js, core.entityID, testDeviceEventID, "single_press", time.Now().UTC())
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
		failures = logEvents(logs.records(t), "device_event.processing_failed")
		if len(failures) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(failures) != 1 {
		t.Fatalf("device_event.processing_failed events = %d, want 1:\n%s", len(failures), logs.output())
	}
	if failures[0]["level"] != "ERROR" || failures[0]["stage"] != "record" ||
		failures[0]["error_code"] != "record_failed" ||
		failures[0]["device_event_id"] != testDeviceEventID {
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
func TestDeviceEventHandlerAcknowledgesOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	core := startCoreDeviceEvents(t)
	validator := deviceEventValidator(t)
	logs, logger := newTestLogSink()
	recorder := &gatedDeviceEventRecorder{
		inner: core.service, entered: make(chan struct{}), release: make(chan struct{}),
	}
	message := newDeviceEventTestMessage(
		t, core.entityID,
		deviceEventEnvelope(t, testDeviceEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleDeviceEventMessage(context.Background(), message, validator, recorder, logger)
	}()
	select {
	case <-recorder.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("recording was never attempted")
	}
	if got := message.ackCount(); got != 0 {
		t.Fatalf("Ack calls while the transaction is open = %d, want 0", got)
	}
	if count := core.deviceEventCount(t); count != 0 {
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
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("rows after commit = %d, want 1", count)
	}
	if recorded := logEvents(logs.records(t), "device_event.recorded"); len(recorded) != 1 {
		t.Fatalf("device_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}

	// A committed row must still be unacknowledged until the recording call
	// returns, so JetStream never advances past an uncommitted transaction.
	secondID := newDeviceEventID(t)
	committed := &committedDeviceEventRecorder{
		inner: core.service, entered: make(chan struct{}), release: make(chan struct{}),
	}
	second := newDeviceEventTestMessage(
		t, core.entityID,
		deviceEventEnvelope(t, secondID, core.entityID, "double_press", time.Now().UTC()),
		nil,
	)
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		handleDeviceEventMessage(context.Background(), second, validator, committed, logger)
	}()
	select {
	case <-committed.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("second recording was never attempted")
	}
	if count := core.deviceEventCount(t); count != 2 {
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
func TestDeviceEventHandlerSurvivesAcknowledgementLossAfterCommit(t *testing.T) {
	t.Parallel()
	core := startCoreDeviceEvents(t)
	validator := deviceEventValidator(t)
	logs, logger := newTestLogSink()
	payload := deviceEventEnvelope(t, testDeviceEventID, core.entityID, "single_press", time.Now().UTC())

	handleDeviceEventMessage(
		context.Background(),
		newDeviceEventTestMessage(t, core.entityID, payload, errors.New("JetStream unavailable")),
		validator, core.service, logger,
	)
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("rows after lost acknowledgement = %d, want 1", count)
	}
	firstRow := core.deviceEventRow(t, testDeviceEventID)
	if firstRow.disposition != "accepted" || firstRow.rejection != "" {
		t.Fatalf("stored row = %#v", firstRow)
	}
	failures := logEvents(logs.records(t), "device_event.processing_failed")
	if len(failures) != 1 || failures[0]["stage"] != "ack" || failures[0]["error_code"] != "ack_failed" {
		t.Fatalf("ack failure records = %#v\n%s", failures, logs.output())
	}
	if recorded := logEvents(logs.records(t), "device_event.recorded"); len(recorded) != 1 {
		t.Fatalf("device_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}

	// Core restarts and JetStream redelivers the same report.
	restarted := core.restart(t)
	redelivered := newDeviceEventTestMessage(t, restarted.entityID, payload, nil)
	handleDeviceEventMessage(context.Background(), redelivered, validator, restarted.service, logger)
	if got := redelivered.ackCount(); got != 1 {
		t.Fatalf("redelivered Ack calls = %d, want 1", got)
	}
	if count := restarted.deviceEventCount(t); count != 1 {
		t.Fatalf("rows after redelivery = %d, want 1", count)
	}
	redeliveredRow := restarted.deviceEventRow(t, testDeviceEventID)
	if redeliveredRow.disposition != "accepted" || redeliveredRow.rejection != "" ||
		redeliveredRow.recordedAt != firstRow.recordedAt {
		t.Fatalf("redelivery rewrote the row: %#v, want %#v", redeliveredRow, firstRow)
	}
}

// This test protects descriptor-corruption handling and fails if a corrupt
// persisted descriptor is acknowledged, persisted as a guessed disposition, or
// permanently blocks valid input after repair.
func TestDeviceEventHandlerLeavesDescriptorCorruptionUnacknowledged(t *testing.T) {
	t.Parallel()
	core := startCoreDeviceEvents(t)
	validator := deviceEventValidator(t)
	logs, logger := newTestLogSink()
	if _, err := core.database.ExecContext(context.Background(),
		"UPDATE entities SET support_json = ? WHERE id = ?",
		`{"state":{},"operations":{}}`, string(core.entityID),
	); err != nil {
		t.Fatal(err)
	}
	payload := deviceEventEnvelope(t, testDeviceEventID, core.entityID, "single_press", time.Now().UTC())
	corrupt := newDeviceEventTestMessage(t, core.entityID, payload, nil)
	handleDeviceEventMessage(context.Background(), corrupt, validator, core.service, logger)
	if got := corrupt.ackCount(); got != 0 {
		t.Fatalf("Ack calls for corrupt descriptor = %d, want 0", got)
	}
	if count := core.deviceEventCount(t); count != 0 {
		t.Fatalf("rows for corrupt descriptor = %d, want 0", count)
	}
	failures := logEvents(logs.records(t), "device_event.processing_failed")
	if len(failures) != 1 || failures[0]["stage"] != "record" ||
		failures[0]["error_code"] != "record_failed" ||
		failures[0]["device_event_id"] != testDeviceEventID {
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
	repaired := newDeviceEventTestMessage(t, core.entityID, payload, nil)
	handleDeviceEventMessage(context.Background(), repaired, validator, core.service, logger)
	if got := repaired.ackCount(); got != 1 {
		t.Fatalf("Ack calls after repair = %d, want 1", got)
	}
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("rows after repair = %d, want 1", count)
	}
}

// This test protects prompt acknowledgement and fails if a slow log
// destination delays the Ack for a committed Device Event, or if the committed
// disposition diagnostic is lost.
func TestDeviceEventHandlerAcknowledgesBeforeRecordedLogsUnderBlockedWriter(t *testing.T) {
	t.Parallel()
	core := startCoreDeviceEvents(t)
	validator := deviceEventValidator(t)
	message := newDeviceEventTestMessage(
		t, core.entityID,
		deviceEventEnvelope(t, testDeviceEventID, core.entityID, "single_press", time.Now().UTC()),
		nil,
	)
	logs, logger, entered, release := newBlockingObservationSink()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleDeviceEventMessage(context.Background(), message, validator, core.service, logger)
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
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
	recorded := logEvents(logs.records(t), "device_event.recorded")
	if len(recorded) != 1 {
		t.Fatalf("device_event.recorded events = %d, want 1:\n%s", len(recorded), logs.output())
	}
}

// This test protects identity-conflict logging and fails if a changed tuple for
// a known event ID is acknowledged without the diagnostic, or is recorded.
func TestDeviceEventHandlerLogsIdentityConflictWithoutRecording(t *testing.T) {
	t.Parallel()
	core := startCoreDeviceEvents(t)
	validator := deviceEventValidator(t)
	logs, logger := newTestLogSink()
	emittedAt := time.Now().UTC()
	handleDeviceEventMessage(
		context.Background(),
		newDeviceEventTestMessage(
			t, core.entityID,
			deviceEventEnvelope(t, testDeviceEventID, core.entityID, "single_press", emittedAt),
			nil,
		),
		validator, core.service, logger,
	)
	changed := newDeviceEventTestMessage(
		t, core.entityID,
		deviceEventEnvelope(t, testDeviceEventID, core.entityID, "double_press", emittedAt),
		nil,
	)
	handleDeviceEventMessage(context.Background(), changed, validator, core.service, logger)
	if got := changed.ackCount(); got != 1 {
		t.Fatalf("conflict Ack calls = %d, want 1", got)
	}
	if count := core.deviceEventCount(t); count != 1 {
		t.Fatalf("rows after conflict = %d, want 1", count)
	}
	conflicts := logEvents(logs.records(t), "device_event.identity_conflict")
	if len(conflicts) != 1 || conflicts[0]["level"] != "WARN" ||
		conflicts[0]["device_event_id"] != testDeviceEventID ||
		conflicts[0]["name"] != "double_press" {
		t.Fatalf("identity conflict records = %#v\n%s", conflicts, logs.output())
	}
}

// This test protects wire identity mapping and fails if a non-canonical ID
// becomes trusted domain input.
func TestDomainDeviceEventRequiresCanonicalIdentities(t *testing.T) {
	t.Parallel()
	emittedAt := time.Now().UTC()
	valid := natswire.Envelope[deviceEvent]{
		ID: testDeviceEventID, CorrelationID: testCorrelationID,
		Data: deviceEvent{EntityID: testEntityID, Name: "single_press"},
	}
	mapped, err := domainDeviceEvent(valid, emittedAt)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.ID != devices.DeviceEventID(testDeviceEventID) ||
		mapped.EntityID != devices.EntityID(testEntityID) ||
		mapped.Name != devices.DeviceEventName("single_press") ||
		mapped.CorrelationID != devices.CorrelationID(testCorrelationID) ||
		!mapped.EmittedAt.Equal(emittedAt) {
		t.Fatalf("mapped event = %#v", mapped)
	}

	invalid := []struct {
		name   string
		mutate func(*natswire.Envelope[deviceEvent])
	}{
		{"event ID", func(envelope *natswire.Envelope[deviceEvent]) { envelope.ID = "evt_nope" }},
		{"entity ID", func(envelope *natswire.Envelope[deviceEvent]) { envelope.Data.EntityID = "nope" }},
		{"correlation", func(envelope *natswire.Envelope[deviceEvent]) { envelope.CorrelationID = "cor_nope" }},
	}
	for _, test := range invalid {
		envelope := valid
		test.mutate(&envelope)
		if _, mappingErr := domainDeviceEvent(envelope, emittedAt); mappingErr == nil {
			t.Fatalf("%s mapping error = nil, want validation failure", test.name)
		}
	}
}
