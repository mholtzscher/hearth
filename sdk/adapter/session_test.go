package adapter //nolint:testpackage // Tests exercise package-private Session lifecycle behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const testEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func TestConnectRetriesWhenNATSStartsLater(t *testing.T) {
	t.Parallel()
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := Connect(ctx, Config{AdapterID: "simulator", NATSURL: "nats://" + address})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if session.connection.Status() != natsgo.RECONNECTING {
		t.Fatalf("initial NATS status = %s, want %s", session.connection.Status(), natsgo.RECONNECTING)
	}

	startServer(t, port, t.TempDir())
	waitForConnectionStatus(t, session.connection, natsgo.CONNECTED)
}

func TestRegisterAcceptedRejectedAndLocalValidation(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	subject, err := natswire.RegistrationSubject("simulator")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	traceHeaders := make(chan string, 2)
	_, err = core.Subscribe(subject, func(message *natsgo.Msg) {
		requests.Add(1)
		traceHeaders <- message.Header.Get("traceparent")
		request, decodeErr := natswire.Decode[Registration](
			validator,
			contractsv1.RegistrationRequestSchemaID,
			message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode registration request: %v", decodeErr)
			return
		}
		responseID := mustID(t, "rep")
		causationID := request.ID
		response := natswire.Envelope[RegistrationResponse]{
			ID: responseID, Schema: contractsv1.RegistrationResponseSchemaID, EmittedAt: nowString(),
			CorrelationID: request.CorrelationID, CausationID: &causationID,
		}
		if request.Data.BindingKey == "rejected-light" {
			response.Data = RegistrationResponse{
				Status: "rejected",
				Error:  &RegistrationError{Code: "identity_conflict", Message: "binding is already owned"},
			}
		} else {
			response.Data = RegistrationResponse{Status: "accepted", Binding: &Binding{
				BindingKey: request.Data.BindingKey,
				DeviceID:   "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				Entities:   []EntityBinding{{Key: "power", EntityID: testEntityID, Enabled: true}},
			}}
		}
		payload, encodeErr := natswire.Encode(validator, contractsv1.RegistrationResponseSchemaID, response)
		if encodeErr != nil {
			t.Errorf("encode registration response: %v", encodeErr)
			return
		}
		if respondErr := message.Respond(payload); respondErr != nil {
			t.Errorf("respond to registration: %v", respondErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	session := connectSession(t, server.ClientURL())
	registration := validRegistration("office-light")
	registerContext := trace.ContextWithSpanContext(testContext(t), sampleSpanContext())
	binding, err := session.Register(registerContext, registration)
	if err != nil {
		t.Fatal(err)
	}
	if binding.BindingKey != "office-light" || binding.Entities[0].EntityID != testEntityID ||
		!binding.Entities[0].Enabled {
		t.Fatalf("binding = %#v", binding)
	}
	if traceHeader := <-traceHeaders; traceHeader == "" {
		t.Fatal("registration request omitted W3C trace context")
	}

	registration.BindingKey = "rejected-light"
	_, err = session.Register(testContext(t), registration)
	<-traceHeaders
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) || rejected.Code != RegistrationIdentityConflict {
		t.Fatalf("rejection = %#v, error = %v", rejected, err)
	}

	registration = validRegistration("bad.binding")
	_, err = session.Register(testContext(t), registration)
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("local validation error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("core received %d requests, want 2", got)
	}
}

func TestRegisterNoResponderRemainsRequestError(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	_, err := session.Register(testContext(t), validRegistration("office-light"))
	if !errors.Is(err, natsgo.ErrNoResponders) {
		t.Fatalf("registration error = %v, want no responders", err)
	}
	var rejected *RegistrationRejectedError
	var validation *ValidationError
	if errors.As(err, &rejected) || errors.As(err, &validation) {
		t.Fatalf("transient request error was classified as permanent: %v", err)
	}
}

func TestSetEntityEnabledRoundTripsAcceptedAndTypedRejectedResponses(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	subject, err := natswire.EntityEnablementSubject("simulator", testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	_, err = core.Subscribe(subject, func(message *natsgo.Msg) {
		requests.Add(1)
		request, decodeErr := natswire.Decode[EntityEnablementRequest](
			validator, contractsv1.EntityEnablementRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode Entity enablement request: %v", decodeErr)
			return
		}
		causationID := request.ID
		response := natswire.Envelope[EntityEnablementResponse]{
			ID: mustID(t, "rep"), Schema: contractsv1.EntityEnablementResponseSchemaID,
			EmittedAt: nowString(), CorrelationID: request.CorrelationID, CausationID: &causationID,
		}
		if request.Data.Enabled {
			response.Data = EntityEnablementResponse{
				Status: "rejected",
				Error: &EntityEnablementError{
					Code: EntityEnablementWrongAdapter, Message: "entity is owned by another adapter",
				},
			}
		} else {
			confirmed := false
			response.Data = EntityEnablementResponse{
				Status: "accepted", EntityID: request.Data.EntityID, Enabled: &confirmed,
			}
		}
		payload, encodeErr := natswire.Encode(validator, contractsv1.EntityEnablementResponseSchemaID, response)
		if encodeErr != nil {
			t.Errorf("encode Entity enablement response: %v", encodeErr)
			return
		}
		if respondErr := message.Respond(payload); respondErr != nil {
			t.Errorf("respond to Entity enablement: %v", respondErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	session := connectSession(t, server.ClientURL())
	confirmed, err := session.SetEntityEnabled(testContext(t), testEntityID, false)
	if err != nil || confirmed {
		t.Fatalf("accepted result = %t, %v", confirmed, err)
	}
	_, err = session.SetEntityEnabled(testContext(t), testEntityID, true)
	var rejected *EntityEnablementRejectedError
	if !errors.As(err, &rejected) || rejected.Code != EntityEnablementWrongAdapter {
		t.Fatalf("rejection = %#v, error = %v", rejected, err)
	}
	if _, validationErr := session.SetEntityEnabled(testContext(t), "not-an-entity", false); validationErr == nil {
		t.Fatal("invalid local Entity ID unexpectedly accepted")
	} else {
		if _, ok := errors.AsType[*ValidationError](validationErr); !ok {
			t.Fatalf("local validation error = %v", validationErr)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("Core requests = %d, want 2", requests.Load())
	}
}

func TestPublishObservationWaitsForAcknowledgement(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	stream := createObservationStream(t, core)
	session := connectSession(t, server.ClientURL())

	publishContext := trace.ContextWithSpanContext(testContext(t), sampleSpanContext())
	observationID, err := session.PublishObservation(publishContext, Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Header.Get(natsgo.MsgIdHdr) != string(observationID) {
		t.Fatalf("Nats-Msg-Id = %q, want %q", stored.Header.Get(natsgo.MsgIdHdr), observationID)
	}
	if stored.Header.Get("traceparent") == "" {
		t.Fatal("observation omitted W3C trace context")
	}
	validator := compileValidator(t)
	envelope, err := natswire.Decode[Observation](validator, contractsv1.ObservationSchemaID, stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ID != string(observationID) || envelope.Data.EntityID != testEntityID {
		t.Fatalf("stored observation = %#v", envelope)
	}
}

func TestPublishObservationRetriesSameIDAfterReconnect(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	server := startServer(t, -1, storeDir)
	core := connectNATS(t, server.ClientURL())
	createObservationStream(t, core)
	session := connectSession(t, server.ClientURL())
	port := server.Addr().(*net.TCPAddr).Port

	server.Shutdown()
	server.WaitForShutdown()
	waitForConnectionStatus(t, session.connection, natsgo.RECONNECTING)

	type publishResult struct {
		id  ObservationID
		err error
	}
	result := make(chan publishResult, 1)
	publishContext, cancelPublish := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPublish()
	go func() {
		id, err := session.PublishObservation(publishContext, Observation{
			EntityID: testEntityID, Value: json.RawMessage(`false`), AdapterReceivedAt: nowString(),
		})
		result <- publishResult{id: id, err: err}
	}()

	restarted := startServer(t, port, storeDir)
	published := <-result
	if published.err != nil {
		t.Fatal(published.err)
	}
	reconnectedCore := connectNATS(t, restarted.ClientURL())
	js, err := jetstream.New(reconnectedCore)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(testContext(t), "HEARTH_OBSERVATIONS_V1")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Header.Get(natsgo.MsgIdHdr) != string(published.id) {
		t.Fatalf("retried Nats-Msg-Id = %q, want %q", stored.Header.Get(natsgo.MsgIdHdr), published.id)
	}
	observation, err := natswire.Decode[Observation](compileValidator(t), contractsv1.ObservationSchemaID, stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ID != string(published.id) {
		t.Fatalf("retried envelope ID = %q, want %q", observation.ID, published.id)
	}
}

func TestServeCommandsInvokesHandlersConcurrentlyAndRespondsOnce(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	session := connectSession(t, server.ClientURL())
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	entered := make(chan Command, 2)
	release := make(chan struct{})
	secondReplies := make(chan error, 2)
	traceIDs := make(chan trace.TraceID, 2)
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(ctx context.Context, command Command, responder Responder) error {
			traceIDs <- trace.SpanContextFromContext(ctx).TraceID()
			entered <- command
			<-release
			if err := responder.Accept(); err != nil {
				return err
			}
			secondReplies <- responder.Reject("")
			return nil
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	spanContext := sampleSpanContext()
	requestContext := trace.ContextWithSpanContext(context.Background(), spanContext)
	replies := make(chan *natsgo.Msg, 2)
	errorsChannel := make(chan error, 2)
	for _, value := range []bool{true, false} {
		go func() {
			reply, requestErr := sendCommand(requestContext, core, value)
			replies <- reply
			errorsChannel <- requestErr
		}()
	}
	first := <-entered
	second := <-entered
	if first.ID == second.ID {
		t.Fatal("concurrent commands reused an ID")
	}
	close(release)

	validator := compileValidator(t)
	for range 2 {
		if requestErr := <-errorsChannel; requestErr != nil {
			t.Fatal(requestErr)
		}
		reply := <-replies
		response, decodeErr := natswire.Decode[CommandResponse](
			validator,
			contractsv1.CommandResponseSchemaID,
			reply.Data,
		)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if response.Data.Status != "accepted" || response.CausationID == nil ||
			response.Data.CommandID != *response.CausationID {
			t.Fatalf("command response = %#v", response)
		}
		if reply.Header.Get("traceparent") == "" {
			t.Fatal("command response omitted W3C trace context")
		}
		if secondErr := <-secondReplies; !errors.Is(secondErr, ErrAlreadyResponded) {
			t.Fatalf("second response error = %v", secondErr)
		}
		if got := <-traceIDs; got != spanContext.TraceID() {
			t.Fatalf("handler trace ID = %s, want %s", got, spanContext.TraceID())
		}
	}

	cancelServe()
	if err := <-serveDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("ServeCommands error = %v", err)
	}
}

func TestCommandHandlerUsesTransmittedDeadline(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	session := connectSession(t, server.ClientURL())
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	type deadlineResult struct {
		deadline time.Time
		ok       bool
	}
	observedDeadline := make(chan deadlineResult, 1)
	handlerDone := make(chan error, 1)
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(ctx context.Context, _ Command, _ Responder) error {
			deadline, ok := ctx.Deadline()
			observedDeadline <- deadlineResult{deadline: deadline, ok: ok}
			if !ok {
				return errors.New("command context has no deadline")
			}
			<-ctx.Done()
			handlerDone <- ctx.Err()
			return nil
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	commandDeadline := time.Now().UTC().Add(250 * time.Millisecond)
	requestDone := make(chan error, 1)
	go func() {
		_, err := sendCommandWithDeadline(context.Background(), core, true, time.Second, commandDeadline)
		requestDone <- err
	}()

	select {
	case observed := <-observedDeadline:
		if !observed.ok || !observed.deadline.Equal(commandDeadline) {
			t.Fatalf("handler deadline = %v, %t; want %v, true", observed.deadline, observed.ok, commandDeadline)
		}
	case <-time.After(time.Second):
		t.Fatal("command handler was not invoked")
	}
	select {
	case err := <-handlerDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handler context error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("command handler context was not canceled at the transmitted deadline")
	}
	if err := <-requestDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want deadline exceeded", err)
	}

	cancelServe()
	if err := <-serveDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("ServeCommands error = %v", err)
	}
}

func TestCommandRejectionUsesUpstreamRejectedCode(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	session := connectSession(t, server.ClientURL())
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(_ context.Context, _ Command, responder Responder) error {
			return responder.Reject("vendor declined the command")
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, true)
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
	if response.Data.Status != "rejected" || response.Data.Error == nil ||
		response.Data.Error.Code != "upstream_rejected" {
		t.Fatalf("command response = %#v", response)
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v", serveErr)
	}
}

func TestCommandPublishFailureDoesNotConsumeResponder(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	responder := &commandResponder{
		context:       context.Background(),
		connection:    session.connection,
		replySubject:  "_INBOX.command-response",
		validator:     compileValidator(t),
		commandID:     mustID(t, "cmd"),
		correlationID: mustID(t, "cor"),
	}
	session.connection.Close()

	if err := responder.Accept(); err == nil || errors.Is(err, ErrAlreadyResponded) {
		t.Fatalf("first response error = %v, want publish error", err)
	}
	if responder.didRespond() {
		t.Fatal("failed publish consumed responder")
	}
	if err := responder.Accept(); err == nil || errors.Is(err, ErrAlreadyResponded) {
		t.Fatalf("retry response error = %v, want publish error", err)
	}
}

func TestLinkedObservationReusesCommandCausality(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	stream := createObservationStream(t, core)
	session := connectSession(t, server.ClientURL())
	serveContext := t.Context()
	published := make(chan error, 1)
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(ctx context.Context, command Command, responder Responder) error {
			if err := responder.Accept(); err != nil {
				return err
			}
			_, err := session.PublishObservation(ctx, Observation{
				EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(), RefreshForCommand: &command.ID,
			})
			published <- err
			return err
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, true)
	if err != nil {
		t.Fatal(err)
	}
	if publishErr := <-published; publishErr != nil {
		t.Fatal(publishErr)
	}
	validator := compileValidator(t)
	response, err := natswire.Decode[CommandResponse](validator, contractsv1.CommandResponseSchemaID, reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stream.GetMsg(testContext(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := natswire.Decode[Observation](validator, contractsv1.ObservationSchemaID, stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	if observation.CausationID == nil || *observation.CausationID != response.Data.CommandID ||
		observation.CorrelationID != response.CorrelationID {
		t.Fatalf("linked observation causality = %#v; response = %#v", observation, response)
	}
}

func TestMissingCommandResponseLetsRequestTimeOut(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	session := connectSession(t, server.ClientURL())
	serveContext := t.Context()
	handled := make(chan struct{}, 1)
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(context.Context, Command, Responder) error {
			handled <- struct{}{}
			return nil
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	_, err := sendCommandWithTimeout(context.Background(), core, true, 200*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want deadline exceeded", err)
	}
	select {
	case <-handled:
	default:
		t.Fatal("command handler was not invoked")
	}
}

func TestCloseWaitsForCommandHandlers(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	session := connectSession(t, server.ClientURL())
	serveContext := t.Context()
	entered := make(chan struct{})
	release := make(chan struct{})
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(_ context.Context, _ Command, responder Responder) error {
			close(entered)
			<-release
			return responder.Accept()
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	requestDone := make(chan error, 1)
	go func() {
		_, err := sendCommand(context.Background(), core, true)
		requestDone <- err
	}()
	<-entered
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while command handler was running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if err := <-requestDone; err != nil {
		t.Fatalf("command request failed during Close: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-serveDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("ServeCommands error = %v, want closed", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func validRegistration(bindingKey string) Registration {
	return Registration{
		BindingKey: bindingKey,
		Device:     DeviceDescriptor{Name: "Office Light", Kind: "light"},
		Entities: []EntityDescriptor{{
			Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		}},
	}
}

func sendCommand(ctx context.Context, connection *natsgo.Conn, value bool) (*natsgo.Msg, error) {
	return sendCommandWithTimeout(ctx, connection, value, 3*time.Second)
}

func sendCommandWithTimeout(
	ctx context.Context,
	connection *natsgo.Conn,
	value bool,
	timeout time.Duration,
) (*natsgo.Msg, error) {
	return sendCommandWithDeadline(ctx, connection, value, timeout, time.Now().UTC().Add(10*time.Second))
}

func sendCommandWithDeadline(
	ctx context.Context,
	connection *natsgo.Conn,
	value bool,
	timeout time.Duration,
	deadline time.Time,
) (*natsgo.Msg, error) {
	validator, err := contractsv1.Compile()
	if err != nil {
		return nil, err
	}
	commandID, err := newID("cmd")
	if err != nil {
		return nil, err
	}
	correlationID, err := newID("cor")
	if err != nil {
		return nil, err
	}
	subject, err := natswire.CommandSubject("simulator", testEntityID, "set")
	if err != nil {
		return nil, err
	}
	payload, err := natswire.Encode(validator, contractsv1.CommandRequestSchemaID, natswire.Envelope[Command]{
		ID:            commandID,
		Schema:        contractsv1.CommandRequestSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		Data: Command{
			EntityID:      testEntityID,
			OperationName: "set",
			Parameters:    json.RawMessage(mustJSON(value)),
			Deadline:      deadline.UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		return nil, err
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, message.Header)
	requestContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return connection.RequestMsgWithContext(requestContext, message)
}

func mustJSON(value bool) string {
	if value {
		return `{"value":true}`
	}
	return `{"value":false}`
}

func startServer(t *testing.T, port int, storeDir string) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: port, NoSigs: true, NoLog: true, JetStream: true, StoreDir: storeDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func connectNATS(t *testing.T, url string) *natsgo.Conn {
	t.Helper()
	connection, err := natsgo.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	return connection
}

func connectSession(t *testing.T, url string) *Session {
	t.Helper()
	session, err := Connect(testContext(t), Config{AdapterID: "simulator", NATSURL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func createObservationStream(t *testing.T, connection *natsgo.Conn) jetstream.Stream {
	t.Helper()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateStream(testContext(t), jetstream.StreamConfig{
		Name:     "HEARTH_OBSERVATIONS_V1",
		Subjects: []string{"hearth.v1.adapter.*.observation.>"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func waitForSubscription(t *testing.T, server *natsserver.Server, previous uint32, serveDone <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-serveDone:
			t.Fatalf("ServeCommands returned before activation: %v", err)
		default:
		}
		if server.NumSubscriptions() > previous {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("command subscription did not become active")
}

func waitForConnectionStatus(t *testing.T, connection *natsgo.Conn, status natsgo.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connection.Status() == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("NATS connection status = %s, want %s", connection.Status(), status)
}

func sampleSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
}

func compileValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func mustID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := newID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
