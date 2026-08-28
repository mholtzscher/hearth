package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

type Session struct {
	adapterID    string
	connection   *natsgo.Conn
	jetstream    jetstream.JetStream
	validator    *contractsv1.Validator
	logger       *slog.Logger
	closed       chan struct{}
	closeOnce    sync.Once
	closeErr     error
	handlerMutex sync.Mutex
	handlerWait  sync.WaitGroup
	closing      bool
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
	if _, err := natswire.RegistrationSubject(config.AdapterID); err != nil {
		return nil, &ValidationError{Err: err}
	}
	if config.NATSURL == "" {
		return nil, &ValidationError{Err: errors.New("NATS URL is required")}
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile wire schemas: %w", err)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	options := []natsgo.Option{
		natsgo.Name("hearth-adapter-" + config.AdapterID),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(250 * time.Millisecond),
		natsgo.RetryOnFailedConnect(true),
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
		logger:     logger,
		closed:     make(chan struct{}),
	}, nil
}

// requestReply performs one schema-validated Core NATS request/reply exchange:
// it envelopes and encodes data, sends it to subject, decodes the reply, and
// verifies causation and correlation IDs. Rejection handling and response
// identity checks remain with the caller.
func requestReply[Req, Resp any](
	ctx context.Context,
	session *Session,
	prefix, requestSchema, responseSchema, kind, subject string,
	data Req,
) (natswire.Envelope[Resp], error) {
	requestID, err := newID(prefix)
	if err != nil {
		return natswire.Envelope[Resp]{}, err
	}
	correlationID, err := newID("cor")
	if err != nil {
		return natswire.Envelope[Resp]{}, err
	}
	envelope := natswire.Envelope[Req]{
		ID:            requestID,
		Schema:        requestSchema,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		Data:          data,
	}
	payload, err := natswire.Encode(session.validator, requestSchema, envelope)
	if err != nil {
		return natswire.Envelope[Resp]{}, &ValidationError{Err: err}
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, message.Header)
	reply, err := session.connection.RequestMsgWithContext(ctx, message)
	if err != nil {
		return natswire.Envelope[Resp]{}, err
	}
	response, err := natswire.Decode[Resp](session.validator, responseSchema, reply.Data)
	if err != nil {
		return natswire.Envelope[Resp]{}, fmt.Errorf("invalid %s response: %w", kind, err)
	}
	if response.CausationID == nil || *response.CausationID != requestID {
		return natswire.Envelope[Resp]{}, errors.New("response causation ID does not match request")
	}
	if response.CorrelationID != correlationID {
		return natswire.Envelope[Resp]{}, errors.New("response correlation ID does not match request")
	}
	return response, nil
}

// Register performs one schema-validated Core NATS request/reply attempt.
func (session *Session) Register(ctx context.Context, registration Registration) (Binding, error) {
	subject, err := natswire.RegistrationSubject(session.adapterID)
	if err != nil {
		return Binding{}, &ValidationError{Err: err}
	}
	response, err := requestReply[Registration, RegistrationResponse](
		ctx, session, "reg",
		contractsv1.RegistrationRequestSchemaID, contractsv1.RegistrationResponseSchemaID,
		"registration", subject, registration,
	)
	if err != nil {
		return Binding{}, err
	}
	if response.Data.Status == "rejected" {
		return Binding{}, &RegistrationRejectedError{
			Code:    RegistrationRejectionCode(response.Data.Error.Code),
			Message: response.Data.Error.Message,
		}
	}
	return *response.Data.Binding, nil
}

// SetEntityEnabled performs one schema-validated Core NATS request/reply attempt.
func (session *Session) SetEntityEnabled(ctx context.Context, entityID string, enabled bool) (bool, error) {
	subject, err := natswire.EntityEnablementSubject(session.adapterID, entityID)
	if err != nil {
		return false, &ValidationError{Err: err}
	}
	route, err := natswire.ParseEntityEnablementSubject(subject)
	if err != nil || route.AdapterID != session.adapterID || route.EntityID != entityID {
		if err == nil {
			err = errors.New("entity enablement route does not match request")
		}
		return false, &ValidationError{Err: err}
	}
	response, err := requestReply[EntityEnablementRequest, EntityEnablementResponse](
		ctx, session, "ena",
		contractsv1.EntityEnablementRequestSchemaID, contractsv1.EntityEnablementResponseSchemaID,
		"entity enablement", subject, EntityEnablementRequest{EntityID: entityID, Enabled: enabled},
	)
	if err != nil {
		return false, err
	}
	if response.Data.Status == "rejected" {
		return false, &EntityEnablementRejectedError{
			Code: response.Data.Error.Code, Message: response.Data.Error.Message,
		}
	}
	if response.Data.EntityID != entityID || response.Data.Enabled == nil {
		return false, errors.New("entity enablement response identity does not match request")
	}
	return *response.Data.Enabled, nil
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
			return observationID, &ValidationError{
				Err: errors.New("linked observation requires its command handler context"),
			}
		}
		correlationID = metadata.correlationID
		causationID = observation.RefreshForCommand
	}
	event := natswire.Envelope[Observation]{
		ID:            generated,
		Schema:        contractsv1.ObservationSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		CausationID:   causationID,
		Data:          observation,
	}
	payload, err := natswire.Encode(session.validator, contractsv1.ObservationSchemaID, event)
	if err != nil {
		return observationID, &ValidationError{Err: err}
	}
	subject, err := natswire.ObservationSubject(session.adapterID, observation.EntityID)
	if err != nil {
		return observationID, &ValidationError{Err: err}
	}
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, generated)
	natswire.InjectTrace(ctx, headers)

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
	wildcard, err := natswire.CommandWildcard(session.adapterID)
	if err != nil {
		return &ValidationError{Err: err}
	}
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
		session.logger.ErrorContext(parent, "discarding command without reply subject", "subject", message.Subject)
		return
	}
	request, err := natswire.Decode[Command](session.validator, contractsv1.CommandRequestSchemaID, message.Data)
	if err != nil {
		session.logger.ErrorContext(parent, "discarding invalid command", "subject", message.Subject, "error", err)
		return
	}
	route, err := natswire.ParseCommandSubject(message.Subject)
	if err != nil || route.AdapterID != session.adapterID || route.EntityID != request.Data.EntityID ||
		route.OperationName != request.Data.OperationName || request.CausationID != nil {
		session.logger.ErrorContext(
			parent,
			"discarding command with mismatched routing",
			"subject",
			message.Subject,
			"command_id",
			request.ID,
		)
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, request.Data.Deadline)
	if err != nil {
		session.logger.ErrorContext(
			parent,
			"discarding command with invalid deadline",
			"subject",
			message.Subject,
			"command_id",
			request.ID,
			"error",
			err,
		)
		return
	}

	ctx := natswire.ExtractTrace(parent, message.Header)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		session.logger.ErrorContext(
			parent,
			"discarding expired command",
			"subject",
			message.Subject,
			"command_id",
			request.ID,
			"error",
			err,
		)
		return
	}
	ctx = context.WithValue(
		ctx,
		commandMetadataKey{},
		commandMetadata{id: request.ID, correlationID: request.CorrelationID},
	)
	command := request.Data
	command.ID = request.ID
	command.CorrelationID = request.CorrelationID
	responder := &commandResponder{
		context:       ctx,
		connection:    session.connection,
		replySubject:  message.Reply,
		validator:     session.validator,
		commandID:     request.ID,
		correlationID: request.CorrelationID,
	}
	if err := handler(ctx, command, responder); err != nil {
		session.logger.ErrorContext(parent, "command handler failed", "command_id", request.ID, "error", err)
	}
	if !responder.didRespond() {
		session.logger.ErrorContext(parent, ErrMissingResponse.Error(), "command_id", request.ID)
	}
}

type commandResponder struct {
	context       context.Context
	connection    *natsgo.Conn
	replySubject  string
	validator     *contractsv1.Validator
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
	envelope := natswire.Envelope[CommandResponse]{
		ID:            replyID,
		Schema:        contractsv1.CommandResponseSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: responder.correlationID,
		CausationID:   &causationID,
		Data:          response,
	}
	payload, err := natswire.Encode(responder.validator, contractsv1.CommandResponseSchemaID, envelope)
	if err != nil {
		return &ValidationError{Err: err}
	}

	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if responder.responded {
		return ErrAlreadyResponded
	}
	message := &natsgo.Msg{Subject: responder.replySubject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(responder.context, message.Header)
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

func isTransientPublishError(err error) bool {
	return errors.Is(err, natsgo.ErrDisconnected) ||
		errors.Is(err, natsgo.ErrNoResponders) ||
		errors.Is(err, natsgo.ErrTimeout) ||
		errors.Is(err, context.DeadlineExceeded)
}
