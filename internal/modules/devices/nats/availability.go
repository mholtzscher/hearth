package nats

import (
	"context"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type AvailabilityReporter interface {
	ReportEntityAvailability(
		context.Context,
		string,
		string,
		devices.RuntimeID,
		[]devices.EntityAvailabilityReport,
	) (time.Time, error)
}

type EntityAvailabilityServer struct {
	requests *requestReplyServer
}

func StartEntityAvailabilityServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	reporter AvailabilityReporter,
	logger *slog.Logger,
) (*EntityAvailabilityServer, error) {
	if reporter == nil {
		return nil, errors.New("entity availability reporter is required")
	}
	logger = defaultLogger(logger)
	requests, err := startRequestReplyServer(
		connection, validator,
		natswire.EntityAvailabilityWildcard(), "Entity availability", "availability_id",
		contractsv1.EntityAvailabilityRequestSchemaID, contractsv1.EntityAvailabilityResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[entityAvailabilityRequest],
		) (entityAvailabilityResponse, bool) {
			return handleEntityAvailability(ctx, subject, request, reporter, logger)
		},
	)
	if err != nil {
		return nil, err
	}
	return &EntityAvailabilityServer{requests: requests}, nil
}

func (server *EntityAvailabilityServer) Drain() error {
	if server == nil {
		return nil
	}
	return server.requests.Drain()
}

func handleEntityAvailability(
	ctx context.Context,
	subject string,
	request natswire.Envelope[entityAvailabilityRequest],
	reporter AvailabilityReporter,
	logger *slog.Logger,
) (entityAvailabilityResponse, bool) {
	route, err := natswire.ParseEntityAvailabilitySubject(subject)
	if err != nil {
		logger.ErrorContext(ctx, "discarding Entity availability with invalid subject",
			"subject", subject, "availability_id", request.ID, "error", err)
		return entityAvailabilityResponse{}, false
	}
	runtimeID, err := devices.ParseRuntimeID(route.RuntimeID)
	if err != nil {
		logger.ErrorContext(ctx, "discarding Entity availability with invalid runtime ID",
			"subject", subject, "availability_id", request.ID, "error", err)
		return entityAvailabilityResponse{}, false
	}
	reports := make([]devices.EntityAvailabilityReport, len(request.Data.Entities))
	for index, entity := range request.Data.Entities {
		entityID, parseErr := devices.ParseEntityID(entity.EntityID)
		if parseErr != nil {
			logger.ErrorContext(ctx, "discarding Entity availability with invalid Entity ID",
				"subject", subject, "availability_id", request.ID, "error", parseErr)
			return entityAvailabilityResponse{}, false
		}
		sourceObservedAt, parseErr := time.Parse(time.RFC3339Nano, entity.SourceObservedAt)
		if parseErr != nil {
			logger.ErrorContext(ctx, "discarding Entity availability with invalid source time",
				"subject", subject, "availability_id", request.ID, "entity_id", entity.EntityID,
				"error", parseErr)
			return entityAvailabilityResponse{}, false
		}
		reports[index] = devices.EntityAvailabilityReport{
			EntityID: entityID, Status: devices.EntityAvailabilityStatus(entity.Status),
			SourceObservedAt: sourceObservedAt, Reason: domainHealthReason(entity.Reason),
		}
	}
	reportedAt, reportErr := reporter.ReportEntityAvailability(
		ctx, request.ID, route.AdapterID, runtimeID, reports,
	)
	response, handled := mapAvailabilityResult(reportedAt, len(reports), reportErr)
	if !handled {
		logger.ErrorContext(ctx, "report Entity availability",
			"subject", subject, "availability_id", request.ID, "error", reportErr)
	}
	return response, handled
}

func mapAvailabilityResult(
	reportedAt time.Time,
	count int,
	err error,
) (entityAvailabilityResponse, bool) {
	if err == nil {
		return entityAvailabilityResponse{
			Status: statusAccepted, ReportedAt: reportedAt.UTC().Format(time.RFC3339Nano), Count: count,
		}, true
	}
	if errors.Is(err, devices.ErrRuntimeFenced) {
		return rejectedAvailability(runtimeFencedCode, runtimeFencedMessage, ""), true
	}
	if errors.Is(err, devices.ErrAdapterUnhealthy) {
		return rejectedAvailability("adapter_unhealthy", "Adapter is not healthy", ""), true
	}
	if errors.Is(err, devices.ErrInvalidAvailabilityRequest) {
		return rejectedAvailability("invalid_request", "Entity availability request is invalid", ""), true
	}
	if entityErr, ok := errors.AsType[*devices.EntityAvailabilityReportError](err); ok {
		switch {
		case errors.Is(entityErr, devices.ErrEntityNotFound):
			return rejectedAvailability(
				"unknown_entity", "Entity is unknown", string(entityErr.EntityID),
			), true
		case errors.Is(entityErr, devices.ErrEntityWrongAdapter):
			return rejectedAvailability(
				"wrong_adapter", "Entity belongs to another Adapter", string(entityErr.EntityID),
			), true
		}
	}
	return entityAvailabilityResponse{}, false
}

func rejectedAvailability(code, message, entityID string) entityAvailabilityResponse {
	return entityAvailabilityResponse{
		Status: statusRejected,
		Error:  &entityAvailabilityError{Code: code, Message: message, EntityID: entityID},
	}
}
