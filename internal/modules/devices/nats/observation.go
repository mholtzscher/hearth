package nats

import (
	"context"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const observationFutureClockThreshold = time.Minute

type ObservationProjector interface {
	ProjectObservation(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Observation,
		time.Time,
	) (devices.ProjectionResult, error)
}

// ObservationConsumer is the durable Observation consumer. It embeds the
// shared durable lifecycle, so activity reporting and shutdown behave exactly
// as they do for the Entity Event consumer.
type ObservationConsumer struct {
	*durableConsumer
}

// observationClass is the Observation wire vocabulary shared consumer code
// uses for diagnostics. An Observation is State evidence, so invalid, consume,
// and processing failures all report under observation.*.
func observationClass() consumerClass {
	return consumerClass{
		kind:         "observation",
		invalidEvent: "observation.invalid",
		failureEvent: "observation.processing_failed",
		idKey:        "observation_id",
	}
}

// StartObservationConsumer subscribes the Observation consumer and starts
// reporting its activity. Nil dependencies fail before any subscription, and a
// subscription that cannot activate leaves no consumer running.
func StartObservationConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	validator *contractsv1.Validator,
	projector ObservationProjector,
	logger *slog.Logger,
) (*ObservationConsumer, error) {
	if validator == nil {
		return nil, errors.New("observation validator is required")
	}
	if projector == nil {
		return nil, errors.New("observation handler is required")
	}
	durable, err := startDurableConsumer(
		baseContext,
		consumer,
		observationClass(),
		logger,
		func(ctx context.Context, logger *slog.Logger, message jetstream.Msg) {
			handleObservationMessage(ctx, message, validator, projector, logger)
		},
	)
	if err != nil {
		return nil, err
	}
	return &ObservationConsumer{durableConsumer: durable}, nil
}

func handleObservationMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	projector ObservationProjector,
	logger *slog.Logger,
) {
	opened, ok := openConsumerMessage(baseContext, message, logger, observationClass())
	if !ok {
		return
	}
	ctx := opened.operation
	metadata := opened.metadata
	permanentFailure := opened.reject

	envelope, decodeErr := natswire.Decode[observation](validator, contractsv1.ObservationSchemaID, message.Data())
	if decodeErr != nil {
		permanentFailure("observation_decode_failed", "")
		return
	}
	route, routeErr := natswire.ParseObservationSubject(message.Subject())
	if routeErr != nil {
		permanentFailure("observation_route_failed", envelope.ID)
		return
	}
	if route.EntityID != envelope.Data.EntityID {
		permanentFailure("observation_entity_mismatch", envelope.ID)
		return
	}
	if message.Headers().Get(natsgo.MsgIdHdr) != envelope.ID {
		permanentFailure("observation_msg_id_mismatch", envelope.ID)
		return
	}
	if envelope.CausationID != nil &&
		(envelope.Data.RefreshForCommand == nil || *envelope.CausationID != *envelope.Data.RefreshForCommand) {
		permanentFailure("observation_causation_mismatch", envelope.ID)
		return
	}

	adapterReceivedAt, parseErr := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if parseErr != nil {
		permanentFailure("adapter_received_at_parse_failed", envelope.ID)
		return
	}
	var sourceUpdatedAt *time.Time
	if envelope.Data.SourceUpdatedAt != nil {
		parsed, sourceTimeErr := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if sourceTimeErr != nil {
			permanentFailure("source_updated_at_parse_failed", envelope.ID)
			return
		}
		sourceUpdatedAt = &parsed
	}
	domain, domainErr := domainObservation(envelope, adapterReceivedAt, sourceUpdatedAt)
	if domainErr != nil {
		permanentFailure("observation_identity_failed", envelope.ID)
		return
	}
	result, projectionErr := projector.ProjectObservation(
		ctx, route.AdapterID, devices.RuntimeID(route.RuntimeID), domain, metadata.Timestamp.UTC(),
	)
	if projectionErr != nil {
		logger.ErrorContext(ctx, "project observation",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("observation_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "observation.processing_failed"),
			slog.String("stage", "commit"),
			slog.String(transportErrorCodeKey, "projection_failed"),
		)
		return
	}
	// A slow diagnostic sink must not delay acknowledgement of committed input.
	ackErr := message.Ack()
	if adapterReceivedAt.After(metadata.Timestamp.Add(observationFutureClockThreshold)) {
		logger.WarnContext(ctx, "adapter observation clock is ahead of core receive time",
			slog.String("observation_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "observation.clock_skew"),
			slog.Time("adapter_received_at", adapterReceivedAt),
			slog.Time("observed_at", metadata.Timestamp),
		)
	}
	logObservationProjected(ctx, logger, envelope, route, domain, result)
	if ackErr != nil {
		logger.ErrorContext(ctx, "acknowledge projected observation",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("observation_id", envelope.ID),
			slog.String(transportEventKey, "observation.processing_failed"),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		)
	}
}

// logObservationProjected records the committed projection disposition at
// Debug with safe identity fields only; State values are never logged. The
// handler attempts Ack before calling it, so it is retained regardless of
// the Ack outcome.
func logObservationProjected(
	ctx context.Context,
	logger *slog.Logger,
	envelope natswire.Envelope[observation],
	route natswire.ObservationRoute,
	domain devices.Observation,
	result devices.ProjectionResult,
) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, "observation.projected"),
		slog.String("observation_id", envelope.ID),
		slog.String("adapter_id", route.AdapterID),
		slog.String("entity_id", route.EntityID),
		slog.String("disposition", string(result.Disposition)),
	}
	if envelope.CorrelationID != "" {
		attributes = append(attributes, slog.String("correlation_id", envelope.CorrelationID))
	}
	if domain.RefreshForCommand != nil {
		attributes = append(attributes, slog.String("command_id", string(*domain.RefreshForCommand)))
	}
	if result.Rejection != nil {
		attributes = append(attributes, slog.String("rejection_code", string(*result.Rejection)))
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "observation projected", attributes...)
}

func domainObservation(
	envelope natswire.Envelope[observation],
	adapterReceivedAt time.Time,
	sourceUpdatedAt *time.Time,
) (devices.Observation, error) {
	observationID, observationIDErr := devices.ParseObservationID(envelope.ID)
	if observationIDErr != nil {
		return devices.Observation{}, observationIDErr
	}
	entityID, entityIDErr := devices.ParseEntityID(envelope.Data.EntityID)
	if entityIDErr != nil {
		return devices.Observation{}, entityIDErr
	}
	domain := devices.Observation{
		ID:                observationID,
		EntityID:          entityID,
		Value:             append(devices.Value(nil), envelope.Data.Value...),
		AdapterReceivedAt: adapterReceivedAt,
	}
	if sourceUpdatedAt != nil {
		cloned := *sourceUpdatedAt
		domain.SourceUpdatedAt = &cloned
	}
	if envelope.Data.RefreshForCommand != nil {
		commandID, commandIDErr := devices.ParseCommandID(*envelope.Data.RefreshForCommand)
		if commandIDErr != nil {
			return devices.Observation{}, commandIDErr
		}
		domain.RefreshForCommand = &commandID
	}
	return domain, nil
}
