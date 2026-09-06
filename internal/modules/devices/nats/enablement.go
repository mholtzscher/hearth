package nats

import (
	"context"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type EntityEnablementSetter interface {
	SetOwnedEntityEnabled(context.Context, string, devices.RuntimeID, devices.EntityID, bool) (bool, error)
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
	server, startErr := startRequestReplyServer(
		connection, validator,
		natswire.EntityEnablementWildcard(), "entity enablement", "enablement_id",
		contractsv1.EntityEnablementRequestSchemaID, contractsv1.EntityEnablementResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[entityEnablementRequest],
		) (entityEnablementResponse, bool) {
			route, routeErr := natswire.ParseEntityEnablementSubject(subject)
			if routeErr != nil {
				logger.With(slog.String("enablement_id", request.ID)).WarnContext(ctx,
					"discarding Entity enablement request with invalid subject",
					slog.String(transportEventKey, "enablement.request_discarded"),
					slog.String(transportErrorCodeKey, "subject_invalid"),
				)
				return entityEnablementResponse{}, false
			}
			if route.EntityID != request.Data.EntityID {
				logger.With(slog.String("enablement_id", request.ID)).WarnContext(ctx,
					"discarding Entity enablement request with mismatched routing",
					slog.String(transportEventKey, "enablement.request_discarded"),
					slog.String(transportErrorCodeKey, "routing_mismatch"),
				)
				return entityEnablementResponse{}, false
			}
			entityID, entityIDErr := devices.ParseEntityID(request.Data.EntityID)
			if entityIDErr != nil {
				logger.With(slog.String("enablement_id", request.ID)).WarnContext(ctx,
					"discarding Entity enablement request with invalid Entity ID",
					slog.String(transportEventKey, "enablement.request_discarded"),
					slog.String(transportErrorCodeKey, "entity_id_invalid"),
				)
				return entityEnablementResponse{}, false
			}
			confirmed, enablementErr := setter.SetOwnedEntityEnabled(
				ctx, route.AdapterID, devices.RuntimeID(route.RuntimeID), entityID, request.Data.Enabled,
			)
			response, handled := mapEntityEnablementResult(request.Data.EntityID, confirmed, enablementErr)
			if !handled {
				logger.With(
					slog.String("enablement_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
				).ErrorContext(ctx,
					"set Entity enablement",
					slog.String(transportEventKey, "enablement.failed"),
					slog.String(transportErrorCodeKey, "enablement_failed"),
				)
			}
			return response, handled
		},
	)
	if startErr != nil {
		return nil, startErr
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
			Status: statusAccepted, EntityID: entityID, Enabled: &confirmed,
		}, true
	case errors.Is(err, devices.ErrEntityNotFound):
		return entityEnablementResponse{
			Status: statusRejected,
			Error:  &entityEnablementError{Code: "unknown_entity", Message: "entity not found"},
		}, true
	case errors.Is(err, devices.ErrEntityWrongAdapter):
		return entityEnablementResponse{
			Status: statusRejected,
			Error:  &entityEnablementError{Code: "wrong_adapter", Message: "entity is owned by another adapter"},
		}, true
	case errors.Is(err, devices.ErrRuntimeFenced):
		return entityEnablementResponse{
			Status: statusRejected,
			Error:  &entityEnablementError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, true
	default:
		return entityEnablementResponse{}, false
	}
}
