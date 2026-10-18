package nats

import (
	"context"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type RuntimeClaimer interface {
	ClaimAdapterRuntime(context.Context, adapters.ClaimAdapterRuntimeParams) error
}

type HealthRecorder interface {
	RecordAdapterHeartbeat(context.Context, adapters.AdapterHeartbeat) (adapters.HeartbeatResult, error)
	ReleaseAdapterRuntime(context.Context, string, adapters.RuntimeID) error
}

type SessionServer struct {
	claim     *platformnats.RequestReplyServer
	heartbeat *platformnats.RequestReplyServer
	release   *platformnats.RequestReplyServer
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
	if logger == nil {
		logger = slog.Default().With(slog.String("component", "nats"))
	}
	claim, err := startClaimServer(connection, validator, claimer, logger)
	if err != nil {
		return nil, err
	}
	heartbeat, err := startHeartbeatServer(connection, validator, recorder, logger)
	if err != nil {
		_ = claim.Close(context.Background())
		return nil, err
	}
	release, err := startReleaseServer(connection, validator, recorder, logger)
	if err != nil {
		_ = heartbeat.Close(context.Background())
		_ = claim.Close(context.Background())
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

// Close stops and joins all three endpoint callbacks before dependency teardown.
func (server *SessionServer) Close(ctx context.Context) error {
	if server == nil {
		return nil
	}
	return errors.Join(server.release.Close(ctx), server.heartbeat.Close(ctx), server.claim.Close(ctx))
}

func startClaimServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	claimer RuntimeClaimer,
	logger *slog.Logger,
) (*platformnats.RequestReplyServer, error) {
	return platformnats.StartRequestReplyServer(
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
				logger.WarnContext(ctx, "discarding Adapter claim with mismatched routing",
					slog.String("claim_id", request.ID),
					slog.String(transportEventKey, "session.claim_discarded"),
					slog.String(transportErrorCodeKey, "routing_mismatch"),
				)
				return adapterClaimResponse{}, false
			}
			runtimeID, err := adapters.ParseRuntimeID(request.Data.RuntimeID)
			if err != nil {
				logger.WarnContext(ctx, "discarding Adapter claim with invalid runtime ID",
					slog.String("claim_id", request.ID),
					slog.String(transportEventKey, "session.claim_discarded"),
					slog.String(transportErrorCodeKey, "runtime_id_invalid"),
				)
				return adapterClaimResponse{}, false
			}
			ctx = adapters.WithHealthPublicationMetadata(ctx, request.CorrelationID, &request.ID)
			claimErr := claimer.ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
				AdapterID: route.AdapterID, RuntimeID: runtimeID,
				SoftwareName: request.Data.SoftwareName, SoftwareVersion: request.Data.SoftwareVersion,
			})
			response, handled := mapClaimResult(claimErr)
			if !handled {
				logger.ErrorContext(ctx, "claim Adapter runtime",
					slog.String("claim_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
					slog.String(transportEventKey, "session.claim_failed"),
					slog.String(transportErrorCodeKey, "claim_failed"),
				)
			}
			return response, handled
		},
	)
}

func mapClaimResult(err error) (adapterClaimResponse, bool) {
	if err == nil {
		return adapterClaimResponse{Status: statusAccepted}, true
	}
	if active, ok := errors.AsType[*adapters.AdapterActiveError](err); ok {
		retryAfter := active.RetryAfter.UTC().Format(time.RFC3339Nano)
		return adapterClaimResponse{
			Status: statusRejected,
			Error: &adapterClaimError{
				Code: "adapter_active", Message: "Adapter already has an active runtime", RetryAfter: &retryAfter,
			},
		}, true
	}
	if errors.Is(err, adapters.ErrRuntimeClaimConflict) {
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
) (*platformnats.RequestReplyServer, error) {
	return platformnats.StartRequestReplyServer(
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
				logger.WarnContext(ctx, "discarding Adapter heartbeat with invalid subject",
					slog.String("heartbeat_id", request.ID),
					slog.String(transportEventKey, "session.heartbeat_discarded"),
					slog.String(transportErrorCodeKey, "subject_invalid"),
				)
				return adapterHeartbeatResponse{}, false
			}
			runtimeID, err := adapters.ParseRuntimeID(route.RuntimeID)
			if err != nil {
				logger.WarnContext(ctx, "discarding Adapter heartbeat with invalid runtime ID",
					slog.String("heartbeat_id", request.ID),
					slog.String(transportEventKey, "session.heartbeat_discarded"),
					slog.String(transportErrorCodeKey, "runtime_id_invalid"),
				)
				return adapterHeartbeatResponse{}, false
			}
			sourceObservedAt, err := time.Parse(time.RFC3339Nano, request.Data.ExternalSystem.SourceObservedAt)
			if err != nil {
				logger.WarnContext(ctx, "discarding Adapter heartbeat with invalid source time",
					slog.String("heartbeat_id", request.ID),
					slog.String(transportEventKey, "session.heartbeat_discarded"),
					slog.String(transportErrorCodeKey, "source_time_invalid"),
				)
				return adapterHeartbeatResponse{}, false
			}
			ctx = adapters.WithHealthPublicationMetadata(ctx, request.CorrelationID, &request.ID)
			result, recordErr := recorder.RecordAdapterHeartbeat(ctx, adapters.AdapterHeartbeat{
				AdapterID: route.AdapterID, RuntimeID: runtimeID,
				ExternalStatus:   adapters.AdapterHealthStatus(request.Data.ExternalSystem.Status),
				SourceObservedAt: sourceObservedAt,
				Reason:           domainHealthReason(request.Data.ExternalSystem.Reason),
			})
			response, handled := mapHeartbeatResult(result, recordErr)
			if !handled {
				logger.ErrorContext(ctx, "record Adapter heartbeat",
					slog.String("heartbeat_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
					slog.String("runtime_id", route.RuntimeID),
					slog.String(transportEventKey, "session.heartbeat_failed"),
					slog.String(transportErrorCodeKey, "heartbeat_failed"),
				)
			}
			return response, handled
		},
	)
}

func mapHeartbeatResult(result adapters.HeartbeatResult, err error) (adapterHeartbeatResponse, bool) {
	if err == nil {
		return adapterHeartbeatResponse{
			Status: statusAccepted, LeaseExpiresAt: result.LeaseExpiresAt.UTC().Format(time.RFC3339Nano),
		}, true
	}
	if errors.Is(err, adapters.ErrRuntimeFenced) {
		return adapterHeartbeatResponse{
			Status: statusRejected,
			Error:  &adapterError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, true
	}
	if errors.Is(err, adapters.ErrInvalidHealthTransition) {
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
) (*platformnats.RequestReplyServer, error) {
	return platformnats.StartRequestReplyServer(
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
				logger.WarnContext(ctx, "discarding Adapter release with invalid subject",
					slog.String("release_id", request.ID),
					slog.String(transportEventKey, "session.release_discarded"),
					slog.String(transportErrorCodeKey, "subject_invalid"),
				)
				return adapterReleaseResponse{}, false
			}
			runtimeID, err := adapters.ParseRuntimeID(route.RuntimeID)
			if err != nil {
				logger.WarnContext(ctx, "discarding Adapter release with invalid runtime ID",
					slog.String("release_id", request.ID),
					slog.String(transportEventKey, "session.release_discarded"),
					slog.String(transportErrorCodeKey, "runtime_id_invalid"),
				)
				return adapterReleaseResponse{}, false
			}
			ctx = adapters.WithHealthPublicationMetadata(ctx, request.CorrelationID, &request.ID)
			releaseErr := recorder.ReleaseAdapterRuntime(ctx, route.AdapterID, runtimeID)
			response, handled := mapReleaseResult(releaseErr)
			if !handled {
				logger.ErrorContext(ctx, "release Adapter runtime",
					slog.String("release_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
					slog.String("runtime_id", route.RuntimeID),
					slog.String(transportEventKey, "session.release_failed"),
					slog.String(transportErrorCodeKey, "release_failed"),
				)
			}
			return response, handled
		},
	)
}

func mapReleaseResult(err error) (adapterReleaseResponse, bool) {
	if err == nil {
		return adapterReleaseResponse{Status: statusAccepted}, true
	}
	if errors.Is(err, adapters.ErrRuntimeFenced) {
		return adapterReleaseResponse{
			Status: statusRejected,
			Error:  &adapterError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, true
	}
	return adapterReleaseResponse{}, false
}

func domainHealthReason(reason *healthReason) *adapters.HealthReason {
	if reason == nil {
		return nil
	}
	return &adapters.HealthReason{Code: reason.Code}
}
