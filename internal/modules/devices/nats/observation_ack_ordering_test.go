package nats //nolint:testpackage // Tests exercise package-private NATS acknowledgement ordering.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ackOrderingTestMessage is a fake JetStream message that records Ack attempts
// without a server, so tests prove acknowledgement precedes log emission.
type ackOrderingTestMessage struct {
	metadata *jetstream.MsgMetadata
	data     []byte
	headers  natsgo.Header
	subject  string
	acked    chan struct{}
	ackErr   error
	once     sync.Once
	mutex    sync.Mutex
	ackCalls int
}

func (message *ackOrderingTestMessage) Metadata() (*jetstream.MsgMetadata, error) {
	return message.metadata, nil
}

func (message *ackOrderingTestMessage) Data() []byte { return message.data }

func (message *ackOrderingTestMessage) Headers() natsgo.Header { return message.headers }

func (message *ackOrderingTestMessage) Subject() string { return message.subject }

func (message *ackOrderingTestMessage) Reply() string { return "" }

func (message *ackOrderingTestMessage) Ack() error {
	message.mutex.Lock()
	message.ackCalls++
	message.mutex.Unlock()
	message.once.Do(func() { close(message.acked) })
	return message.ackErr
}

func (message *ackOrderingTestMessage) ackCount() int {
	message.mutex.Lock()
	defer message.mutex.Unlock()
	return message.ackCalls
}

func (*ackOrderingTestMessage) DoubleAck(context.Context) error { return nil }

func (*ackOrderingTestMessage) Nak() error { return nil }

func (*ackOrderingTestMessage) NakWithDelay(time.Duration) error { return nil }

func (*ackOrderingTestMessage) InProgress() error { return nil }

func (*ackOrderingTestMessage) Term() error { return nil }

func (*ackOrderingTestMessage) TermWithReason(string) error { return nil }

// blockingObservationLogHandler blocks the synchronous log destination on the
// first record until released. Any Ack gated behind log emission deadlocks
// while it blocks.
type blockingObservationLogHandler struct {
	inner   slog.Handler
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (handler *blockingObservationLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *blockingObservationLogHandler) Handle(ctx context.Context, record slog.Record) error {
	handler.once.Do(func() { close(handler.entered) })
	<-handler.release
	return handler.inner.Handle(ctx, record)
}

func (handler *blockingObservationLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &blockingObservationLogHandler{
		inner: handler.inner.WithAttrs(attrs), release: handler.release, entered: handler.entered,
	}
}

func (handler *blockingObservationLogHandler) WithGroup(name string) slog.Handler {
	return &blockingObservationLogHandler{
		inner: handler.inner.WithGroup(name), release: handler.release, entered: handler.entered,
	}
}

func newBlockingObservationSink() (*lockedTestLogWriter, *slog.Logger, chan struct{}, chan struct{}) {
	writer := &lockedTestLogWriter{}
	entered := make(chan struct{})
	release := make(chan struct{})
	logger := slog.New(&blockingObservationLogHandler{
		inner:   slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}),
		release: release,
		entered: entered,
	})
	return writer, logger, entered, release
}

func ackOrderingObservationPayload(
	t *testing.T,
	validator *contractsv1.Validator,
	observationID string,
	receivedAt time.Time,
) []byte {
	t.Helper()
	emittedAt := time.Now().UTC()
	envelope := natswire.Envelope[observation]{
		ID: observationID, Schema: contractsv1.ObservationSchemaID,
		EmittedAt: emittedAt.Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: observation{
			EntityID: testEntityID, Value: []byte(`true`),
			AdapterReceivedAt: receivedAt.Format(time.RFC3339Nano),
		},
	}
	payload, err := natswire.Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func newAckOrderingTestMessage(
	t *testing.T,
	payload []byte,
	observationID string,
	ackErr error,
) *ackOrderingTestMessage {
	t.Helper()
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{observationID}}
	natswire.InjectTrace(testTraceContext(t), headers)
	return &ackOrderingTestMessage{
		metadata: &jetstream.MsgMetadata{
			Sequence:  jetstream.SequencePair{Stream: 7},
			Timestamp: time.Now().UTC(),
		},
		data:    payload,
		headers: headers,
		subject: mustObservationSubject(t),
		acked:   make(chan struct{}),
		ackErr:  ackErr,
	}
}

// This test protects acknowledgement before success logging and fails if a
// blocked log destination delays Ack for a projected observation, including
// the clock-skew diagnostic. The projection diagnostic must still follow.
func TestObservationAcknowledgesBeforeProjectedLogsUnderBlockedWriter(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Now().UTC().Add(2 * time.Minute)
	message := newAckOrderingTestMessage(
		t, ackOrderingObservationPayload(t, validator, testObservationID, receivedAt), testObservationID, nil,
	)
	logs, logger, entered, release := newBlockingObservationSink()
	projector := projectorFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Observation,
		time.Time,
	) (devices.ProjectionResult, error) {
		return devices.ProjectionResult{Disposition: devices.DispositionApplied}, nil
	})

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleObservationMessage(context.Background(), message, validator, projector, logger)
	}()
	select {
	case <-message.acked:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("Ack was not attempted while success logs were blocked")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("blocked success log emission was never attempted")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish after log writer was released")
	}
	if got := message.ackCount(); got != 1 {
		t.Fatalf("Ack calls = %d, want 1", got)
	}
	projected := logEvents(logs.records(t), "observation.projected")
	if len(projected) != 1 {
		t.Fatalf("observation.projected events = %d, want 1:\n%s", len(projected), logs.output())
	}
	if projected[0]["observation_id"] != testObservationID {
		t.Fatalf("projected record = %#v", projected[0])
	}
	if failures := logEvents(logs.records(t), "observation.processing_failed"); len(failures) != 0 {
		t.Fatalf("observation.processing_failed events = %d, want 0:\n%s", len(failures), logs.output())
	}
}

// This test protects acknowledgement before invalid-input logging and fails
// if a blocked log destination delays Ack for wire-invalid input. The Warn
// diagnostic must still follow.
func TestObservationAcknowledgesBeforeInvalidLogsUnderBlockedWriter(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	message := newAckOrderingTestMessage(t, []byte(`{}`), testSecondObservationID, nil)
	logs, logger, entered, release := newBlockingObservationSink()
	projector := projectorFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Observation,
		time.Time,
	) (devices.ProjectionResult, error) {
		t.Error("malformed message reached projector")
		return devices.ProjectionResult{}, nil
	})

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		handleObservationMessage(context.Background(), message, validator, projector, logger)
	}()
	select {
	case <-message.acked:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("Ack was not attempted while invalid-input logs were blocked")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("blocked invalid log emission was never attempted")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish after log writer was released")
	}
	if got := message.ackCount(); got != 1 {
		t.Fatalf("Ack calls = %d, want 1", got)
	}
	invalid := logEvents(logs.records(t), "observation.invalid")
	if len(invalid) != 1 {
		t.Fatalf("observation.invalid events = %d, want 1:\n%s", len(invalid), logs.output())
	}
	if invalid[0]["level"] != "WARN" || invalid[0]["error_code"] != "observation_decode_failed" {
		t.Fatalf("invalid record = %#v", invalid[0])
	}
}

// This test protects projection diagnostics across Ack failures and fails if
// a failed Ack drops the committed projection record or the ack failure
// record.
func TestObservationRetainsProjectedDiagnosticWhenAckFails(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	message := newAckOrderingTestMessage(
		t,
		ackOrderingObservationPayload(t, validator, testObservationID, time.Now().UTC()),
		testObservationID,
		errors.New("JetStream unavailable"),
	)
	logs, logger := newTestLogSink()
	projector := projectorFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Observation,
		time.Time,
	) (devices.ProjectionResult, error) {
		return devices.ProjectionResult{Disposition: devices.DispositionApplied}, nil
	})

	handleObservationMessage(context.Background(), message, validator, projector, logger)

	if got := message.ackCount(); got != 1 {
		t.Fatalf("Ack calls = %d, want 1", got)
	}
	projected := logEvents(logs.records(t), "observation.projected")
	if len(projected) != 1 {
		t.Fatalf("observation.projected events = %d, want 1:\n%s", len(projected), logs.output())
	}
	failures := logEvents(logs.records(t), "observation.processing_failed")
	if len(failures) != 1 {
		t.Fatalf("observation.processing_failed events = %d, want 1:\n%s", len(failures), logs.output())
	}
	if failures[0]["stage"] != "ack" || failures[0]["error_code"] != "ack_failed" ||
		failures[0]["observation_id"] != testObservationID {
		t.Fatalf("ack failure record = %#v", failures[0])
	}
}
