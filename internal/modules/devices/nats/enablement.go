package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	natsgo "github.com/nats-io/nats.go"
)

type EntityEnablementSetter interface {
	SetOwnedEntityEnabled(context.Context, string, devices.EntityID, bool) (bool, error)
}

type EntityEnablementServer struct {
	subscription *natsgo.Subscription
}

func StartEntityEnablementServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	setter EntityEnablementSetter,
	logger *slog.Logger,
) (*EntityEnablementServer, error) {
	if connection == nil {
		return nil, errors.New("entity enablement NATS connection is required")
	}
	if validator == nil {
		return nil, errors.New("entity enablement validator is required")
	}
	if setter == nil {
		return nil, errors.New("entity enablement setter is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	subscription, err := connection.Subscribe(natswire.EntityEnablementWildcard(), func(message *natsgo.Msg) {
		handleEntityEnablementMessage(connection, message, validator, setter, logger)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to entity enablement requests: %w", err)
	}
	if err := connection.Flush(); err != nil {
		_ = subscription.Unsubscribe()
		return nil, fmt.Errorf("activate entity enablement subscription: %w", err)
	}
	return &EntityEnablementServer{subscription: subscription}, nil
}

func (server *EntityEnablementServer) Drain() error {
	if server == nil || server.subscription == nil {
		return nil
	}
	if err := server.subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain entity enablement subscription: %w", err)
	}
	return nil
}

func handleEntityEnablementMessage(
	connection *natsgo.Conn,
	message *natsgo.Msg,
	validator *contractsv1.Validator,
	setter EntityEnablementSetter,
	logger *slog.Logger,
) {
	if message.Reply == "" {
		logger.Error("discarding Entity enablement request without reply subject", "subject", message.Subject)
		return
	}
	route, err := natswire.ParseEntityEnablementSubject(message.Subject)
	if err != nil {
		logger.Error("discarding Entity enablement request with invalid subject", "subject", message.Subject, "error", err)
		return
	}
	request, err := natswire.Decode[entityEnablementRequest](
		validator, contractsv1.EntityEnablementRequestSchemaID, message.Data,
	)
	if err != nil {
		logger.Error("discarding invalid Entity enablement request", "subject", message.Subject, "error", err)
		return
	}
	if request.CausationID != nil || route.EntityID != request.Data.EntityID {
		logger.Error("discarding Entity enablement request with mismatched routing", "subject", message.Subject, "enablement_id", request.ID)
		return
	}
	entityID, err := devices.ParseEntityID(request.Data.EntityID)
	if err != nil {
		logger.Error("discarding Entity enablement request with invalid Entity ID", "subject", message.Subject, "enablement_id", request.ID)
		return
	}
	ctx := natswire.ExtractTrace(context.Background(), message.Header)
	confirmed, err := setter.SetOwnedEntityEnabled(ctx, route.AdapterID, entityID, request.Data.Enabled)
	response, handled := mapEntityEnablementResult(request.Data.EntityID, confirmed, err)
	if !handled {
		logger.Error("set Entity enablement", "subject", message.Subject, "enablement_id", request.ID, "error", err)
		return
	}
	replyID, err := newReplyID()
	if err != nil {
		logger.Error("generate Entity enablement reply ID", "enablement_id", request.ID, "error", err)
		return
	}
	causationID := request.ID
	reply := natswire.Envelope[entityEnablementResponse]{
		ID: replyID, Schema: contractsv1.EntityEnablementResponseSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: request.CorrelationID,
		CausationID: &causationID, Data: response,
	}
	payload, err := natswire.Encode(validator, contractsv1.EntityEnablementResponseSchemaID, reply)
	if err != nil {
		logger.Error("encode Entity enablement response", "enablement_id", request.ID, "error", err)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, replyMessage.Header)
	if err := connection.PublishMsg(replyMessage); err != nil {
		logger.Error("publish Entity enablement response", "enablement_id", request.ID, "error", err)
	}
}

func mapEntityEnablementResult(
	entityID string,
	confirmed bool,
	err error,
) (entityEnablementResponse, bool) {
	switch {
	case err == nil:
		return entityEnablementResponse{
			Status: "accepted", EntityID: entityID, Enabled: &confirmed,
		}, true
	case errors.Is(err, devices.ErrEntityNotFound):
		return entityEnablementResponse{
			Status: "rejected",
			Error:  &entityEnablementError{Code: "unknown_entity", Message: "entity not found"},
		}, true
	case errors.Is(err, devices.ErrEntityWrongAdapter):
		return entityEnablementResponse{
			Status: "rejected",
			Error:  &entityEnablementError{Code: "wrong_adapter", Message: "entity is owned by another adapter"},
		}, true
	default:
		return entityEnablementResponse{}, false
	}
}
