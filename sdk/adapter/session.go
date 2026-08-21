package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/propagation"
)

const subjectPrefix = "hearth.v1.adapter"

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type Session struct {
	adapterID    string
	connection   *natsgo.Conn
	jetstream    jetstream.JetStream
	validator    *contractsv1.Validator
	propagator   propagation.TextMapPropagator
	closed       chan struct{}
	closeOnce    sync.Once
	closeErr     error
	handlerMutex sync.Mutex
	handlerWait  sync.WaitGroup
	closing      bool
}

type envelope[T any] struct {
	ID            string  `json:"id"`
	Schema        string  `json:"schema"`
	EmittedAt     string  `json:"emitted_at"`
	CorrelationID string  `json:"correlation_id"`
	CausationID   *string `json:"causation_id,omitempty"`
	Data          T       `json:"data"`
}

type commandMetadata struct {
	id            string
	correlationID string
}

type commandMetadataKey struct{}

// Connect validates config, compiles the embedded wire schemas, and connects to NATS.
func Connect(ctx context.Context, config Config) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !slugPattern.MatchString(config.AdapterID) {
		return nil, &ValidationError{Err: fmt.Errorf("invalid adapter ID %q", config.AdapterID)}
	}
	if config.NATSURL == "" {
		return nil, &ValidationError{Err: errors.New("NATS URL is required")}
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile wire schemas: %w", err)
	}

	options := []natsgo.Option{
		natsgo.Name("hearth-adapter-" + config.AdapterID),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(250 * time.Millisecond),
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		options = append(options, natsgo.Timeout(remaining))
	}
	connection, err := natsgo.Connect(config.NATSURL, options...)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}
	return &Session{
		adapterID:  config.AdapterID,
		connection: connection,
		jetstream:  js,
		validator:  validator,
		propagator: propagation.TraceContext{},
		closed:     make(chan struct{}),
	}, nil
}

// Register performs one schema-validated Core NATS request/reply attempt.
func (session *Session) Register(ctx context.Context, registration Registration) (Binding, error) {
	requestID, err := newID("reg")
	if err != nil {
		return Binding{}, err
	}
	correlationID, err := newID("cor")
	if err != nil {
		return Binding{}, err
	}
	request := envelope[Registration]{
		ID:            requestID,
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		Data:          registration,
	}
	payload, err := session.encode(contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		return Binding{}, &ValidationError{Err: err}
	}
	message := &natsgo.Msg{
		Subject: registrationSubject(session.adapterID),
		Header:  make(natsgo.Header),
		Data:    payload,
	}
	session.propagator.Inject(ctx, headerCarrier(message.Header))
	reply, err := session.connection.RequestMsgWithContext(ctx, message)
	if err != nil {
		return Binding{}, err
	}
	if err := session.validator.Validate(contractsv1.RegistrationResponseSchemaID, reply.Data); err != nil {
		return Binding{}, fmt.Errorf("invalid registration response: %w", err)
	}
	var response envelope[RegistrationResponse]
	if err := json.Unmarshal(reply.Data, &response); err != nil {
		return Binding{}, fmt.Errorf("decode registration response: %w", err)
	}
	if response.CausationID == nil || *response.CausationID != requestID {
		return Binding{}, errors.New("registration response causation ID does not match request")
	}
	if response.CorrelationID != correlationID {
		return Binding{}, errors.New("registration response correlation ID does not match request")
	}
	if response.Data.Status == "rejected" {
		return Binding{}, &RegistrationRejectedError{
			Code:    RegistrationRejectionCode(response.Data.Error.Code),
			Message: response.Data.Error.Message,
		}
	}
	return *response.Data.Binding, nil
}

// PublishObservation publishes one envelope through JetStream and waits for its
// acknowledgement. A command-linked observation must use the context received
// by that command's handler so its causation and correlation IDs are preserved.
func (session *Session) PublishObservation(ctx context.Context, observation Observation) (ObservationID, error) {
	generated, err := newID("obs")
	if err != nil {
		return "", err
	}
	observationID := ObservationID(generated)
	correlationID, err := newID("cor")
	if err != nil {
		return observationID, err
	}
	var causationID *string
	if observation.RefreshForCommand != nil {
		metadata, ok := ctx.Value(commandMetadataKey{}).(commandMetadata)
		if !ok || metadata.id != *observation.RefreshForCommand {
			return observationID, &ValidationError{Err: errors.New("linked observation requires its command handler context")}
		}
		correlationID = metadata.correlationID
		causationID = observation.RefreshForCommand
	}
	event := envelope[Observation]{
		ID:            generated,
		Schema:        contractsv1.ObservationSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		CausationID:   causationID,
		Data:          observation,
	}
	payload, err := session.encode(contractsv1.ObservationSchemaID, event)
	if err != nil {
		return observationID, &ValidationError{Err: err}
	}
	subject := observationSubject(session.adapterID, observation.EntityID)
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, generated)
	session.propagator.Inject(ctx, headerCarrier(headers))

	for {
		message := &natsgo.Msg{Subject: subject, Header: headers, Data: payload}
		if _, err = session.jetstream.PublishMsg(ctx, message); err == nil {
			return observationID, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return observationID, ctxErr
		}
		if !isTransientPublishError(err) {
			return observationID, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return observationID, ctx.Err()
		case <-session.closed:
			timer.Stop()
			return observationID, ErrClosed
		case <-timer.C:
		}
	}
}

// ServeCommands handles valid command requests concurrently until ctx ends.
func (session *Session) ServeCommands(ctx context.Context, handler CommandHandler) error {
	if handler == nil {
		return &ValidationError{Err: errors.New("command handler is required")}
	}
	wildcard := commandWildcard(session.adapterID)
	subscription, err := session.connection.Subscribe(wildcard, func(message *natsgo.Msg) {
		session.startCommandHandler(ctx, message, handler)
	})
	if err != nil {
		return fmt.Errorf("subscribe to commands: %w", err)
	}
	flushContext, cancelFlush := context.WithTimeout(ctx, 5*time.Second)
	err = session.connection.FlushWithContext(flushContext)
	cancelFlush()
	if err != nil {
		_ = subscription.Unsubscribe()
		return fmt.Errorf("activate command subscription: %w", err)
	}
	select {
	case <-ctx.Done():
		if err := subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
			return fmt.Errorf("drain command subscription: %w", err)
		}
		return ctx.Err()
	case <-session.closed:
		return ErrClosed
	}
}

// Close idempotently drains the NATS connection.
func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		session.handlerMutex.Lock()
		session.closing = true
		session.handlerMutex.Unlock()
		session.handlerWait.Wait()
		close(session.closed)
		session.closeErr = session.connection.Drain()
		if session.closeErr != nil {
			session.connection.Close()
		}
	})
	return session.closeErr
}

func (session *Session) startCommandHandler(parent context.Context, message *natsgo.Msg, handler CommandHandler) {
	session.handlerMutex.Lock()
	if session.closing {
		session.handlerMutex.Unlock()
		return
	}
	session.handlerWait.Add(1)
	session.handlerMutex.Unlock()
	go func() {
		defer session.handlerWait.Done()
		session.handleCommand(parent, message, handler)
	}()
}

func (session *Session) handleCommand(parent context.Context, message *natsgo.Msg, handler CommandHandler) {
	if message.Reply == "" {
		slog.Error("discarding command without reply subject", "subject", message.Subject)
		return
	}
	if err := session.validator.Validate(contractsv1.CommandRequestSchemaID, message.Data); err != nil {
		slog.Error("discarding invalid command", "subject", message.Subject, "error", err)
		return
	}
	var request envelope[Command]
	if err := json.Unmarshal(message.Data, &request); err != nil {
		slog.Error("discarding undecodable command", "subject", message.Subject, "error", err)
		return
	}
	entityID, operation, ok := parseCommandSubject(session.adapterID, message.Subject)
	if !ok || entityID != request.Data.EntityID || operation != request.Data.Operation || request.CausationID != nil {
		slog.Error("discarding command with mismatched routing", "subject", message.Subject, "command_id", request.ID)
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, request.Data.Deadline)
	if err != nil {
		slog.Error("discarding command with invalid deadline", "subject", message.Subject, "command_id", request.ID, "error", err)
		return
	}

	ctx := session.propagator.Extract(parent, headerCarrier(message.Header))
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		slog.Error("discarding expired command", "subject", message.Subject, "command_id", request.ID, "error", err)
		return
	}
	ctx = context.WithValue(ctx, commandMetadataKey{}, commandMetadata{id: request.ID, correlationID: request.CorrelationID})
	command := request.Data
	command.ID = request.ID
	command.CorrelationID = request.CorrelationID
	responder := &commandResponder{
		context:       ctx,
		connection:    session.connection,
		replySubject:  message.Reply,
		validator:     session.validator,
		propagator:    session.propagator,
		commandID:     request.ID,
		correlationID: request.CorrelationID,
	}
	if err := handler(ctx, command, responder); err != nil {
		slog.Error("command handler failed", "command_id", request.ID, "error", err)
	}
	if !responder.didRespond() {
		slog.Error(ErrMissingResponse.Error(), "command_id", request.ID)
	}
}

func (session *Session) encode(schemaID string, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	if err := session.validator.Validate(schemaID, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

type commandResponder struct {
	context       context.Context
	connection    *natsgo.Conn
	replySubject  string
	validator     *contractsv1.Validator
	propagator    propagation.TextMapPropagator
	commandID     string
	correlationID string
	mutex         sync.Mutex
	responded     bool
}

func (responder *commandResponder) Accept() error {
	return responder.respond(CommandResponse{CommandID: responder.commandID, Status: "accepted"})
}

func (responder *commandResponder) Reject(message string) error {
	return responder.respond(CommandResponse{
		CommandID: responder.commandID,
		Status:    "rejected",
		Error:     &CommandError{Code: "upstream_rejected", Message: message},
	})
}

func (responder *commandResponder) respond(response CommandResponse) error {
	if responder.didRespond() {
		return ErrAlreadyResponded
	}
	replyID, err := newID("rep")
	if err != nil {
		return err
	}
	causationID := responder.commandID
	envelope := envelope[CommandResponse]{
		ID:            replyID,
		Schema:        contractsv1.CommandResponseSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: responder.correlationID,
		CausationID:   &causationID,
		Data:          response,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode command response: %w", err)
	}
	if err := responder.validator.Validate(contractsv1.CommandResponseSchemaID, payload); err != nil {
		return &ValidationError{Err: err}
	}

	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if responder.responded {
		return ErrAlreadyResponded
	}
	message := &natsgo.Msg{Subject: responder.replySubject, Header: make(natsgo.Header), Data: payload}
	responder.propagator.Inject(responder.context, headerCarrier(message.Header))
	if err := responder.connection.PublishMsg(message); err != nil {
		return fmt.Errorf("publish command response: %w", err)
	}
	responder.responded = true
	return nil
}

func (responder *commandResponder) didRespond() bool {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	return responder.responded
}

func newID(prefix string) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate %s ID: %w", prefix, err)
	}
	return prefix + "_" + id.String(), nil
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func registrationSubject(adapterID string) string {
	return subjectPrefix + "." + adapterID + ".register"
}

func observationSubject(adapterID, entityID string) string {
	return subjectPrefix + "." + adapterID + ".observation." + entityID
}

func commandWildcard(adapterID string) string {
	return subjectPrefix + "." + adapterID + ".command.*.*"
}

func parseCommandSubject(adapterID, subject string) (string, string, bool) {
	parts := strings.Split(subject, ".")
	if len(parts) != 7 || strings.Join(parts[:3], ".") != subjectPrefix || parts[3] != adapterID || parts[4] != "command" {
		return "", "", false
	}
	return parts[5], parts[6], true
}

func isTransientPublishError(err error) bool {
	return errors.Is(err, natsgo.ErrDisconnected) ||
		errors.Is(err, natsgo.ErrNoResponders) ||
		errors.Is(err, natsgo.ErrTimeout) ||
		errors.Is(err, context.DeadlineExceeded)
}
