package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const requestReplyActivationTimeout = 5 * time.Second

// RequestReplyServer owns one plain Core NATS endpoint and its admitted callbacks.
type RequestReplyServer struct {
	subscription *natsgo.Subscription
	callbacks    *lifecycle.AdmissionGroup
}

// Drain begins asynchronous subscription draining, preserving queued delivery.
// It does not join callbacks; callers must keep dependencies alive until joined.
func (server *RequestReplyServer) Drain() error {
	if server == nil || server.subscription == nil {
		return nil
	}
	if err := server.subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain subscription: %w", err)
	}
	return nil
}

// Close stops admission, unsubscribes, and joins admitted callbacks. The context
// bounds only the caller's wait, not callback execution. Concurrent and repeated
// calls are safe, including after the connection closes unexpectedly.
func (server *RequestReplyServer) Close(ctx context.Context) error {
	if server == nil {
		return nil
	}
	server.callbacks.CloseAdmission()
	err := server.subscription.Unsubscribe()
	if errors.Is(err, natsgo.ErrConnectionClosed) || errors.Is(err, natsgo.ErrBadSubscription) {
		err = nil
	}
	return errors.Join(err, server.callbacks.Wait(ctx))
}

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default().With(slog.String("component", "nats"))
	}
	return logger
}

// Structured field keys shared by transport log emission sites. Event values
// stay whole literals at each site so searching an event name finds its
// implementation.
const (
	transportEventKey     = "event"
	transportErrorCodeKey = "error_code"
)

// StartRequestReplyServer validates dependencies, subscribes to the wildcard
// subject, and answers each message through handleRequest. The respond
// callback owns route validation, the domain call, result mapping, and its own
// discard logging; returning ok=false means no reply is published.
func StartRequestReplyServer[Req, Resp any](
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	wildcard, kind, idField string,
	requestSchema, responseSchema string,
	logger *slog.Logger,
	respond func(context.Context, string, natswire.Envelope[Req]) (Resp, bool),
) (*RequestReplyServer, error) {
	logger = defaultLogger(logger)
	if connection == nil {
		return nil, fmt.Errorf("%s NATS connection is required", kind)
	}
	if validator == nil {
		return nil, fmt.Errorf("%s validator is required", kind)
	}
	if respond == nil {
		return nil, fmt.Errorf("%s handler is required", kind)
	}
	callbacks := lifecycle.NewAdmissionGroup()
	subscription, subscribeErr := connection.Subscribe(wildcard, func(message *natsgo.Msg) {
		reservation, admitted := callbacks.TryAcquire()
		if !admitted {
			return
		}
		defer reservation.Release()
		handleRequest(connection, message, validator, kind, idField, requestSchema, responseSchema, logger, respond)
	})
	if subscribeErr != nil {
		return nil, fmt.Errorf("subscribe to %s requests: %w", kind, subscribeErr)
	}
	server := &RequestReplyServer{subscription: subscription, callbacks: callbacks}
	activationContext, cancel := context.WithTimeout(context.Background(), requestReplyActivationTimeout)
	defer cancel()
	if err := connection.FlushWithContext(activationContext); err != nil {
		// No server is returned on failure: join any callback that entered before
		// activation failed so its dependencies can safely be torn down.
		_ = server.Close(context.Background())
		return nil, fmt.Errorf("activate %s subscription: %w", kind, err)
	}
	return server, nil
}

// handleRequest performs the shared reply choreography for one request/reply
// exchange: reply-subject check, schema-validated decode, causation check, and
// trace propagation frame the respond callback and the reply publish.
func handleRequest[Req, Resp any](
	connection *natsgo.Conn,
	message *natsgo.Msg,
	validator *contractsv1.Validator,
	kind, idField string,
	requestSchema, responseSchema string,
	logger *slog.Logger,
	respond func(context.Context, string, natswire.Envelope[Req]) (Resp, bool),
) {
	// Extract the operation context from headers before decoding so every
	// discard below preserves it even for undecodable input.
	ctx := natswire.ExtractTrace(context.Background(), message.Header)
	if message.Reply == "" {
		logger.WarnContext(ctx, "discarding request without reply subject",
			slog.String("kind", kind),
			slog.String(transportEventKey, "transport.request_discarded"),
			slog.String(transportErrorCodeKey, "missing_reply_subject"),
		)
		return
	}
	request, err := natswire.Decode[Req](validator, requestSchema, message.Data)
	if err != nil {
		logger.WarnContext(ctx, fmt.Sprintf("discarding invalid %s", kind),
			slog.String("kind", kind),
			slog.String(transportEventKey, "transport.request_discarded"),
			slog.String(transportErrorCodeKey, "request_decode_failed"),
		)
		return
	}
	if request.CausationID != nil {
		logger.WarnContext(ctx, fmt.Sprintf("discarding caused %s", kind),
			slog.String("kind", kind),
			slog.String(idField, request.ID),
			slog.String(transportEventKey, "transport.request_discarded"),
			slog.String(transportErrorCodeKey, "causation_present"),
		)
		return
	}
	response, handled := respond(ctx, message.Subject, request)
	if !handled {
		return
	}
	replyID, err := newReplyID()
	if err != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("generate %s reply ID", kind),
			slog.String("kind", kind),
			slog.String(idField, request.ID),
			slog.String(transportEventKey, "transport.response_failed"),
			slog.String("stage", "reply_id"),
			slog.String(transportErrorCodeKey, "reply_id_failed"),
		)
		return
	}
	causationID := request.ID
	reply := natswire.Envelope[Resp]{
		ID: replyID, Schema: responseSchema,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: request.CorrelationID,
		CausationID: &causationID, Data: response,
	}
	payload, err := natswire.Encode(validator, responseSchema, reply)
	if err != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("encode %s response", kind),
			slog.String("kind", kind),
			slog.String(idField, request.ID),
			slog.String(transportEventKey, "transport.response_failed"),
			slog.String("stage", "encode"),
			slog.String(transportErrorCodeKey, "response_encode_failed"),
		)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, replyMessage.Header)
	if publishErr := connection.PublishMsg(replyMessage); publishErr != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("publish %s response", kind),
			slog.String("kind", kind),
			slog.String(idField, request.ID),
			slog.String(transportEventKey, "transport.response_failed"),
			slog.String("stage", "publish"),
			slog.String(transportErrorCodeKey, "response_publish_failed"),
		)
	}
}

func newReplyID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return "rep_" + id.String(), nil
}
