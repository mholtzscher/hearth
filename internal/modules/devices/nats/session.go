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

type RuntimeClaimer interface {
	ClaimAdapterRuntime(context.Context, devices.ClaimAdapterRuntimeParams) error
}

type HealthRecorder interface {
	RecordAdapterHeartbeat(context.Context, devices.AdapterHeartbeat) (devices.HeartbeatResult, error)
	ReleaseAdapterRuntime(context.Context, string, devices.RuntimeID) error
}

type SessionServer struct {
	claim     *requestReplyServer
	heartbeat *requestReplyServer
	release   *requestReplyServer
}

func StartSessionServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	claimer RuntimeClaimer,
	recorder HealthRecorder,
	logger *slog.Logger,
) (*SessionServer, error) {
	if claimer == nil {
		return nil, errors.New("adapter runtime claimer is required")
	}
	if recorder == nil {
		return nil, errors.New("adapter health recorder is required")
	}
	logger = defaultLogger(logger)
	claim, err := startClaimServer(connection, validator, claimer, logger)
	if err != nil {
		return nil, err
	}
	heartbeat, err := startHeartbeatServer(connection, validator, recorder, logger)
	if err != nil {
		_ = claim.Drain()
		return nil, err
	}
	release, err := startReleaseServer(connection, validator, recorder, logger)
	if err != nil {
		_ = heartbeat.Drain()
		_ = claim.Drain()
		return nil, err
	}
	return &SessionServer{claim: claim, heartbeat: heartbeat, release: release}, nil
}

func (server *SessionServer) Drain() error {
	if server == nil {
		return nil
	}
	return errors.Join(server.release.Drain(), server.heartbeat.Drain(), server.claim.Drain())
}

func startClaimServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	claimer RuntimeClaimer,
	logger *slog.Logger,
) (*requestReplyServer, error) {
	return startRequestReplyServer(
		connection, validator,
		natswire.AdapterClaimWildcard(), "Adapter claim", "claim_id",
		contractsv1.AdapterClaimRequestSchemaID, contractsv1.AdapterClaimResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[adapterClaimRequest],
		) (adapterClaimResponse, bool) {
			route, err := natswire.ParseAdapterClaimSubject(subject)
			if err != nil || route.AdapterID != request.Data.AdapterID {
				logger.ErrorContext(ctx, "discarding Adapter claim with mismatched routing",
					"subject", subject, "claim_id", request.ID, "error", err)
				return adapterClaimResponse{}, false
			}
			runtimeID, err := devices.ParseRuntimeID(request.Data.RuntimeID)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter claim with invalid runtime ID",
					"subject", subject, "claim_id", request.ID, "error", err)
				return adapterClaimResponse{}, false
			}
			claimErr := claimer.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
				AdapterID: route.AdapterID, RuntimeID: runtimeID,
				SoftwareName: request.Data.SoftwareName, SoftwareVersion: request.Data.SoftwareVersion,
			})
			response, handled := mapClaimResult(claimErr)
			if !handled {
				logger.ErrorContext(ctx, "claim Adapter runtime",
					"subject", subject, "claim_id", request.ID, "error", claimErr)
			}
			return response, handled
		},
	)
}

func mapClaimResult(err error) (adapterClaimResponse, bool) {
	if err == nil {
		return adapterClaimResponse{Status: statusAccepted}, true
	}
	if active, ok := errors.AsType[*devices.AdapterActiveError](err); ok {
		retryAfter := active.RetryAfter.UTC().Format(time.RFC3339Nano)
		return adapterClaimResponse{
			Status: statusRejected,
			Error: &adapterClaimError{
				Code: "adapter_active", Message: "Adapter already has an active runtime", RetryAfter: &retryAfter,
			},
		}, true
	}
	if errors.Is(err, devices.ErrRuntimeClaimConflict) {
		return adapterClaimResponse{
			Status: statusRejected,
			Error:  &adapterClaimError{Code: "claim_conflict", Message: "Runtime claim conflicts with prior state"},
		}, true
	}
	return adapterClaimResponse{}, false
}

func startHeartbeatServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	recorder HealthRecorder,
	logger *slog.Logger,
) (*requestReplyServer, error) {
	return startRequestReplyServer(
		connection, validator,
		natswire.AdapterHeartbeatWildcard(), "Adapter heartbeat", "heartbeat_id",
		contractsv1.AdapterHeartbeatRequestSchemaID, contractsv1.AdapterHeartbeatResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[adapterHeartbeatRequest],
		) (adapterHeartbeatResponse, bool) {
			route, err := natswire.ParseAdapterHeartbeatSubject(subject)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter heartbeat with invalid subject",
					"subject", subject, "heartbeat_id", request.ID, "error", err)
				return adapterHeartbeatResponse{}, false
			}
			runtimeID, err := devices.ParseRuntimeID(route.RuntimeID)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter heartbeat with invalid runtime ID",
					"subject", subject, "heartbeat_id", request.ID, "error", err)
				return adapterHeartbeatResponse{}, false
			}
			sourceObservedAt, err := time.Parse(time.RFC3339Nano, request.Data.ExternalSystem.SourceObservedAt)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter heartbeat with invalid source time",
					"subject", subject, "heartbeat_id", request.ID, "error", err)
				return adapterHeartbeatResponse{}, false
			}
			result, recordErr := recorder.RecordAdapterHeartbeat(ctx, devices.AdapterHeartbeat{
				AdapterID: route.AdapterID, RuntimeID: runtimeID,
				ExternalStatus:   devices.AdapterHealthStatus(request.Data.ExternalSystem.Status),
				SourceObservedAt: sourceObservedAt,
				Reason:           domainHealthReason(request.Data.ExternalSystem.Reason),
			})
			response, handled := mapHeartbeatResult(result, recordErr)
			if !handled {
				logger.ErrorContext(ctx, "record Adapter heartbeat",
					"subject", subject, "heartbeat_id", request.ID, "error", recordErr)
			}
			return response, handled
		},
	)
}

func mapHeartbeatResult(result devices.HeartbeatResult, err error) (adapterHeartbeatResponse, bool) {
	if err == nil {
		return adapterHeartbeatResponse{
			Status: statusAccepted, LeaseExpiresAt: result.LeaseExpiresAt.UTC().Format(time.RFC3339Nano),
		}, true
	}
	if errors.Is(err, devices.ErrRuntimeFenced) {
		return adapterHeartbeatResponse{
			Status: statusRejected,
			Error:  &adapterError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, true
	}
	if errors.Is(err, devices.ErrInvalidHealthTransition) {
		return adapterHeartbeatResponse{
			Status: statusRejected,
			Error:  &adapterError{Code: "invalid_transition", Message: "Adapter health transition is invalid"},
		}, true
	}
	return adapterHeartbeatResponse{}, false
}

func startReleaseServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	recorder HealthRecorder,
	logger *slog.Logger,
) (*requestReplyServer, error) {
	return startRequestReplyServer(
		connection, validator,
		natswire.AdapterReleaseWildcard(), "Adapter release", "release_id",
		contractsv1.AdapterReleaseRequestSchemaID, contractsv1.AdapterReleaseResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[adapterReleaseRequest],
		) (adapterReleaseResponse, bool) {
			route, err := natswire.ParseAdapterReleaseSubject(subject)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter release with invalid subject",
					"subject", subject, "release_id", request.ID, "error", err)
				return adapterReleaseResponse{}, false
			}
			runtimeID, err := devices.ParseRuntimeID(route.RuntimeID)
			if err != nil {
				logger.ErrorContext(ctx, "discarding Adapter release with invalid runtime ID",
					"subject", subject, "release_id", request.ID, "error", err)
				return adapterReleaseResponse{}, false
			}
			releaseErr := recorder.ReleaseAdapterRuntime(ctx, route.AdapterID, runtimeID)
			response, handled := mapReleaseResult(releaseErr)
			if !handled {
				logger.ErrorContext(ctx, "release Adapter runtime",
					"subject", subject, "release_id", request.ID, "error", releaseErr)
			}
			return response, handled
		},
	)
}

func mapReleaseResult(err error) (adapterReleaseResponse, bool) {
	if err == nil {
		return adapterReleaseResponse{Status: statusAccepted}, true
	}
	if errors.Is(err, devices.ErrRuntimeFenced) {
		return adapterReleaseResponse{
			Status: statusRejected,
			Error:  &adapterError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, true
	}
	return adapterReleaseResponse{}, false
}

func domainHealthReason(reason *healthReason) *devices.HealthReason {
	if reason == nil {
		return nil
	}
	return &devices.HealthReason{Code: reason.Code}
}
