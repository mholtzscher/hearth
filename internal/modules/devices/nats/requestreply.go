package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	natsgo "github.com/nats-io/nats.go"
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
		return slog.Default()
	}
	return logger
}

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
	if message.Reply == "" {
		logger.Error(fmt.Sprintf("discarding %s without reply subject", kind), "subject", message.Subject)
		return
	}
	request, err := natswire.Decode[Req](validator, requestSchema, message.Data)
	if err != nil {
		logger.Error(fmt.Sprintf("discarding invalid %s", kind), "subject", message.Subject, "error", err)
		return
	}
	if request.CausationID != nil {
		logger.Error(fmt.Sprintf("discarding caused %s", kind), "subject", message.Subject, idField, request.ID)
		return
	}
	ctx := natswire.ExtractTrace(context.Background(), message.Header)
	response, handled := respond(ctx, message.Subject, request)
	if !handled {
		return
	}
	replyID, err := newReplyID()
	if err != nil {
		logger.Error(fmt.Sprintf("generate %s reply ID", kind), idField, request.ID, "error", err)
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
		logger.Error(fmt.Sprintf("encode %s response", kind), idField, request.ID, "error", err)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, replyMessage.Header)
	if publishErr := connection.PublishMsg(replyMessage); publishErr != nil {
		logger.Error(fmt.Sprintf("publish %s response", kind), idField, request.ID, "error", publishErr)
	}
}

func newReplyID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return "rep_" + id.String(), nil
}
