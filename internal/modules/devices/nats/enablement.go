package nats

import (
	"context"
	"errors"
	"log/slog"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	natsgo "github.com/nats-io/nats.go"
)

type EntityEnablementSetter interface {
	SetOwnedEntityEnabled(context.Context, string, devices.EntityID, bool) (bool, error)
}

type EntityEnablementServer struct {
	*requestReplyServer
}

func StartEntityEnablementServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	setter EntityEnablementSetter,
	logger *slog.Logger,
) (*EntityEnablementServer, error) {
	if setter == nil {
		return nil, errors.New("entity enablement setter is required")
	}
	logger = defaultLogger(logger)
	server, err := startRequestReplyServer(
		connection, validator,
		natswire.EntityEnablementWildcard(), "entity enablement", "enablement_id",
		contractsv1.EntityEnablementRequestSchemaID, contractsv1.EntityEnablementResponseSchemaID,
		logger,
		func(ctx context.Context, subject string, request natswire.Envelope[entityEnablementRequest]) (entityEnablementResponse, bool) {
			route, err := natswire.ParseEntityEnablementSubject(subject)
			if err != nil {
				logger.Error("discarding Entity enablement request with invalid subject", "subject", subject, "error", err)
				return entityEnablementResponse{}, false
			}
			if route.EntityID != request.Data.EntityID {
				logger.Error("discarding Entity enablement request with mismatched routing", "subject", subject, "enablement_id", request.ID)
				return entityEnablementResponse{}, false
			}
			entityID, err := devices.ParseEntityID(request.Data.EntityID)
			if err != nil {
				logger.Error("discarding Entity enablement request with invalid Entity ID", "subject", subject, "enablement_id", request.ID)
				return entityEnablementResponse{}, false
			}
			confirmed, err := setter.SetOwnedEntityEnabled(ctx, route.AdapterID, entityID, request.Data.Enabled)
			response, handled := mapEntityEnablementResult(request.Data.EntityID, confirmed, err)
			if !handled {
				logger.Error("set Entity enablement", "subject", subject, "enablement_id", request.ID, "error", err)
			}
			return response, handled
		},
	)
	if err != nil {
		return nil, err
	}
	return &EntityEnablementServer{requestReplyServer: server}, nil
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
