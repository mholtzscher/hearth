package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
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

type ObservationConsumer struct {
	consume jetstream.ConsumeContext
	active  atomic.Bool
}

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
	if logger == nil {
		logger = defaultLogger(logger)
	}
	if baseContext == nil {
		baseContext = context.Background()
	}

	running := &ObservationConsumer{}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handleObservationMessage(baseContext, message, validator, projector, logger)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, _ error) {
			logger.ErrorContext(baseContext, "observation consume error",
				transportEventKey, "observation.processing_failed",
				"stage", "consume",
				transportErrorCodeKey, "consumer_error",
			)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start observation consumer: %w", err)
	}
	running.consume = consume
	running.active.Store(true)
	go func() {
		<-consume.Closed()
		running.active.Store(false)
	}()
	return running, nil
}

func (consumer *ObservationConsumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

func (consumer *ObservationConsumer) Stop() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Stop()
}

func (consumer *ObservationConsumer) Drain() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Drain()
}

func (consumer *ObservationConsumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.consume.Closed()
}

//nolint:funlen // The handler is a linear decode, validate, project, and acknowledge pipeline.
func handleObservationMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	projector ObservationProjector,
	logger *slog.Logger,
) {
	// Extract the operation context from headers before decoding so every
	// emission below, including permanent invalid input, preserves it.
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(ctx, "cannot read observation metadata",
			transportEventKey, "observation.processing_failed",
			"stage", "metadata",
			transportErrorCodeKey, "metadata_unavailable",
		)
		return
	}
	sequence := metadata.Sequence.Stream
	payloadSize := len(message.Data())
	permanentFailure := func(errorCode, observationID string) {
		logInvalidObservation(ctx, logger, message, sequence, payloadSize, errorCode, observationID)
	}

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
	if adapterReceivedAt.After(metadata.Timestamp.Add(observationFutureClockThreshold)) {
		logger.WarnContext(ctx, "adapter observation clock is ahead of core receive time",
			transportEventKey, "observation.clock_skew",
			"observation_id", envelope.ID,
			"adapter_id", route.AdapterID,
			"entity_id", route.EntityID,
			"adapter_received_at", adapterReceivedAt,
			"observed_at", metadata.Timestamp,
		)
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
			transportEventKey, "observation.processing_failed",
			"stage", "commit",
			transportErrorCodeKey, "projection_failed",
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
			"adapter_id", route.AdapterID,
			"entity_id", route.EntityID,
		)
		return
	}
	logObservationProjected(ctx, logger, envelope, route, domain, result)
	if ackErr := message.Ack(); ackErr != nil {
		logger.ErrorContext(ctx, "acknowledge projected observation",
			transportEventKey, "observation.processing_failed",
			"stage", "ack",
			transportErrorCodeKey, "ack_failed",
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
		)
	}
}

// logInvalidObservation records permanent wire-invalid input at Warn with a
// fixed validation class and safe sizes and IDs, then acknowledges the
// message. Raw payloads and decode errors are never logged.
func logInvalidObservation(
	ctx context.Context,
	logger *slog.Logger,
	message jetstream.Msg,
	sequence uint64,
	payloadSize int,
	errorCode, observationID string,
) {
	attributes := []any{
		transportEventKey, "observation.invalid",
		transportErrorCodeKey, errorCode,
		"stream_sequence", sequence,
		"payload_size", payloadSize,
	}
	if observationID != "" {
		attributes = append(attributes, "observation_id", observationID)
	}
	logger.WarnContext(ctx, "acknowledging invalid observation", attributes...)
	if ackErr := message.Ack(); ackErr != nil {
		ackAttributes := []any{
			transportEventKey, "observation.processing_failed",
			"stage", "ack",
			transportErrorCodeKey, "ack_failed",
			"stream_sequence", sequence,
		}
		if observationID != "" {
			ackAttributes = append(ackAttributes, "observation_id", observationID)
		}
		logger.ErrorContext(ctx, "acknowledge invalid observation", ackAttributes...)
	}
}

// logObservationProjected records the committed projection disposition at
// Debug with safe identity fields only; State values are never logged.
func logObservationProjected(
	ctx context.Context,
	logger *slog.Logger,
	envelope natswire.Envelope[observation],
	route natswire.ObservationRoute,
	domain devices.Observation,
	result devices.ProjectionResult,
) {
	attributes := []any{
		transportEventKey, "observation.projected",
		"observation_id", envelope.ID,
		"adapter_id", route.AdapterID,
		"entity_id", route.EntityID,
		"disposition", string(result.Disposition),
	}
	if envelope.CorrelationID != "" {
		attributes = append(attributes, "correlation_id", envelope.CorrelationID)
	}
	if domain.RefreshForCommand != nil {
		attributes = append(attributes, "command_id", string(*domain.RefreshForCommand))
	}
	if result.Rejection != nil {
		attributes = append(attributes, "rejection_code", string(*result.Rejection))
	}
	logger.DebugContext(ctx, "observation projected", attributes...)
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
