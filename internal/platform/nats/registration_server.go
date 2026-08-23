package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
)

type RegistrationHandler func(context.Context, string, Registration) (RegistrationResponse, error)

type RegistrationServer struct {
	subscription *natsgo.Subscription
}

func StartRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	handler RegistrationHandler,
	logger *slog.Logger,
) (*RegistrationServer, error) {
	if connection == nil {
		return nil, errors.New("registration NATS connection is required")
	}
	if validator == nil {
		return nil, errors.New("registration validator is required")
	}
	if handler == nil {
		return nil, errors.New("registration handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	subscription, err := connection.Subscribe(RegistrationWildcard(), func(message *natsgo.Msg) {
		handleRegistrationMessage(connection, message, validator, handler, logger)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to registrations: %w", err)
	}
	if err := connection.Flush(); err != nil {
		_ = subscription.Unsubscribe()
		return nil, fmt.Errorf("activate registration subscription: %w", err)
	}
	return &RegistrationServer{subscription: subscription}, nil
}

func (server *RegistrationServer) Drain() error {
	if server == nil || server.subscription == nil {
		return nil
	}
	if err := server.subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain registration subscription: %w", err)
	}
	return nil
}

func handleRegistrationMessage(
	connection *natsgo.Conn,
	message *natsgo.Msg,
	validator *contractsv1.Validator,
	handler RegistrationHandler,
	logger *slog.Logger,
) {
	if message.Reply == "" {
		logger.Error("discarding registration without reply subject", "subject", message.Subject)
		return
	}
	route, err := ParseRegistrationSubject(message.Subject)
	if err != nil {
		logger.Error("discarding registration with invalid subject", "subject", message.Subject, "error", err)
		return
	}
	request, err := Decode[Registration](validator, contractsv1.RegistrationRequestSchemaID, message.Data)
	if err != nil {
		logger.Error("discarding invalid registration", "subject", message.Subject, "error", err)
		return
	}
	if request.CausationID != nil {
		logger.Error("discarding caused registration", "subject", message.Subject, "registration_id", request.ID)
		return
	}
	ctx := propagation.TraceContext{}.Extract(context.Background(), HeaderCarrier(message.Header))
	response, err := handler(ctx, route.AdapterID, request.Data)
	if err != nil {
		logger.Error("handle registration", "subject", message.Subject, "registration_id", request.ID, "error", err)
		return
	}
	replyID, err := newReplyID()
	if err != nil {
		logger.Error("generate registration reply ID", "registration_id", request.ID, "error", err)
		return
	}
	causationID := request.ID
	reply := Envelope[RegistrationResponse]{
		ID: replyID, Schema: contractsv1.RegistrationResponseSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: request.CorrelationID,
		CausationID: &causationID, Data: response,
	}
	payload, err := Encode(validator, contractsv1.RegistrationResponseSchemaID, reply)
	if err != nil {
		logger.Error("encode registration response", "registration_id", request.ID, "error", err)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	propagation.TraceContext{}.Inject(ctx, HeaderCarrier(replyMessage.Header))
	if err := connection.PublishMsg(replyMessage); err != nil {
		logger.Error("publish registration response", "registration_id", request.ID, "error", err)
	}
}

func newReplyID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return "rep_" + id.String(), nil
}
