package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	statusAccepted        = "accepted"
	statusRejected        = "rejected"
	natsReconnectWait     = 250 * time.Millisecond
	requestRetryWait      = 100 * time.Millisecond
	jetStreamFlushTimeout = 5 * time.Second
)

type jetStreamPublisher interface {
	PublishMsg(context.Context, *natsgo.Msg, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

type Session struct {
	adapterID         string
	runtimeID         string
	heartbeatInterval time.Duration
	connection        *natsgo.Conn
	jetstream         jetStreamPublisher
	validator         *contractsv1.Validator
	logger            *slog.Logger
	connectCtx        context.Context
	lifecycleCtx      context.Context
	expectedClose     atomic.Bool
	fenceLogOnce      sync.Once

	stateMutex        sync.Mutex
	terminalErr       error
	closed            chan struct{}
	closedOnce        sync.Once
	closeOnce         sync.Once
	closeErr          error
	connectedOnce     atomic.Bool
	lifecycleCancel   context.CancelFunc
	heartbeatDone     chan struct{}
	heartbeatWake     chan struct{}
	heartbeatNotify   chan struct{}
	desiredHealth     HealthReport
	desiredGeneration uint64
	ackedGeneration   uint64
	ackedHealth       HealthReport
	ackedHealthSet    bool
	availabilityGate  chan struct{}

	handlerMutex sync.Mutex
	handlerWait  sync.WaitGroup
	closing      bool
}

// Connect validates config, connects to NATS, and claims one Core runtime before returning.
func Connect(ctx context.Context, config Config) (*Session, error) {
	if err := validateConfig(ctx, config); err != nil {
		return nil, err
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile wire schemas: %w", err)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	session := &Session{
		adapterID: config.AdapterID,
		logger: logger.With(
			"component", "adapter_session",
			"adapter_id", config.AdapterID,
		),
		connectCtx:       ctx,
		closed:           make(chan struct{}),
		heartbeatDone:    make(chan struct{}),
		heartbeatWake:    make(chan struct{}, 1),
		heartbeatNotify:  make(chan struct{}),
		desiredHealth:    HealthReport{Status: HealthUnknown, SourceObservedAt: time.Now().UTC()},
		availabilityGate: make(chan struct{}, 1),
	}
	session.availabilityGate <- struct{}{}

	options := []natsgo.Option{
		natsgo.Name("hearth-adapter-" + config.AdapterID),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(natsReconnectWait),
		natsgo.RetryOnFailedConnect(true),
		natsgo.ConnectHandler(session.onConnected),
		natsgo.DisconnectErrHandler(session.onDisconnected),
		natsgo.ReconnectHandler(session.onReconnected),
		natsgo.ClosedHandler(session.onClosed),
		natsgo.ErrorHandler(session.onAsyncError),
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
	// Initial success is reported through onConnected/onReconnected: with
	// RetryOnFailedConnect the client returns RECONNECTING before any
	// successful dial, so logging here would claim a milestone not yet reached.
	js, err := jetstream.New(connection)
	if err != nil {
		session.expectedClose.Store(true)
		connection.Close()
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}
	session.connection = connection
	session.jetstream = js
	session.validator = validator
	if claimErr := session.claim(ctx, config); claimErr != nil {
		session.expectedClose.Store(true)
		connection.Close()
		return nil, claimErr
	}
	// Derive the lifecycle from the Connect context without inheriting its
	// cancellation: connection callbacks and release preserve incoming
	// context values while keeping independent lifetime semantics.
	lifecycleContext, cancelLifecycle := context.WithCancel(context.WithoutCancel(ctx))
	session.stateMutex.Lock()
	session.lifecycleCtx = lifecycleContext
	session.lifecycleCancel = cancelLifecycle
	session.stateMutex.Unlock()
	go session.runHeartbeats(lifecycleContext)
	return session, nil
}

// onConnected reports the initial transport establishment exactly once.
// With RetryOnFailedConnect the first success may arrive via ReconnectHandler,
// so both callbacks share connectedOnce to distinguish initial from recovery.
func (session *Session) onConnected(_ *natsgo.Conn) {
	if session.isExpectedClose() || session.sessionError() != nil {
		return
	}
	if !session.connectedOnce.CompareAndSwap(false, true) {
		return
	}
	logger, ctx := session.callbackLog()
	logger.InfoContext(ctx, "connected to NATS",
		slog.String("event", "dependency.connected"),
		slog.String("dependency", "nats"),
	)
}

// onDisconnected reports an unexpected live-connection loss. Teardown paths set
// expectedClose or a terminal error first, so those quiet exits stay silent here.
func (session *Session) onDisconnected(_ *natsgo.Conn, _ error) {
	if session.isExpectedClose() || session.sessionError() != nil {
		return
	}
	logger, ctx := session.callbackLog()
	logger.WarnContext(ctx, "NATS connection lost",
		slog.String("event", "dependency.disconnected"),
		slog.String("dependency", "nats"),
		slog.String("error_code", "connection_lost"),
	)
}

// isExpectedClose reports whether connection teardown already began, either
// through explicit expectedClose or session lifecycle cancellation. The
// Connect context is consulted only before the independent lifecycle is
// established: the heartbeat lifecycle derives via WithoutCancel so cancelling
// the Connect context after a successful Connect must not quiet unexpected
// disconnect/reconnect/closed diagnostics while the session stays alive.
// It reads shared state safely for NATS callbacks, which run on the client
// library's goroutines.
func (session *Session) isExpectedClose() bool {
	if session.expectedClose.Load() {
		return true
	}
	session.stateMutex.Lock()
	lifecycle := session.lifecycleCtx
	session.stateMutex.Unlock()
	if lifecycle != nil {
		return lifecycle.Err() != nil
	}
	return session.connectCtx != nil && session.connectCtx.Err() != nil
}

// onReconnected reports transport recovery without claiming Adapter health.
// The first success after a failed initial dial arrives here, so it claims the
// initial connected milestone once before subsequent recoveries log reconnected.
func (session *Session) onReconnected(_ *natsgo.Conn) {
	if session.isExpectedClose() || session.sessionError() != nil {
		return
	}
	logger, ctx := session.callbackLog()
	if session.connectedOnce.CompareAndSwap(false, true) {
		logger.InfoContext(ctx, "connected to NATS",
			slog.String("event", "dependency.connected"),
			slog.String("dependency", "nats"),
		)
		return
	}
	logger.InfoContext(ctx, "reconnected to NATS",
		slog.String("event", "dependency.reconnected"),
		slog.String("dependency", "nats"),
	)
}

// onClosed distinguishes expected teardown from an unexpected terminal close.
// Lifecycle cancellation counts as expected even before Close runs: fencing
// and failure paths cancel the lifecycle first, so the resulting close is
// teardown rather than a new unexpected failure.
func (session *Session) onClosed(_ *natsgo.Conn) {
	logger, ctx := session.callbackLog()
	if session.isExpectedClose() {
		logger.DebugContext(ctx, "NATS connection closed",
			slog.String("event", "dependency.closed"),
			slog.String("dependency", "nats"),
		)
		return
	}
	logger.ErrorContext(ctx, "NATS connection closed unexpectedly",
		slog.String("event", "dependency.closed"),
		slog.String("dependency", "nats"),
		slog.String("reason_code", "unexpected_close"),
	)
}

// onAsyncError replaces the NATS client's default stderr ErrorHandler, which
// would leak arbitrary error text and full subjects. Async subscription
// errors carry untrusted content, so the record uses only a fixed diagnostic
// code and dependency identity. Teardown paths stay quiet here like the other
// connection callbacks.
func (session *Session) onAsyncError(_ *natsgo.Conn, _ *natsgo.Subscription, _ error) {
	if session.isExpectedClose() || session.sessionError() != nil {
		return
	}
	logger, ctx := session.callbackLog()
	logger.ErrorContext(ctx, "NATS async error",
		slog.String("event", "dependency.operation_failed"),
		slog.String("dependency", "nats"),
		slog.String("error_code", "nats_async_error"),
	)
}

// Register retries one schema-validated Core request under ctx. Transient
// attempts reuse the same envelope, preserving request identity after a lost reply.
func (session *Session) Register(ctx context.Context, registration Registration) (Binding, error) {
	if err := session.sessionError(); err != nil {
		return Binding{}, err
	}
	subject, err := natswire.RegistrationSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return Binding{}, &ValidationError{Err: err}
	}
	request, err := prepareRequest(
		session, "reg", contractsv1.RegistrationRequestSchemaID,
		contractsv1.RegistrationResponseSchemaID, "registration", subject, registration,
	)
	if err != nil {
		return Binding{}, err
	}
	episode := newRetryEpisode("registration", "core")
	for {
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[RegistrationResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := episode.waitRetry(ctx, session.log(),
				slog.String("error_code", requestErrorCode(requestErr)),
				requestRetryWait, requestErr,
			); retryErr != nil {
				return Binding{}, retryErr
			}
			continue
		}
		if response.Data.Status == statusRejected {
			code := RegistrationRejectionCode(response.Data.Error.Code)
			if code == registrationRuntimeFenced {
				session.markFenced(ctx)
				return Binding{}, ErrRuntimeFenced
			}
			session.log().WarnContext(ctx, "adapter registration rejected",
				slog.String("event", "adapter.registration_rejected"),
				slog.String("rejection_code", string(code)),
				slog.String("correlation_id", request.correlationID),
			)
			return Binding{}, &RegistrationRejectedError{
				Code: code, Message: response.Data.Error.Message,
			}
		}
		binding := *response.Data.Binding
		episode.succeeded(ctx, session.log())
		session.logRegistrationCompleted(ctx, request.correlationID, binding)
		return binding, nil
	}
}

// logRegistrationCompleted emits the bounded registration summary and one
// Debug record per returned mapping with canonical IDs.
func (session *Session) logRegistrationCompleted(
	ctx context.Context,
	correlationID string,
	binding Binding,
) {
	attrs := []any{
		slog.String("event", "adapter.registration_completed"),
		slog.String("device_id", binding.DeviceID),
		slog.Int("entity_count", len(binding.Entities)),
		slog.String("correlation_id", correlationID),
	}
	if len(binding.Entities) == 1 {
		attrs = append(attrs, slog.String("entity_id", binding.Entities[0].EntityID))
	}
	session.log().InfoContext(ctx, "adapter registration completed", attrs...)
	for _, entity := range binding.Entities {
		session.log().DebugContext(ctx, "adapter registration mapping",
			slog.String("event", "adapter.registration_mapping"),
			slog.String("device_id", binding.DeviceID),
			slog.String("entity_id", entity.EntityID),
			slog.String("entity_key", entity.Key),
		)
	}
}

type ownedMappingsResponse struct {
	Status     string              `json:"status"`
	Items      *[]OwnedMapping     `json:"items,omitempty"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Error      *ownedMappingsError `json:"error,omitempty"`
}

type ownedMappingsError struct {
	Code    OwnedMappingsRejectionCode `json:"code"`
	Message string                     `json:"message"`
}

// ListOwnedMappings returns one page of Binding and Entity mappings owned by the Session's Adapter.
func (session *Session) ListOwnedMappings(
	ctx context.Context,
	pageRequest OwnedMappingPageRequest,
) (OwnedMappingPage, error) {
	if err := session.sessionError(); err != nil {
		return OwnedMappingPage{}, err
	}
	if pageRequest.Limit < 0 || pageRequest.Limit > 200 {
		return OwnedMappingPage{}, &ValidationError{
			Err: errors.New("owned mappings limit must be 0 or between 1 and 200"),
		}
	}
	subject, err := natswire.OwnedMappingsSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return OwnedMappingPage{}, &ValidationError{Err: err}
	}
	request, err := prepareRequest(
		session, "map", contractsv1.OwnedMappingsRequestSchemaID,
		contractsv1.OwnedMappingsResponseSchemaID, "owned mappings", subject, pageRequest,
	)
	if err != nil {
		return OwnedMappingPage{}, err
	}
	episode := newRetryEpisode("owned_mappings", "core")
	for {
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[ownedMappingsResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := episode.waitRetry(ctx, session.log(),
				slog.String("error_code", requestErrorCode(requestErr)),
				requestRetryWait, requestErr,
			); retryErr != nil {
				return OwnedMappingPage{}, retryErr
			}
			continue
		}
		if response.Data.Status == statusRejected {
			if response.Data.Error.Code == ownedMappingsRuntimeFenced {
				session.markFenced(ctx)
				return OwnedMappingPage{}, ErrRuntimeFenced
			}
			return OwnedMappingPage{}, &OwnedMappingsRejectedError{
				Code: response.Data.Error.Code, Message: response.Data.Error.Message,
			}
		}
		episode.succeeded(ctx, session.log())
		return OwnedMappingPage{
			Items: *response.Data.Items, NextCursor: response.Data.NextCursor,
		}, nil
	}
}

// SetEntityEnabled performs one schema-validated Core NATS request/reply attempt.
func (session *Session) SetEntityEnabled(ctx context.Context, entityID string, enabled bool) (bool, error) {
	if err := session.sessionError(); err != nil {
		return false, err
	}
	subject, err := natswire.EntityEnablementSubject(session.adapterID, session.runtimeID, entityID)
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
	request, err := prepareRequest(
		session, "ena", contractsv1.EntityEnablementRequestSchemaID,
		contractsv1.EntityEnablementResponseSchemaID, "entity enablement", subject,
		EntityEnablementRequest{EntityID: entityID, Enabled: enabled},
	)
	if err != nil {
		return false, err
	}
	response, err := sendSessionRequest[EntityEnablementResponse](ctx, session, request)
	if err != nil {
		return false, err
	}
	if response.Data.Status == statusRejected {
		if response.Data.Error.Code == entityEnablementRuntimeFenced {
			session.markFenced(ctx)
			return false, ErrRuntimeFenced
		}
		return false, &EntityEnablementRejectedError{
			Code: response.Data.Error.Code, Message: response.Data.Error.Message,
		}
	}
	if response.Data.EntityID != entityID || response.Data.Enabled == nil {
		return false, errors.New("entity enablement response identity does not match request")
	}
	return *response.Data.Enabled, nil
}

// PublishObservation publishes one ordinary Observation through JetStream and
// waits for its acknowledgement.
func (session *Session) PublishObservation(ctx context.Context, observation Observation) (ObservationID, error) {
	return session.publishObservation(ctx, observation, nil)
}

// ServeCommands handles valid command requests concurrently until ctx ends.
func (session *Session) ServeCommands(ctx context.Context, handler CommandHandler) error {
	if err := session.sessionError(); err != nil {
		return err
	}
	if handler == nil {
		return &ValidationError{Err: errors.New("command handler is required")}
	}
	wildcard, err := natswire.CommandWildcard(session.adapterID, session.runtimeID)
	if err != nil {
		return &ValidationError{Err: err}
	}
	subscription, err := session.connection.Subscribe(wildcard, func(message *natsgo.Msg) {
		session.startCommandHandler(ctx, message, handler)
	})
	if err != nil {
		return fmt.Errorf("subscribe to commands: %w", err)
	}
	flushContext, cancelFlush := context.WithTimeout(ctx, jetStreamFlushTimeout)
	err = session.connection.FlushWithContext(flushContext)
	cancelFlush()
	if err != nil {
		_ = subscription.Unsubscribe()
		return fmt.Errorf("activate command subscription: %w", err)
	}
	session.log().InfoContext(ctx, "command subscription active",
		slog.String("event", "adapter.commands_listening"),
	)
	select {
	case <-ctx.Done():
		if drainErr := subscription.Drain(); drainErr != nil && !errors.Is(drainErr, natsgo.ErrConnectionClosed) {
			return fmt.Errorf("drain command subscription: %w", drainErr)
		}
		return ctx.Err()
	case <-session.closed:
		return session.sessionError()
	}
}

// Close releases the claimed runtime and idempotently drains the NATS connection.
func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		session.closeErr = session.close()
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
	// Extract the incoming trace before validation so every discard diagnostic
	// preserves the sender's trace context instead of a bare parent.
	traceCtx := natswire.ExtractTrace(parent, message.Header)
	if message.Reply == "" {
		session.log().WarnContext(traceCtx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("error_code", "missing_reply_subject"),
		)
		return
	}
	request, err := natswire.Decode[Command](session.validator, contractsv1.CommandRequestSchemaID, message.Data)
	if err != nil {
		session.log().WarnContext(traceCtx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("error_code", "invalid_envelope"),
		)
		return
	}
	route, err := natswire.ParseCommandSubject(message.Subject)
	if err != nil || route.AdapterID != session.adapterID || route.RuntimeID != session.runtimeID ||
		route.EntityID != request.Data.EntityID || route.OperationName != request.Data.OperationName ||
		request.CausationID != nil {
		session.log().WarnContext(traceCtx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("command_id", request.ID),
			slog.String("error_code", "routing_mismatch"),
		)
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, request.Data.Deadline)
	if err != nil {
		session.log().WarnContext(traceCtx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("command_id", request.ID),
			slog.String("error_code", "invalid_deadline"),
		)
		return
	}

	ctx, cancel := context.WithDeadline(traceCtx, deadline)
	defer cancel()
	if contextErr := ctx.Err(); contextErr != nil {
		session.log().WarnContext(ctx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("command_id", request.ID),
			slog.String("error_code", "command_expired"),
		)
		return
	}
	command := request.Data
	command.ID = request.ID
	command.CorrelationID = request.CorrelationID
	session.log().InfoContext(ctx, "command received",
		slog.String("event", "command.received"),
		slog.String("command_id", request.ID),
		slog.String("correlation_id", request.CorrelationID),
		slog.String("entity_id", command.EntityID),
		slog.String("operation", command.OperationName),
	)
	responder := &commandResponder{
		context:       ctx,
		session:       session,
		connection:    session.connection,
		replySubject:  message.Reply,
		validator:     session.validator,
		commandID:     request.ID,
		correlationID: request.CorrelationID,
		entityID:      command.EntityID,
		deadline:      deadline,
	}
	// A successfully published rejection is the complete record: a handler error
	// return after it is expected bookkeeping, not an internal failure. An
	// accepted command whose handler then fails (for example a linked
	// Observation that never publishes) would otherwise vanish after acceptance,
	// so it emits a safe Error diagnostic unless the command context ended.
	handlerErr := handler(ctx, command, responder)
	if !responder.didRespond() {
		session.log().ErrorContext(ctx, "command discarded",
			slog.String("event", "command.discarded"),
			slog.String("command_id", request.ID),
			slog.String("entity_id", command.EntityID),
			slog.String("error_code", "missing_response"),
		)
		return
	}
	if handlerErr == nil || !responder.didAccept() || ctx.Err() != nil ||
		errors.Is(handlerErr, context.Canceled) || errors.Is(handlerErr, context.DeadlineExceeded) ||
		errors.Is(handlerErr, ErrClosed) || errors.Is(handlerErr, ErrRuntimeFenced) {
		return
	}
	session.log().ErrorContext(ctx, "command handler failed",
		slog.String("event", "command.handler_failed"),
		slog.String("command_id", request.ID),
		slog.String("correlation_id", request.CorrelationID),
		slog.String("entity_id", command.EntityID),
		slog.String("error_code", "handler_failed"),
	)
}

type commandResponder struct {
	context       context.Context
	session       *Session
	connection    *natsgo.Conn
	replySubject  string
	validator     *contractsv1.Validator
	commandID     string
	correlationID string
	entityID      string
	deadline      time.Time
	mutex         sync.Mutex
	responded     bool
	accepted      bool
}

func (responder *commandResponder) Accept() (CommandEvidence, error) {
	if err := responder.respond(CommandResponse{CommandID: responder.commandID, Status: statusAccepted}); err != nil {
		return nil, err
	}
	responder.mutex.Lock()
	responder.accepted = true
	responder.mutex.Unlock()
	responder.session.log().InfoContext(responder.context, "command accepted",
		slog.String("event", "command.accepted"),
		slog.String("command_id", responder.commandID),
		slog.String("correlation_id", responder.correlationID),
		slog.String("entity_id", responder.entityID),
	)
	return newCommandEvidence(responder.context, responder.session, observationLink{
		commandID:     responder.commandID,
		correlationID: responder.correlationID,
		entityID:      responder.entityID,
		deadline:      responder.deadline,
	}), nil
}

func (responder *commandResponder) Reject(message string) error {
	return responder.reject("upstream_rejected", message)
}

func (responder *commandResponder) RejectUnavailable(message string) error {
	return responder.reject("entity_unavailable", message)
}

func (responder *commandResponder) reject(code, message string) error {
	if err := responder.respond(CommandResponse{
		CommandID: responder.commandID,
		Status:    statusRejected,
		Error:     &CommandError{Code: code, Message: message},
	}); err != nil {
		return err
	}
	responder.session.log().WarnContext(responder.context, "command rejected",
		slog.String("event", "command.rejected"),
		slog.String("command_id", responder.commandID),
		slog.String("correlation_id", responder.correlationID),
		slog.String("entity_id", responder.entityID),
		slog.String("rejection_code", code),
	)
	return nil
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
	if publishErr := responder.connection.PublishMsg(message); publishErr != nil {
		return fmt.Errorf("publish command response: %w", publishErr)
	}
	responder.responded = true
	return nil
}

func (responder *commandResponder) didRespond() bool {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	return responder.responded
}

func (responder *commandResponder) didAccept() bool {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	return responder.responded && responder.accepted
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
