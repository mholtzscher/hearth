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
	ProjectObservation(context.Context, string, devices.Observation, time.Time) (devices.ProjectionResult, error)
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
		logger = slog.Default()
	}
	if baseContext == nil {
		baseContext = context.Background()
	}

	running := &ObservationConsumer{}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handleObservationMessage(baseContext, message, validator, projector, logger)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			logger.ErrorContext(baseContext, "observation consumer error", "error", err)
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
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(baseContext, "cannot read observation metadata", "subject", message.Subject(), "error", metadataErr)
		return
	}
	permanentFailure := func(err error, observationID string) {
		attributes := []any{
			"subject", message.Subject(),
			"stream_sequence", metadata.Sequence.Stream,
			"error", err,
		}
		if observationID != "" {
			attributes = append(attributes, "observation_id", observationID)
		}
		logger.ErrorContext(baseContext, "acknowledging invalid observation", attributes...)
		if ackErr := message.Ack(); ackErr != nil {
			logger.ErrorContext(
				baseContext,
				"acknowledge invalid observation",
				"subject",
				message.Subject(),
				"stream_sequence",
				metadata.Sequence.Stream,
				"error",
				ackErr,
			)
		}
	}

	envelope, decodeErr := natswire.Decode[observation](validator, contractsv1.ObservationSchemaID, message.Data())
	if decodeErr != nil {
		permanentFailure(decodeErr, "")
		return
	}
	route, routeErr := natswire.ParseObservationSubject(message.Subject())
	if routeErr != nil {
		permanentFailure(routeErr, envelope.ID)
		return
	}
	if route.EntityID != envelope.Data.EntityID {
		permanentFailure(errors.New("observation subject entity does not match payload"), envelope.ID)
		return
	}
	if message.Headers().Get(natsgo.MsgIdHdr) != envelope.ID {
		permanentFailure(errors.New("Nats-Msg-Id does not match observation ID"), envelope.ID)
		return
	}
	if envelope.CausationID != nil &&
		(envelope.Data.RefreshForCommand == nil || *envelope.CausationID != *envelope.Data.RefreshForCommand) {
		permanentFailure(errors.New("observation causation ID does not match refresh command ID"), envelope.ID)
		return
	}

	adapterReceivedAt, parseErr := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if parseErr != nil {
		permanentFailure(fmt.Errorf("parse adapter_received_at: %w", parseErr), envelope.ID)
		return
	}
	var sourceUpdatedAt *time.Time
	if envelope.Data.SourceUpdatedAt != nil {
		parsed, sourceTimeErr := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if sourceTimeErr != nil {
			permanentFailure(fmt.Errorf("parse source_updated_at: %w", sourceTimeErr), envelope.ID)
			return
		}
		sourceUpdatedAt = &parsed
	}
	if adapterReceivedAt.After(metadata.Timestamp.Add(observationFutureClockThreshold)) {
		logger.WarnContext(baseContext, "adapter observation clock is ahead of core receipt time",
			"observation_id", envelope.ID,
			"adapter_id", route.AdapterID,
			"entity_id", route.EntityID,
			"adapter_received_at", adapterReceivedAt,
			"observed_at", metadata.Timestamp,
		)
	}

	domain, domainErr := domainObservation(envelope, adapterReceivedAt, sourceUpdatedAt)
	if domainErr != nil {
		permanentFailure(domainErr, envelope.ID)
		return
	}
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	if _, projectionErr := projector.ProjectObservation(
		ctx, route.AdapterID, domain, metadata.Timestamp.UTC(),
	); projectionErr != nil {
		logger.ErrorContext(baseContext, "project observation",
			"subject", message.Subject(),
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
			"error", projectionErr,
		)
		return
	}
	if ackErr := message.Ack(); ackErr != nil {
		logger.ErrorContext(baseContext, "acknowledge projected observation",
			"subject", message.Subject(),
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
			"error", ackErr,
		)
	}
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
		copy := *sourceUpdatedAt
		domain.SourceUpdatedAt = &copy
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
