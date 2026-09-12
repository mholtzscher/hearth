package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// factEnvelope is the generic envelope shape every Device Fact shares; the
// family-specific data stays raw so each assertion decodes only what it checks.
type factEnvelope struct {
	ID            string          `json:"id"`
	Schema        string          `json:"schema"`
	EmittedAt     string          `json:"emitted_at"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Data          json.RawMessage `json:"data"`
}

// deviceFactFixture is one fully assembled fact transport: two connections, one
// epoch fence, one dispatcher and one external subscriber.
type deviceFactFixture struct {
	server     *natsserver.Server
	ingest     *natsgo.Conn
	publish    *natsgo.Conn
	subscriber *natsgo.Conn
	epochs     *DeviceFactEpochs
	dispatcher *DeviceFactDispatcher
	validator  *contractsv1.Validator
	facts      chan *natsgo.Msg
	logs       *lockedTestLogWriter
	logger     *slog.Logger
}

func newDeviceFactFixture(t *testing.T) *deviceFactFixture {
	t.Helper()
	server := startDeviceFactServer(t, -1)
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(t, server.ClientURL(), DeviceFactConnectionOptions()...)
	subscriber := connectDeviceFactClient(t, server.ClientURL())
	facts := make(chan *natsgo.Msg, 1024)
	if _, err := subscriber.Subscribe(natswire.DeviceFactWildcard(), func(message *natsgo.Msg) {
		facts <- message
	}); err != nil {
		t.Fatal(err)
	}
	if err := subscriber.Flush(); err != nil {
		t.Fatal(err)
	}
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	logs, logger := newTestLogSink()
	dispatcher, err := StartDeviceFactDispatcher(publish, validator, epochs, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDrain()
		_ = dispatcher.Drain(drainContext)
	})
	return &deviceFactFixture{
		server: server, ingest: ingest, publish: publish, subscriber: subscriber,
		epochs: epochs, dispatcher: dispatcher, validator: validator, facts: facts,
		logs: logs, logger: logger,
	}
}

// nextFact reads one published fact within a bounded wait.
func (fixture *deviceFactFixture) nextFact(t *testing.T) *natsgo.Msg {
	t.Helper()
	select {
	case message := <-fixture.facts:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a Device Fact")
		return nil
	}
}

// assertNoFact fails when any fact arrives inside a short bounded window.
func (fixture *deviceFactFixture) assertNoFact(t *testing.T) {
	t.Helper()
	select {
	case message := <-fixture.facts:
		t.Fatalf("unexpected Device Fact on %q", message.Subject)
	case <-time.After(200 * time.Millisecond):
	}
}

func decodeFact(
	t *testing.T,
	validator *contractsv1.Validator,
	message *natsgo.Msg,
) (factEnvelope, natswire.DeviceFactRoute) {
	t.Helper()
	route, err := natswire.ParseDeviceFactSubject(message.Subject)
	if err != nil {
		t.Fatalf("parse fact subject: %v", err)
	}
	schemaID := factSchemaID(t, route.Family)
	if validateErr := validator.Validate(schemaID, message.Data); validateErr != nil {
		t.Fatalf("fact payload is not schema-valid: %v", validateErr)
	}
	var envelope factEnvelope
	if unmarshalErr := json.Unmarshal(message.Data, &envelope); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if envelope.Schema != schemaID {
		t.Fatalf("fact schema = %q, want %q", envelope.Schema, schemaID)
	}
	if _, idErr := devices.ParseDeviceFactID(envelope.ID); idErr != nil {
		t.Fatalf("fact ID %q is not canonical: %v", envelope.ID, idErr)
	}
	return envelope, route
}

func factSchemaID(t *testing.T, family natswire.DeviceFactFamily) string {
	t.Helper()
	switch family {
	case natswire.DeviceFactFamilyObservation:
		return contractsv1.ObservationFactSchemaID
	case natswire.DeviceFactFamilyEntityEvent:
		return contractsv1.EntityEventFactSchemaID
	case natswire.DeviceFactFamilyCommand:
		return contractsv1.CommandFactSchemaID
	}
	t.Fatalf("unknown fact family %q", family)
	return ""
}

func observationFact(entityID string, observedAt time.Time) devices.ObservationFact {
	return devices.ObservationFact{
		ObservationID:     devices.ObservationID(deviceFactTestObservationID),
		EntityID:          devices.EntityID(entityID),
		Disposition:       devices.DispositionApplied,
		Value:             devices.Value(`{"on":true}`),
		CorrelationID:     devices.CorrelationID(testCorrelationID),
		AdapterReceivedAt: observedAt.Add(-time.Second),
		ObservedAt:        observedAt,
	}
}

func entityEventFact(entityID string, receivedAt time.Time) devices.EntityEventFact {
	return devices.EntityEventFact{
		EventID:       devices.EntityEventID(deviceFactTestEventID),
		EntityID:      devices.EntityID(entityID),
		Name:          devices.EntityEventName("single_press"),
		CorrelationID: devices.CorrelationID(testCorrelationID),
		ReportedAt:    receivedAt.Add(-time.Second),
		ReceivedAt:    receivedAt,
		RecordedAt:    receivedAt,
	}
}

func commandFact(entityID string, requestedAt time.Time) devices.CommandFact {
	return devices.CommandFact{Record: devices.CommandRecord{
		ID:            devices.CommandID(deviceFactTestCommandID),
		EntityID:      devices.EntityID(entityID),
		AdapterID:     "simulator",
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
		CorrelationID: devices.CorrelationID(testCorrelationID),
		Status:        devices.CommandStatusSatisfied,
		RequestedAt:   requestedAt.Add(-time.Second),
		DeadlineAt:    requestedAt.Add(time.Minute),
		AcceptedAt:    new(requestedAt.Add(-time.Millisecond)),
		CompletedAt:   new(requestedAt),
		OutcomeObservationID: new(
			devices.ObservationID(deviceFactTestObservationID),
		),
	}}
}

// commandFactWireCase is one Command lifecycle status with exactly the terminal
// fields its strict schema permits.
type commandFactWireCase struct {
	status      devices.CommandStatus
	terminal    bool
	outcome     bool
	hasFailure  bool
	failureCode devices.CommandFailureCode
}

// commandFactWireCases covers every status a durable Command transition can
// publish, with the field combination the schema requires for it.
func commandFactWireCases() []commandFactWireCase {
	return []commandFactWireCase{
		{status: devices.CommandStatusRequested},
		{status: devices.CommandStatusAccepted},
		{status: devices.CommandStatusSatisfied, terminal: true, outcome: true},
		{status: devices.CommandStatusDispatched, terminal: true},
		{
			status: devices.CommandStatusRejected, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureUpstreamRejected,
		},
		{
			status: devices.CommandStatusAdapterUnhealthy, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureAdapterUnhealthy,
		},
		{
			status: devices.CommandStatusEntityUnavailable, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureEntityUnavailable,
		},
		{
			status: devices.CommandStatusOutcomeTimeout, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureOutcomeTimeout,
		},
		{
			status: devices.CommandStatusEntityDisabled, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureEntityDisabled,
		},
		{
			status: devices.CommandStatusInternalFailure, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureInternalError,
		},
		{
			status: devices.CommandStatusInterrupted, terminal: true, hasFailure: true,
			failureCode: devices.CommandFailureCoreRestarted,
		},
	}
}

// TestDeviceFactDispatcherPublishesEveryCommandStatusVariantWireValid covers the
// Command wire mapping for every lifecycle status a durable transition can
// publish, including the dispatch terminal and the failure statuses: each maps
// to its exact subject variant and a strict-schema-valid payload carrying only
// the fields its status permits.
func TestDeviceFactDispatcherPublishesEveryCommandStatusVariantWireValid(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	for _, test := range commandFactWireCases() {
		fixture.dispatcher.CommandTransitioned(context.Background(), devices.CommandFact{
			Record: commandFactWireRecord(now, test),
		})
		assertCommandFactWire(t, fixture, test)
	}
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

// commandFactWireRecord builds the durable record one Command status commits.
func commandFactWireRecord(now time.Time, test commandFactWireCase) devices.CommandRecord {
	record := devices.CommandRecord{
		ID:            devices.CommandID(deviceFactTestCommandID),
		EntityID:      devices.EntityID(deviceFactTestEntityID),
		AdapterID:     "simulator",
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
		CorrelationID: devices.CorrelationID(testCorrelationID),
		Status:        test.status,
		RequestedAt:   now.Add(-time.Second),
		DeadlineAt:    now.Add(time.Minute),
	}
	// A requested transition is published before acceptance, so it is the one
	// status whose fact carries no accepted_at.
	if test.status != devices.CommandStatusRequested {
		record.AcceptedAt = new(now.Add(-time.Millisecond))
	}
	if test.terminal {
		record.CompletedAt = new(now)
	}
	if test.hasFailure {
		record.FailureCode = new(test.failureCode)
	}
	if test.outcome {
		record.OutcomeObservationID = new(devices.ObservationID(deviceFactTestObservationID))
	}
	return record
}

// assertCommandFactWire validates one published Command status fact: exact
// subject variant, source causation, and exactly the terminal fields the status
// permits.
func assertCommandFactWire(t *testing.T, fixture *deviceFactFixture, test commandFactWireCase) {
	t.Helper()
	envelope, route := decodeFact(t, fixture.validator, fixture.nextFact(t))
	if route.Family != natswire.DeviceFactFamilyCommand || route.Variant != string(test.status) {
		t.Fatalf("status %q published route %#v", test.status, route)
	}
	if envelope.CausationID != deviceFactTestCommandID {
		t.Fatalf("status %q causation = %q", test.status, envelope.CausationID)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if status := commandFactWireString(t, data, "status"); status != string(test.status) {
		t.Fatalf("payload status %q disagrees with subject variant %q", status, route.Variant)
	}
	assertCommandFactOptionalField(t, envelope.Data, data, "completed_at", test.terminal)
	assertCommandFactOptionalField(t, envelope.Data, data, "outcome_observation_id", test.outcome)
	assertCommandFactOptionalField(t, envelope.Data, data, "failure_code", test.hasFailure)
	if !test.hasFailure {
		return
	}
	if code := commandFactWireString(t, data, "failure_code"); code != string(test.failureCode) {
		t.Fatalf("status %q failure_code = %q, want %q", test.status, code, test.failureCode)
	}
}

// assertCommandFactOptionalField pins one fact field's presence.
func assertCommandFactOptionalField(
	t *testing.T,
	payload []byte,
	data map[string]json.RawMessage,
	field string,
	want bool,
) {
	t.Helper()
	_, present := data[field]
	if present != want {
		t.Fatalf("fact %s present = %t, want %t: %s", field, present, want, payload)
	}
}

func commandFactWireString(t *testing.T, data map[string]json.RawMessage, field string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(data[field], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestDeviceFactDispatcherPublishesEachFamilyOnceWithStrictWire protects the
// mapping contract: exact subject, schema-valid payload, subject/payload
// agreement, fresh fct_ identity, source correlation and causation, injected
// trace headers, no Nats-Msg-Id and exactly one publication per fact.
//
//nolint:gocognit // The per-family wire matrix is clearer as one table-less sequence.
func TestDeviceFactDispatcherPublishesEachFamilyOnceWithStrictWire(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	ctx := testTraceContext(t)

	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	fixture.dispatcher.EntityEventAccepted(ctx, entityEventFact(deviceFactTestEntityID, now))
	fixture.dispatcher.CommandTransitioned(ctx, commandFact(deviceFactTestEntityID, now))

	seen := map[string]string{}
	for range 3 {
		message := fixture.nextFact(t)
		envelope, route := decodeFact(t, fixture.validator, message)
		if envelope.EmittedAt != now.Format(time.RFC3339Nano) {
			t.Fatalf("emitted_at = %q, want %q", envelope.EmittedAt, now.Format(time.RFC3339Nano))
		}
		if envelope.CorrelationID != testCorrelationID {
			t.Fatalf("correlation_id = %q, want %q", envelope.CorrelationID, testCorrelationID)
		}
		if route.EntityID != deviceFactTestEntityID {
			t.Fatalf("subject Entity = %q, want %q", route.EntityID, deviceFactTestEntityID)
		}
		if message.Header.Get(natsgo.MsgIdHdr) != "" {
			t.Fatal("Device Fact carried a Nats-Msg-Id header")
		}
		if message.Header.Get("traceparent") == "" {
			t.Fatal("Device Fact did not carry injected W3C trace headers")
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatal(err)
		}
		var payloadEntity string
		if err := json.Unmarshal(data["entity_id"], &payloadEntity); err != nil {
			t.Fatal(err)
		}
		if payloadEntity != route.EntityID {
			t.Fatalf("payload Entity %q disagrees with subject Entity %q", payloadEntity, route.EntityID)
		}
		switch route.Family {
		case natswire.DeviceFactFamilyObservation:
			if route.Variant != natswire.ObservationFactApplied || envelope.CausationID != deviceFactTestObservationID {
				t.Fatalf("Observation fact route = %#v, causation = %q", route, envelope.CausationID)
			}
			var disposition string
			if err := json.Unmarshal(data["disposition"], &disposition); err != nil {
				t.Fatal(err)
			}
			if disposition != route.Variant {
				t.Fatalf("payload disposition %q disagrees with subject variant %q", disposition, route.Variant)
			}
		case natswire.DeviceFactFamilyEntityEvent:
			if route.Variant != "single_press" || envelope.CausationID != deviceFactTestEventID {
				t.Fatalf("Entity Event fact route = %#v, causation = %q", route, envelope.CausationID)
			}
		case natswire.DeviceFactFamilyCommand:
			if route.Variant != string(devices.CommandStatusSatisfied) ||
				envelope.CausationID != deviceFactTestCommandID {
				t.Fatalf("Command fact route = %#v, causation = %q", route, envelope.CausationID)
			}
			var status string
			if err := json.Unmarshal(data["status"], &status); err != nil {
				t.Fatal(err)
			}
			if status != route.Variant {
				t.Fatalf("payload status %q disagrees with subject variant %q", status, route.Variant)
			}
		}
		if previous, duplicate := seen[envelope.ID]; duplicate {
			t.Fatalf("fact ID %q published twice (%s and %s)", envelope.ID, previous, route.Family)
		}
		seen[envelope.ID] = string(route.Family)
	}
	if len(seen) != 3 {
		t.Fatalf("published identities = %d, want 3 unique", len(seen))
	}
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

// TestDeviceFactDispatcherSinkNeverTouchesTheConnection protects the isolation
// claim: a worker stalled inside its publication cannot delay or block a sink
// method, because the sink never calls the connection at all.
func TestDeviceFactDispatcherSinkNeverTouchesTheConnection(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	entered := make(chan struct{})
	var attempts atomic.Int64
	release := make(chan struct{})
	fixture.dispatcher.publish = func(string, natsgo.Header, []byte) error {
		if attempts.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	}
	ctx := testTraceContext(t)
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never entered its publication")
	}

	// The worker is inside a publication now. Every sink method must still
	// return promptly and must not attempt another connection write.
	for range 3 {
		done := make(chan struct{})
		go func() {
			defer close(done)
			fixture.dispatcher.EntityEventAccepted(ctx, entityEventFact(deviceFactTestEntityID, now))
			fixture.dispatcher.CommandTransitioned(ctx, commandFact(deviceFactTestEntityID, now))
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a sink method blocked behind a stalled publication")
		}
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("sink methods caused %d publication attempts, want only the stalled one", got)
	}
	close(release)
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 7 {
		t.Fatalf("publication attempts = %d, want 7 (one stalled plus six queued)", got)
	}
}

// TestDeviceFactDispatcherQueueBoundsDropSafely protects both bounded-queue
// limits: message count and admitted bytes never grow past their constants, and
// overflow records one fixed diagnostic with the current counts.
//
//nolint:gocognit // The two bounded limits assert more clearly as one table.
func TestDeviceFactDispatcherQueueBoundsDropSafely(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// valueLen sizes each fact payload so the byte bound, not the message
		// count, is the binding limit in the bytes subtest.
		valueLen int
		// wantDropped is the exact expected drop count; zero means only that
		// the bound produced at least one drop.
		wantDropped int
	}{
		{name: "message count", valueLen: 4, wantDropped: 64},
		{name: "admitted bytes", valueLen: 40 * 1024, wantDropped: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newDeviceFactFixture(t)
			now := time.Now().UTC().Add(time.Second)
			fixture.dispatcher.now = func() time.Time { return now }
			entered := make(chan struct{})
			release := make(chan struct{})
			var stalled atomic.Bool
			stalled.Store(true)
			var releaseOnce sync.Once
			releaseAll := func() {
				stalled.Store(false)
				releaseOnce.Do(func() { close(release) })
			}
			// A stalled worker must never hang test teardown, so the stall is
			// always released before the fixture cleanup drains.
			defer releaseAll()
			realPublish := fixture.dispatcher.publish
			var enterOnce sync.Once
			fixture.dispatcher.publish = func(subject string, headers natsgo.Header, payload []byte) error {
				if stalled.Load() {
					enterOnce.Do(func() { close(entered) })
					<-release
				}
				return realPublish(subject, headers, payload)
			}
			fact := observationFact(deviceFactTestEntityID, now)
			fact.Value = devices.Value(`"` + strings.Repeat("a", test.valueLen) + `"`)
			ctx := context.Background()
			fixture.dispatcher.ObservationAccepted(ctx, fact)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the worker never entered its publication")
			}
			for range DeviceFactPendingMessages + 64 {
				fixture.dispatcher.ObservationAccepted(ctx, fact)
			}
			overflow := logEvents(fixture.logs.records(t), "device_fact.not_published")
			queueFull := 0
			maxPendingBytes := 0.0
			for _, record := range overflow {
				if record["error_code"] != deviceFactCodeQueueFull {
					t.Fatalf("unexpected drop diagnostic: %#v", record)
				}
				queueFull++
				pendingMessages, ok := record["pending_messages"].(float64)
				if !ok || pendingMessages > DeviceFactPendingMessages {
					t.Fatalf(
						"pending_messages = %#v, want at most %d",
						record["pending_messages"],
						DeviceFactPendingMessages,
					)
				}
				pendingBytes, ok := record["pending_bytes"].(float64)
				if !ok || pendingBytes > DeviceFactPendingBytes {
					t.Fatalf("pending_bytes = %#v, want at most %d", record["pending_bytes"], DeviceFactPendingBytes)
				}
				maxPendingBytes = max(maxPendingBytes, pendingBytes)
			}
			switch {
			case test.wantDropped > 0 && queueFull != test.wantDropped:
				t.Fatalf("queue overflow drops = %d, want %d", queueFull, test.wantDropped)
			case test.wantDropped == 0 && queueFull == 0:
				t.Fatal("the admitted-bytes bound produced no drop diagnostic")
			}
			if test.valueLen > DeviceFactMaxMessageBytes/2 && maxPendingBytes < float64(DeviceFactPendingBytes)/2 {
				t.Fatalf("admitted bytes peaked at %v, want the byte bound to bind first", maxPendingBytes)
			}
			sent := 1 + DeviceFactPendingMessages + 64
			releaseAll()
			if err := fixture.dispatcher.Drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			for range sent - queueFull {
				fixture.nextFact(t)
			}
			fixture.assertNoFact(t)
		})
	}
}

// TestDeviceFactDispatcherStalledPublicationDoesNotDelayConsumerAck protects A8
// end to end: a committed Observation is acknowledged to JetStream while the
// publication worker is stalled inside a publication, because the sink only
// enqueues bounded work on the caller and never touches the connection.
func TestDeviceFactDispatcherStalledPublicationDoesNotDelayConsumerAck(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	var attempts atomic.Int64
	realPublish := fixture.dispatcher.publish
	fixture.dispatcher.publish = func(subject string, headers natsgo.Header, payload []byte) error {
		if attempts.Add(1) == 1 {
			close(entered)
		}
		<-release
		return realPublish(subject, headers, payload)
	}

	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := platformdb.Migrate(t.Context(), database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(
		devices.SQLiteStores(repository), nil, catalog,
		devices.Dependencies{DeviceFacts: fixture.dispatcher},
	)
	entityID := registerStalledPublicationEntity(t, service)

	observationID := "obs_01890f47-7a6b-7c4d-8e9f-0123456789c1"
	subject, err := natswire.ObservationSubject("simulator", testRuntimeID, string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(fixture.validator, contractsv1.ObservationSchemaID,
		natswire.Envelope[observation]{
			ID: observationID, Schema: contractsv1.ObservationSchemaID,
			EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
			Data: observation{
				EntityID: string(entityID), Value: json.RawMessage(`true`),
				AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	message := &ackOrderingTestMessage{
		metadata: &jetstream.MsgMetadata{
			Sequence:  jetstream.SequencePair{Stream: 11},
			Timestamp: time.Now().UTC(),
		},
		data:    payload,
		headers: natsgo.Header{natsgo.MsgIdHdr: []string{observationID}},
		subject: subject,
		acked:   make(chan struct{}),
	}
	handled := make(chan struct{})
	go func() {
		defer close(handled)
		handleObservationMessage(
			t.Context(), message, fixture.validator, service, slog.New(slog.DiscardHandler),
		)
	}()

	select {
	case <-message.acked:
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled publication delayed the durable consumer acknowledgement")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never entered its stalled publication")
	}
	var disposition string
	if scanErr := database.QueryRowContext(
		t.Context(), "SELECT disposition FROM observations WHERE observation_id = ?", observationID,
	).Scan(&disposition); scanErr != nil {
		t.Fatal(scanErr)
	}
	if disposition != string(devices.DispositionApplied) {
		t.Fatalf("durable disposition = %q, want applied", disposition)
	}

	releaseAll()
	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("the observation handler did not return")
	}
	fact := fixture.nextFact(t)
	route, routeErr := natswire.ParseDeviceFactSubject(fact.Subject)
	if routeErr != nil {
		t.Fatal(routeErr)
	}
	if route.Family != natswire.DeviceFactFamilyObservation ||
		route.Variant != natswire.ObservationFactApplied || route.EntityID != string(entityID) {
		t.Fatalf("published fact route = %#v, want an applied Observation for %s", route, entityID)
	}
	decoded, _ := decodeFact(t, fixture.validator, fact)
	if decoded.CausationID != observationID {
		t.Fatalf("fact causation = %q, want %q", decoded.CausationID, observationID)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("publication attempts = %d, want exactly one", got)
	}
	if drainErr := fixture.dispatcher.Drain(t.Context()); drainErr != nil {
		t.Fatal(drainErr)
	}
}

// registerStalledPublicationEntity claims the test Runtime and registers one
// power Entity so a real Observation commit is accepted.
func registerStalledPublicationEntity(t *testing.T, service *devices.Service) devices.EntityID {
	t.Helper()
	ctx := t.Context()
	if claimErr := service.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: devices.RuntimeID(testRuntimeID),
		SoftwareName: "hearth-facts-test", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", devices.RuntimeID(testRuntimeID), devices.Registration{
		BindingKey: "stalled-light",
		Device:     devices.DeviceDescriptor{Name: "Stalled light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{{
			Key: "power", ExternalID: "stalled.power", Name: "Power",
			TypeID:  devices.EntityTypePowerV1,
			Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding.Entities[0].EntityID
}

// TestDeviceFactDispatcherSuppressesReportsBeforeTheLiveEpoch protects the
// boundary comparison: a report stored before the established live window is
// suppressed with the clock-skew diagnostic, and one stored at or after it is
// eligible.
func TestDeviceFactDispatcherSuppressesReportsBeforeTheLiveEpoch(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	boundary := time.Now().UTC().Add(time.Minute)
	fixture.epochs.IngestConnected(NATSConnectionGeneration{}, boundary)
	fixture.epochs.PublishConnected(NATSConnectionGeneration{}, boundary)

	stale := observationFact(deviceFactTestEntityID, boundary.Add(-time.Second))
	fixture.dispatcher.ObservationAccepted(context.Background(), stale)
	suppressed := logEvents(fixture.logs.records(t), "device_fact.suppressed")
	if len(suppressed) != 1 || suppressed[0]["reason"] != deviceFactReasonBeforeEpoch {
		t.Fatalf("stale report diagnostics = %#v, want one before_epoch suppression", suppressed)
	}
	if _, ok := suppressed[0]["received_at"]; !ok {
		t.Fatalf("before_epoch suppression omitted the clock-skew evidence: %#v", suppressed[0])
	}
	if _, ok := suppressed[0]["live_since"]; !ok {
		t.Fatalf("before_epoch suppression omitted the live boundary: %#v", suppressed[0])
	}

	atBoundary := observationFact(deviceFactTestEntityID, boundary)
	fixture.dispatcher.ObservationAccepted(context.Background(), atBoundary)
	published := fixture.nextFact(t)
	if published.Subject != mustObservationFactSubject(t, natswire.ObservationFactApplied) {
		t.Fatalf("boundary report was not published: %q", published.Subject)
	}
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

// TestDeviceFactDispatcherRequiresBothConnectionsLiveForCommands protects the
// Command side of the gate: Command transitions originate inside Core, so they
// carry no receive time and are published only while both connections are live.
func TestDeviceFactDispatcherRequiresBothConnectionsLiveForCommands(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	// Mark only the ingest side: the publication connection never established
	// an epoch, so no Command fact may be published.
	fixture.epochs.IngestConnected(NATSConnectionGeneration{}, time.Now().UTC())
	fixture.epochs.PublishDisconnected()
	fixture.dispatcher.CommandTransitioned(context.Background(), commandFact(deviceFactTestEntityID, time.Now().UTC()))
	suppressed := logEvents(fixture.logs.records(t), "device_fact.suppressed")
	if len(suppressed) != 1 || suppressed[0]["reason"] != deviceFactReasonNotLive {
		t.Fatalf("command diagnostics = %#v, want one not_live suppression", suppressed)
	}
	if suppressed[0]["family"] != string(natswire.DeviceFactFamilyCommand) {
		t.Fatalf("suppression family = %#v, want command", suppressed[0]["family"])
	}
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

func mustObservationFactSubject(t *testing.T, disposition string) string {
	t.Helper()
	subject, err := natswire.ObservationFactSubject(deviceFactTestEntityID, disposition)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// TestDeviceFactDispatcherDropsFactsAcrossReconnects protects the freshness
// gate end to end: a fact queued while the publication connection is down is
// suppressed rather than delivered after reconnect, and a later live fact is
// published exactly once.
func TestDeviceFactDispatcherDropsFactsAcrossReconnects(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	port := deviceFactServerPort(t, fixture.server)

	first := observationFact(deviceFactTestEntityID, time.Now().UTC())
	if _, err := devices.NewDeviceFactID(); err != nil {
		t.Fatal(err)
	}
	fixture.dispatcher.ObservationAccepted(context.Background(), first)
	firstEnvelope, _ := decodeFact(t, fixture.validator, fixture.nextFact(t))

	fixture.server.Shutdown()
	fixture.server.WaitForShutdown()
	waitForDeviceFactCondition(t, "the live window to close", func() bool {
		return !fixture.epochs.Snapshot().Live
	})
	fixture.dispatcher.ObservationAccepted(
		context.Background(),
		observationFact(deviceFactTestEntityID, time.Now().UTC()),
	)
	suppressed := logEvents(fixture.logs.records(t), "device_fact.suppressed")
	if len(suppressed) != 1 || suppressed[0]["reason"] != deviceFactReasonNotLive {
		t.Fatalf("stale fact diagnostics = %#v, want one not_live suppression", suppressed)
	}

	startDeviceFactServer(t, port)
	waitForDeviceFactCondition(t, "the live window to reopen", func() bool {
		return fixture.epochs.Snapshot().Live && fixture.publish.IsConnected()
	})
	fixture.dispatcher.ObservationAccepted(
		context.Background(),
		observationFact(deviceFactTestEntityID, time.Now().UTC()),
	)
	secondEnvelope, _ := decodeFact(t, fixture.validator, fixture.nextFact(t))
	if firstEnvelope.ID == secondEnvelope.ID {
		t.Fatal("reconnect reused a fact identity")
	}
	if err := fixture.dispatcher.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

// TestDeviceFactConnectionDoesNotBufferDuringReconnect protects A10 directly:
// the dedicated connection fails a publication during reconnect and never
// delivers it later, while a shared-style default connection still buffers and
// delivers.
func TestDeviceFactConnectionDoesNotBufferDuringReconnect(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	subscriber := connectDeviceFactClient(t, server.ClientURL())
	facts := make(chan *natsgo.Msg, 4)
	if _, err := subscriber.Subscribe("hearth.v1.core.fact.>", func(message *natsgo.Msg) {
		facts <- message
	}); err != nil {
		t.Fatal(err)
	}
	if err := subscriber.Flush(); err != nil {
		t.Fatal(err)
	}
	factConnection := connectDeviceFactClient(t, server.ClientURL(), DeviceFactConnectionOptions()...)
	// The comparison connection mirrors the shared Core connection's assembled
	// policy (see connectCoreNATS): nats.go's default reconnect buffering plus the
	// same bounded write deadline. It reconnects slowly, so its subscription peer
	// is re-registered on the restarted server before its buffered publication is
	// flushed. That ordering makes "the buffer was delivered" deterministic
	// instead of racing the subscriber's own reconnect.
	bufferedConnection := connectDeviceFactClient(
		t, server.ClientURL(),
		natsgo.ReconnectWait(2*time.Second),
		natsgo.FlusherTimeout(CoreNATSWriteTimeout),
	)
	if factConnection.Opts.ReconnectBufSize != -1 {
		t.Fatalf("fact connection ReconnectBufSize = %d, want -1", factConnection.Opts.ReconnectBufSize)
	}
	if factConnection.Opts.MaxReconnect != -1 {
		t.Fatalf("fact connection MaxReconnect = %d, want -1", factConnection.Opts.MaxReconnect)
	}
	if factConnection.Opts.Name != DeviceFactDispatcherName {
		t.Fatalf("fact connection name = %q, want %q", factConnection.Opts.Name, DeviceFactDispatcherName)
	}
	if bufferedConnection.Opts.ReconnectBufSize != natsgo.DefaultReconnectBufSize {
		t.Fatalf(
			"shared connection ReconnectBufSize = %d, want the default %d",
			bufferedConnection.Opts.ReconnectBufSize, natsgo.DefaultReconnectBufSize,
		)
	}

	port := deviceFactServerPort(t, server)
	server.Shutdown()
	server.WaitForShutdown()
	waitForDeviceFactCondition(t, "both connections to enter reconnect", func() bool {
		return factConnection.Status() == natsgo.RECONNECTING &&
			bufferedConnection.Status() == natsgo.RECONNECTING
	})
	subject, err := natswire.ObservationFactSubject(deviceFactTestEntityID, natswire.ObservationFactApplied)
	if err != nil {
		t.Fatal(err)
	}
	factErr := factConnection.PublishMsg(&natsgo.Msg{Subject: subject, Data: []byte("dropped")})
	if !errors.Is(factErr, natsgo.ErrReconnectBufExceeded) {
		t.Fatalf("publication during reconnect = %v, want ErrReconnectBufExceeded", factErr)
	}
	if bufferedErr := bufferedConnection.PublishMsg(
		&natsgo.Msg{Subject: subject, Data: []byte("buffered")},
	); bufferedErr != nil {
		t.Fatalf(
			"shared connection publication during reconnect = %v, want the existing buffering behavior",
			bufferedErr,
		)
	}

	startDeviceFactServer(t, port)
	waitForDeviceFactCondition(t, "the subscriber to re-register its subscription", func() bool {
		if !subscriber.IsConnected() {
			return false
		}
		subscriptions, subscriptionErr := server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true, Test: subject,
		})
		return subscriptionErr == nil && subscriptions.Total > 0
	})
	waitForDeviceFactCondition(t, "both connections to reconnect", func() bool {
		return factConnection.IsConnected() && bufferedConnection.IsConnected()
	})
	// The buffered publication is flushed during the reconnect itself, so the
	// assertion is that it arrives, never that a later flush produced it.
	select {
	case message := <-facts:
		if string(message.Data) != "buffered" {
			t.Fatalf("delivered payload = %q, want only the buffered publication", message.Data)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the shared connection did not deliver its buffered publication after reconnect")
	}
	select {
	case message := <-facts:
		t.Fatalf("a fact publication survived reconnect: %q", message.Data)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDeviceFactDispatcherCleanDrainPublishesEverythingQueued protects the
// bounded drain: admitted work is published before the worker exits.
func TestDeviceFactDispatcherCleanDrainPublishesEverythingQueued(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	ctx := context.Background()
	for range 8 {
		fixture.dispatcher.EntityEventAccepted(ctx, entityEventFact(deviceFactTestEntityID, now))
	}
	if err := fixture.dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if fixture.dispatcher.Active() {
		t.Fatal("dispatcher stayed active after Drain")
	}
	select {
	case <-fixture.dispatcher.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("the publication worker did not exit")
	}
	for range 8 {
		fixture.nextFact(t)
	}
	if err := fixture.dispatcher.Drain(ctx); err != nil {
		t.Fatalf("repeated Drain = %v, want nil", err)
	}
	fixture.assertNoFact(t)
}

// TestDeviceFactDispatcherAbortsStalledDrain protects the abort path: an
// expired drain closes the dedicated connection, discards the queue and joins
// the worker, so shutdown always completes.
func TestDeviceFactDispatcherAbortsStalledDrain(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	entered := make(chan struct{})
	released := make(chan struct{})
	var releaseOnce sync.Once
	fixture.publish.SetClosedHandler(func(*natsgo.Conn) { releaseOnce.Do(func() { close(released) }) })
	fixture.dispatcher.publish = func(string, natsgo.Header, []byte) error {
		close(entered)
		<-released
		return natsgo.ErrConnectionClosed
	}
	ctx := context.Background()
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never entered its stalled publication")
	}
	for range 4 {
		fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	}
	canceled, cancelDrain := context.WithCancel(ctx)
	cancelDrain()
	if err := fixture.dispatcher.Drain(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("expired Drain = %v, want context.Canceled", err)
	}
	select {
	case <-fixture.dispatcher.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("the publication worker did not exit after abort")
	}
	if fixture.dispatcher.Active() {
		t.Fatal("dispatcher stayed active after abort")
	}
	if got := len(fixture.facts); got != 0 {
		t.Fatalf("aborted drain published %d facts, want 0", got)
	}
}

// TestDeviceFactDispatcherRejectsInvalidInternalFacts protects every internal
// failure path: each failure is dropped with its stage and fixed error code and
// nothing is published.
func TestDeviceFactDispatcherRejectsInvalidInternalFacts(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	ctx := context.Background()

	fixture.dispatcher.newFactID = func() (devices.DeviceFactID, error) {
		return "", errors.New("identity source failed")
	}
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))

	fixture.dispatcher.newFactID = devices.NewDeviceFactID
	fixture.dispatcher.now = func() time.Time { return time.Time{} }
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))

	fixture.dispatcher.now = func() time.Time { return now }
	fixture.dispatcher.ObservationAccepted(ctx, observationFact("not-an-entity-id", now))

	oversized := observationFact(deviceFactTestEntityID, now)
	oversized.Value = devices.Value(`"` + strings.Repeat("a", DeviceFactMaxMessageBytes) + `"`)
	fixture.dispatcher.ObservationAccepted(ctx, oversized)

	uncorrelated := observationFact(deviceFactTestEntityID, now)
	uncorrelated.CorrelationID = ""
	fixture.dispatcher.ObservationAccepted(ctx, uncorrelated)

	want := map[string]string{
		deviceFactCodeIDFailed:     deviceFactStageIdentify,
		deviceFactCodeClockZero:    deviceFactStageClock,
		deviceFactCodeInvalid:      deviceFactStageMap,
		deviceFactCodeTooLarge:     deviceFactStageSize,
		deviceFactCodeEncodeFailed: deviceFactStageEncode,
	}
	records := logEvents(fixture.logs.records(t), "device_fact.not_published")
	seen := map[string]string{}
	for _, record := range records {
		code, _ := record["error_code"].(string)
		stage, _ := record["stage"].(string)
		if want[code] != stage {
			t.Fatalf("drop diagnostic = %#v, want a known code and stage", record)
		}
		seen[code] = stage
	}
	for code, stage := range want {
		if seen[code] != stage {
			t.Fatalf("missing drop diagnostic for %s at stage %s: %#v", code, stage, records)
		}
	}
	if err := fixture.dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoFact(t)
}

// TestStartDeviceFactDispatcherValidatesDependencies fails construction before
// any goroutine starts.
func TestStartDeviceFactDispatcherValidatesDependencies(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	connection := connectDeviceFactClient(t, server.ClientURL())
	epochs, err := NewDeviceFactEpochs(connection, connection, nil)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	if _, missingErr := StartDeviceFactDispatcher(nil, validator, epochs, logger); missingErr == nil {
		t.Fatal("StartDeviceFactDispatcher accepted a nil connection")
	}
	if _, missingErr := StartDeviceFactDispatcher(connection, nil, epochs, logger); missingErr == nil {
		t.Fatal("StartDeviceFactDispatcher accepted a nil validator")
	}
	if _, missingErr := StartDeviceFactDispatcher(connection, validator, nil, logger); missingErr == nil {
		t.Fatal("StartDeviceFactDispatcher accepted nil epochs")
	}
}

// TestDeviceFactDispatcherInactiveAfterStopAdmission protects the readiness
// seam: admission stops immediately and queued work still drains.
func TestDeviceFactDispatcherInactiveAfterStopAdmission(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	if !fixture.dispatcher.Active() {
		t.Fatal("dispatcher was not active after start")
	}
	fixture.dispatcher.StopAdmission()
	if fixture.dispatcher.Active() {
		t.Fatal("dispatcher stayed active after StopAdmission")
	}
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	fixture.dispatcher.ObservationAccepted(context.Background(), observationFact(deviceFactTestEntityID, now))
	records := logEvents(fixture.logs.records(t), "device_fact.not_published")
	if len(records) != 1 || records[0]["error_code"] != deviceFactCodeAdmissionShut {
		t.Fatalf("closed-admission diagnostic = %#v", records)
	}
}
