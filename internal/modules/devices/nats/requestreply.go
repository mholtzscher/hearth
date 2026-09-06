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
)

// requestReplyServer holds the subscription for one request/reply endpoint.
type requestReplyServer struct {
	subscription *natsgo.Subscription
}

// Drain idempotently drains the endpoint subscription.
func (server *requestReplyServer) Drain() error {
	if server == nil || server.subscription == nil {
		return nil
	}
	if err := server.subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain subscription: %w", err)
	}
	return nil
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

// startRequestReplyServer validates dependencies, subscribes to the wildcard
// subject, and answers each message through handleRequest. The respond
// callback owns route validation, the domain call, result mapping, and its own
// discard logging; returning ok=false means no reply is published.
func startRequestReplyServer[Req, Resp any](
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	wildcard, kind, idField string,
	requestSchema, responseSchema string,
	logger *slog.Logger,
	respond func(context.Context, string, natswire.Envelope[Req]) (Resp, bool),
) (*requestReplyServer, error) {
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
	logger = defaultLogger(logger)
	subscription, subscribeErr := connection.Subscribe(wildcard, func(message *natsgo.Msg) {
		handleRequest(connection, message, validator, kind, idField, requestSchema, responseSchema, logger, respond)
	})
	if subscribeErr != nil {
		return nil, fmt.Errorf("subscribe to %s requests: %w", kind, subscribeErr)
	}
	if err := connection.Flush(); err != nil {
		_ = subscription.Unsubscribe()
		return nil, fmt.Errorf("activate %s subscription: %w", kind, err)
	}
	return &requestReplyServer{subscription: subscription}, nil
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
		logger.With(slog.String("kind", kind)).WarnContext(ctx, "discarding request without reply subject",
			slog.String(transportEventKey, "transport.request_discarded"),
			slog.String(transportErrorCodeKey, "missing_reply_subject"),
		)
		return
	}
	request, err := natswire.Decode[Req](validator, requestSchema, message.Data)
	if err != nil {
		logger.With(slog.String("kind", kind)).WarnContext(ctx, fmt.Sprintf("discarding invalid %s", kind),
			slog.String(transportEventKey, "transport.request_discarded"),
			slog.String(transportErrorCodeKey, "request_decode_failed"),
		)
		return
	}
	if request.CausationID != nil {
		logger.With(
			slog.String("kind", kind),
			slog.String(idField, request.ID),
		).WarnContext(ctx, fmt.Sprintf("discarding caused %s", kind),
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
		logger.With(
			slog.String("kind", kind),
			slog.String(idField, request.ID),
		).ErrorContext(ctx, fmt.Sprintf("generate %s reply ID", kind),
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
		logger.With(
			slog.String("kind", kind),
			slog.String(idField, request.ID),
		).ErrorContext(ctx, fmt.Sprintf("encode %s response", kind),
			slog.String(transportEventKey, "transport.response_failed"),
			slog.String("stage", "encode"),
			slog.String(transportErrorCodeKey, "response_encode_failed"),
		)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, replyMessage.Header)
	if publishErr := connection.PublishMsg(replyMessage); publishErr != nil {
		logger.With(
			slog.String("kind", kind),
			slog.String(idField, request.ID),
		).ErrorContext(ctx, fmt.Sprintf("publish %s response", kind),
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
