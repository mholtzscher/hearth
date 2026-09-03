package adapter //nolint:testpackage // Tests exercise the private evidence and wire representation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

type publishedMessage struct {
	header natsgo.Header
	data   []byte
}

type sequenceJetStreamPublisher struct {
	mutex    sync.Mutex
	messages []publishedMessage
	errors   []error
}

func (publisher *sequenceJetStreamPublisher) PublishMsg(
	_ context.Context,
	message *natsgo.Msg,
	_ ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	header := make(natsgo.Header, len(message.Header))
	for key, values := range message.Header {
		header[key] = append([]string(nil), values...)
	}
	publisher.messages = append(publisher.messages, publishedMessage{
		header: header,
		data:   append([]byte(nil), message.Data...),
	})
	index := len(publisher.messages) - 1
	if index < len(publisher.errors) && publisher.errors[index] != nil {
		return nil, publisher.errors[index]
	}
	return &jetstream.PubAck{}, nil
}

func (publisher *sequenceJetStreamPublisher) published() []publishedMessage {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return append([]publishedMessage(nil), publisher.messages...)
}

type cancellationAwareJetStreamPublisher struct {
	started chan struct{}
	once    sync.Once
}

func (publisher *cancellationAwareJetStreamPublisher) PublishMsg(
	ctx context.Context,
	_ *natsgo.Msg,
	_ ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	publisher.once.Do(func() { close(publisher.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func acceptedEvidence(t *testing.T, session *Session, deadline time.Time) CommandEvidence {
	t.Helper()
	responder := &commandResponder{
		context:       context.Background(),
		session:       session,
		connection:    session.connection,
		replySubject:  "_INBOX.command-evidence",
		validator:     compileValidator(t),
		commandID:     mustID(t, "cmd"),
		correlationID: mustID(t, "cor"),
		entityID:      testEntityID,
		deadline:      deadline,
	}
	evidence, err := responder.Accept()
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestResponderEvidenceIsOneShotAndRejectionGrantsNone(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	responder := &commandResponder{
		context:       context.Background(),
		session:       session,
		connection:    session.connection,
		replySubject:  "_INBOX.command-evidence",
		validator:     compileValidator(t),
		commandID:     mustID(t, "cmd"),
		correlationID: mustID(t, "cor"),
		entityID:      testEntityID,
		deadline:      time.Now().Add(time.Second),
	}
	evidence, err := responder.Accept()
	if err != nil || evidence == nil {
		t.Fatalf("first Accept = %v, %v; want evidence", evidence, err)
	}
	evidence, err = responder.Accept()
	if evidence != nil || !errors.Is(err, ErrAlreadyResponded) {
		t.Fatalf("second Accept = %v, %v; want nil evidence and already responded", evidence, err)
	}

	rejected := &commandResponder{
		context:       context.Background(),
		session:       session,
		connection:    session.connection,
		replySubject:  "_INBOX.command-rejection",
		validator:     compileValidator(t),
		commandID:     mustID(t, "cmd"),
		correlationID: mustID(t, "cor"),
		entityID:      testEntityID,
		deadline:      time.Now().Add(time.Second),
	}
	if rejectErr := rejected.Reject("upstream rejected"); rejectErr != nil {
		t.Fatal(rejectErr)
	}
	evidence, err = rejected.Accept()
	if evidence != nil || !errors.Is(err, ErrAlreadyResponded) {
		t.Fatalf("Accept after rejection = %v, %v; want nil evidence and already responded", evidence, err)
	}
}

func TestCommandEvidenceValidatesEntityAndSupportsSeveralObservations(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &sequenceJetStreamPublisher{}
	session.jetstream = publisher
	evidence := acceptedEvidence(t, session, time.Now().Add(time.Second))

	first, err := evidence.PublishObservation(context.Background(), Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := evidence.PublishObservation(context.Background(), Observation{
		EntityID: testEntityID, Value: json.RawMessage(`false`), AdapterReceivedAt: nowString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("evidence reused Observation ID %q", first)
	}
	if _, validationErr := evidence.PublishObservation(context.Background(), Observation{
		EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		Value:    json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	}); validationErr == nil {
		t.Fatal("evidence published an Observation for another Entity")
	} else if _, ok := errors.AsType[*ValidationError](validationErr); !ok {
		t.Fatalf("other Entity error = %v, want validation error", validationErr)
	}
	if got := len(publisher.published()); got != 2 {
		t.Fatalf("publication attempts = %d, want 2", got)
	}
}

func TestCommandEvidenceRetriesOneEncodedEnvelope(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &sequenceJetStreamPublisher{errors: []error{natsgo.ErrDisconnected}}
	session.jetstream = publisher
	evidence := acceptedEvidence(t, session, time.Now().Add(time.Second))

	observationID, err := evidence.PublishObservation(context.Background(), Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	published := publisher.published()
	if len(published) != 2 || !bytes.Equal(published[0].data, published[1].data) ||
		published[0].header.Get(natsgo.MsgIdHdr) != string(observationID) ||
		published[1].header.Get(natsgo.MsgIdHdr) != string(observationID) {
		t.Fatalf("retried publications = %#v; Observation ID = %q", published, observationID)
	}
}

func TestCommandEvidenceCallerCancellationAndDeadlinePreventPublication(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	publisher := &cancellationAwareJetStreamPublisher{started: make(chan struct{})}
	session.jetstream = publisher
	evidence := acceptedEvidence(t, session, time.Now().Add(time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	published := make(chan error, 1)
	go func() {
		_, err := evidence.PublishObservation(ctx, Observation{
			EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
		})
		published <- err
	}()
	<-publisher.started
	cancel()
	if err := <-published; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller-canceled publication error = %v", err)
	}

	notStarted := &sequenceJetStreamPublisher{}
	session.jetstream = notStarted
	expired := acceptedEvidence(t, session, time.Now().Add(-time.Second))
	if _, err := expired.PublishObservation(context.Background(), Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired evidence error = %v, want deadline exceeded", err)
	}
	if got := len(notStarted.published()); got != 0 {
		t.Fatalf("expired evidence published %d messages", got)
	}
}

func TestCommandEvidenceStopsAfterSessionTermination(t *testing.T) {
	t.Parallel()
	for name, terminate := range map[string]func(*Session){
		"closed": (*Session).markClosed,
		"fenced": (*Session).markFenced,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := startServer(t, -1, t.TempDir())
			session := connectSession(t, server.ClientURL())
			publisher := &sequenceJetStreamPublisher{}
			session.jetstream = publisher
			evidence := acceptedEvidence(t, session, time.Now().Add(time.Second))
			terminate(session)
			_, err := evidence.PublishObservation(context.Background(), Observation{
				EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
			})
			if name == "closed" && !errors.Is(err, ErrClosed) || name == "fenced" && !errors.Is(err, ErrRuntimeFenced) {
				t.Fatalf("terminated evidence error = %v", err)
			}
			if got := len(publisher.published()); got != 0 {
				t.Fatalf("terminated evidence published %d messages", got)
			}
		})
	}
}

func TestOrdinaryObservationInCommandHandlerHasNoLink(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	stream := createObservationStream(t, core)
	session := connectSession(t, server.ClientURL())
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(t.Context(), func(ctx context.Context, _ Command, responder Responder) error {
			if _, err := session.PublishObservation(ctx, Observation{
				EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
			}); err != nil {
				return err
			}
			_, err := responder.Accept()
			return err
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, session.runtimeID, true)
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[CommandResponse](
		compileValidator(t),
		contractsv1.CommandResponseSchemaID,
		reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := natswire.Decode[wireObservation](
		compileValidator(t),
		contractsv1.ObservationSchemaID,
		stored.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.CausationID != nil || observation.Data.RefreshForCommand != nil ||
		observation.CorrelationID == response.CorrelationID {
		t.Fatalf("ordinary Observation = %#v; response = %#v", observation, response)
	}
}
