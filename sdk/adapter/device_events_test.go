package adapter //nolint:testpackage // Tests exercise the private publication and wire representation.

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const testDeviceEventName = "single_press"

type alwaysFailingJetStreamPublisher struct {
	mutex    sync.Mutex
	err      error
	attempts int
}

func (publisher *alwaysFailingJetStreamPublisher) PublishMsg(
	context.Context,
	*natsgo.Msg,
	...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	publisher.mutex.Lock()
	publisher.attempts++
	publisher.mutex.Unlock()
	return nil, publisher.err
}

func (publisher *alwaysFailingJetStreamPublisher) publishedAttempts() int {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return publisher.attempts
}

func createDeviceEventStream(t *testing.T, connection *natsgo.Conn) jetstream.Stream {
	t.Helper()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateStream(testContext(t), jetstream.StreamConfig{
		Name:     "HEARTH_DEVICE_EVENTS_V1",
		Subjects: []string{natswire.DeviceEventWildcard()},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

// This test protects the durable publication path and fails if the stored
// report loses its envelope identity, its MsgId, its trace context, or its
// subject, and if the Session waits for anything beyond JetStream storage.
func TestPublishDeviceEventWaitsForStorageAcknowledgement(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	stream := createDeviceEventStream(t, core)
	session := connectSession(t, server.ClientURL())

	publishContext := trace.ContextWithSpanContext(testContext(t), sampleSpanContext())
	deviceEventID, err := session.PublishDeviceEvent(publishContext, DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(deviceEventID), "evt_") {
		t.Fatalf("Device Event ID = %q, want an evt_ ID", deviceEventID)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Header.Get(natsgo.MsgIdHdr); got != string(deviceEventID) {
		t.Fatalf("Nats-Msg-Id = %q, want %q", got, deviceEventID)
	}
	if stored.Header.Get("traceparent") == "" {
		t.Fatal("Device Event omitted W3C trace context")
	}
	subject, err := natswire.DeviceEventSubject("simulator", session.runtimeID, testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Subject != subject {
		t.Fatalf("stored subject = %q, want %q", stored.Subject, subject)
	}
	envelope, err := natswire.Decode[wireDeviceEvent](
		compileValidator(t), contractsv1.DeviceEventSchemaID, stored.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ID != string(deviceEventID) || envelope.Schema != contractsv1.DeviceEventSchemaID ||
		!strings.HasPrefix(envelope.CorrelationID, "cor_") || envelope.CausationID != nil ||
		envelope.Data.EntityID != testEntityID || envelope.Data.Name != testDeviceEventName {
		t.Fatalf("stored Device Event = %#v", envelope)
	}
	if _, parseErr := time.Parse(time.RFC3339Nano, envelope.EmittedAt); parseErr != nil {
		t.Fatalf("emitted_at = %q: %v", envelope.EmittedAt, parseErr)
	}
	// PubAck means JetStream stored the report. No Core consumer exists here,
	// so it cannot mean Core recorded or accepted anything.
	info, err := stream.Info(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Consumers != 0 || info.State.Msgs != 1 {
		t.Fatalf("stream state after PubAck = %#v", info.State)
	}
}

// This test protects identity stability across a lost PubAck and fails if a
// transient retry mints a new ID, re-encodes the payload, changes the subject,
// or drops the trace metadata.
func TestPublishDeviceEventRetriesOneEncodedReport(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &sequenceJetStreamPublisher{errors: []error{natsgo.ErrDisconnected}}
	session.jetstream = publisher

	publishContext := trace.ContextWithSpanContext(testContext(t), sampleSpanContext())
	deviceEventID, err := session.PublishDeviceEvent(publishContext, DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if err != nil {
		t.Fatal(err)
	}
	published := publisher.published()
	if len(published) != 2 {
		t.Fatalf("publication attempts = %d, want 2", len(published))
	}
	if !bytes.Equal(published[0].data, published[1].data) {
		t.Fatalf("retry re-encoded the report:\n% X\n% X", published[0].data, published[1].data)
	}
	for index, publication := range published {
		if publication.header.Get(natsgo.MsgIdHdr) != string(deviceEventID) ||
			publication.header.Get("traceparent") == "" {
			t.Fatalf("publication %d headers = %#v", index, publication.header)
		}
	}
	envelope, err := natswire.Decode[wireDeviceEvent](
		compileValidator(t), contractsv1.DeviceEventSchemaID, published[0].data,
	)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ID != string(deviceEventID) || !strings.HasPrefix(envelope.CorrelationID, "cor_") {
		t.Fatalf("retried envelope = %#v", envelope)
	}
}

// This test protects the reconnect case with a real JetStream server and fails
// if a report republished after a connection loss is stored under a new
// identity or more than once.
func TestPublishDeviceEventRetriesSameIDAfterReconnect(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	server := startServer(t, -1, storeDir)
	core := connectNATS(t, server.ClientURL())
	createDeviceEventStream(t, core)
	session := connectSession(t, server.ClientURL())
	port := server.Addr().(*net.TCPAddr).Port

	server.Shutdown()
	server.WaitForShutdown()
	waitForConnectionStatus(t, session.connection, natsgo.RECONNECTING)

	type publishResult struct {
		id  DeviceEventID
		err error
	}
	result := make(chan publishResult, 1)
	publishContext, cancelPublish := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPublish()
	go func() {
		id, err := session.PublishDeviceEvent(publishContext, DeviceEvent{
			EntityID: testEntityID, Name: testDeviceEventName,
		})
		result <- publishResult{id: id, err: err}
	}()

	restarted := startServer(t, port, storeDir)
	// Cleanup is LIFO: release the Session before shutting down the restarted server.
	t.Cleanup(func() { _ = session.Close() })
	published := <-result
	if published.err != nil {
		t.Fatal(published.err)
	}
	reconnectedCore := connectNATS(t, restarted.ClientURL())
	js, err := jetstream.New(reconnectedCore)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(testContext(t), "HEARTH_DEVICE_EVENTS_V1")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Header.Get(natsgo.MsgIdHdr) != string(published.id) {
		t.Fatalf("Nats-Msg-Id = %q, want %q", stored.Header.Get(natsgo.MsgIdHdr), published.id)
	}
	envelope, err := natswire.Decode[wireDeviceEvent](
		compileValidator(t), contractsv1.DeviceEventSchemaID, stored.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ID != string(published.id) {
		t.Fatalf("retried envelope ID = %q, want %q", envelope.ID, published.id)
	}
	info, err := stream.Info(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stored messages = %d, want 1", info.State.Msgs)
	}
}

// This test protects the ambiguous-failure contract and fails if the minted ID
// is hidden behind a permanent publication failure.
func TestPublishDeviceEventReturnsMintedIdentityOnPermanentFailure(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	permanent := errors.New("stream not found")
	publisher := &sequenceJetStreamPublisher{errors: []error{permanent}}
	session.jetstream = publisher

	id, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if !errors.Is(err, permanent) {
		t.Fatalf("error = %v, want the permanent failure", err)
	}
	published := publisher.published()
	if len(published) != 1 || id == "" || published[0].header.Get(natsgo.MsgIdHdr) != string(id) {
		t.Fatalf("minted ID = %q, publications = %#v", id, published)
	}
}

// This test protects caller cancellation and fails if retries continue, hide
// the minted ID, or publish after the caller gave up.
func TestPublishDeviceEventCallerCancellationStopsRetries(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &cancellationAwareJetStreamPublisher{started: make(chan struct{})}
	session.jetstream = publisher
	ctx, cancel := context.WithCancel(context.Background())

	type publishResult struct {
		id  DeviceEventID
		err error
	}
	result := make(chan publishResult, 1)
	go func() {
		id, err := session.PublishDeviceEvent(ctx, DeviceEvent{
			EntityID: testEntityID, Name: testDeviceEventName,
		})
		result <- publishResult{id: id, err: err}
	}()
	<-publisher.started
	cancel()
	published := <-result
	if !errors.Is(published.err, context.Canceled) {
		t.Fatalf("cancellation error = %v", published.err)
	}
	if !strings.HasPrefix(string(published.id), "evt_") {
		t.Fatalf("cancellation ID = %q, want the minted evt_ ID", published.id)
	}
}

// This test protects the caller deadline and fails if a transient failure
// retries past the deadline or loses the minted identity.
func TestPublishDeviceEventCallerDeadlineStopsRetries(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &alwaysFailingJetStreamPublisher{err: natsgo.ErrDisconnected}
	session.jetstream = publisher
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	id, err := session.PublishDeviceEvent(ctx, DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	if !strings.HasPrefix(string(id), "evt_") {
		t.Fatalf("deadline ID = %q, want the minted evt_ ID", id)
	}
	if attempts := publisher.publishedAttempts(); attempts < 2 {
		t.Fatalf("transient attempts = %d, want retries before the deadline", attempts)
	}
}

// This test protects Session termination and fails if a terminated Session
// publishes or mints an identity it can no longer use.
func TestPublishDeviceEventStopsAfterSessionTermination(t *testing.T) {
	t.Parallel()
	for name, terminate := range map[string]func(context.Context, *Session){
		"closed": func(_ context.Context, session *Session) { session.markClosed() },
		"fenced": func(ctx context.Context, session *Session) { session.markFenced(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := startServer(t, -1, t.TempDir())
			session := connectSession(t, server.ClientURL())
			publisher := &sequenceJetStreamPublisher{}
			session.jetstream = publisher
			terminate(context.Background(), session)
			id, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
				EntityID: testEntityID, Name: testDeviceEventName,
			})
			if name == "closed" && !errors.Is(err, ErrClosed) ||
				name == "fenced" && !errors.Is(err, ErrRuntimeFenced) {
				t.Fatalf("terminated publication error = %v", err)
			}
			if id != "" || len(publisher.published()) != 0 {
				t.Fatalf(
					"terminated publication minted %q and published %d messages",
					id, len(publisher.published()),
				)
			}
		})
	}
}

// This test protects envelope validation and fails if the Session publishes a
// report whose envelope is invalid, or claims support knowledge it cannot have.
func TestPublishDeviceEventValidatesEnvelopeBeforePublishing(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())

	for name, event := range map[string]DeviceEvent{
		"entity ID": {EntityID: "not-an-entity-id", Name: testDeviceEventName},
		"name":      {EntityID: testEntityID, Name: "Single Press"},
		"empty":     {EntityID: testEntityID},
	} {
		publisher := &sequenceJetStreamPublisher{}
		session.jetstream = publisher
		id, err := session.PublishDeviceEvent(context.Background(), event)
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("%s: error = %v, want a validation error", name, err)
		}
		if !strings.HasPrefix(string(id), "evt_") {
			t.Fatalf("%s: validation ID = %q, want the minted evt_ ID", name, id)
		}
		if got := len(publisher.published()); got != 0 {
			t.Fatalf("%s: invalid report published %d messages", name, got)
		}
	}

	// Only Core owns current support validation: any canonical name slug is
	// publishable, including one the Entity does not currently support.
	publisher := &sequenceJetStreamPublisher{}
	session.jetstream = publisher
	if _, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
		EntityID: testEntityID, Name: "unlisted_gesture",
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(publisher.published()); got != 1 {
		t.Fatalf("publication attempts = %d, want 1", got)
	}

	// Each report mints its own identity.
	first, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("distinct reports reused Device Event ID %q", first)
	}
}

// This test protects the publication diagnostic and fails if it loses the
// bounded identity or logs the encoded envelope.
func TestPublishDeviceEventLogsIdentityWithoutEnvelope(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	createDeviceEventStream(t, core)
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	deviceEventID, err := session.PublishDeviceEvent(context.Background(), DeviceEvent{
		EntityID: testEntityID, Name: testDeviceEventName,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := waitForLogRecord(t, writer, "device_event.published", func(record map[string]any) bool {
		return record["device_event_id"] == string(deviceEventID)
	}, 3*time.Second)
	if record["entity_id"] != testEntityID || record["name"] != testDeviceEventName {
		t.Fatalf("publication record = %#v", record)
	}
	if correlationID, ok := record["correlation_id"].(string); !ok || !strings.HasPrefix(correlationID, "cor_") {
		t.Fatalf("publication record correlation ID = %#v", record["correlation_id"])
	}
	for _, key := range []string{"data", "payload", "value"} {
		if _, present := record[key]; present {
			t.Fatalf("publication record logs %q (record = %#v)", key, record)
		}
	}
}
