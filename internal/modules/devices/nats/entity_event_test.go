package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
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
	_ adapters.RuntimeID,
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
	runtimeID adapters.RuntimeID,
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
	runtimeID adapters.RuntimeID,
	event devices.EntityEvent,
	receivedAt time.Time,
) (devices.EntityEventRecordResult, error) {
	result, err := recorder.inner.RecordEntityEvent(ctx, adapterID, runtimeID, event, receivedAt)
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return result, err
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

// This test protects wire identity mapping and fails if a non-canonical ID
// becomes trusted domain input.
func TestDomainEntityEventRequiresCanonicalIdentities(t *testing.T) {
	t.Parallel()
	emittedAt := time.Now().UTC()
	valid := natswire.Envelope[entityEvent]{
		ID: testEntityEventID, CorrelationID: testCorrelationID,
		Data: entityEvent{EntityID: testEntityID, Name: "single_press"},
	}
	trace := devices.DeviceFactTraceContext{Traceparent: testFactTraceparent, Tracestate: testFactTracestate}
	mapped, err := domainEntityEvent(valid, emittedAt, trace)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.ID != devices.EntityEventID(testEntityEventID) ||
		mapped.EntityID != devices.EntityID(testEntityID) ||
		mapped.Name != devices.EntityEventName("single_press") ||
		mapped.CorrelationID != devices.CorrelationID(testCorrelationID) ||
		!mapped.EmittedAt.Equal(emittedAt) ||
		mapped.Trace != trace {
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
		if _, mappingErr := domainEntityEvent(
			envelope, emittedAt, devices.DeviceFactTraceContext{},
		); mappingErr == nil {
			t.Fatalf("%s mapping error = nil, want validation failure", test.name)
		}
	}
}
