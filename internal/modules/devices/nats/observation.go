package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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
			logger.Error("observation consumer error", "error", err)
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

func handleObservationMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	projector ObservationProjector,
	logger *slog.Logger,
) {
	metadata, err := message.Metadata()
	if err != nil {
		logger.Error("cannot read observation metadata", "subject", message.Subject(), "error", err)
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
		logger.Error("acknowledging invalid observation", attributes...)
		if ackErr := message.Ack(); ackErr != nil {
			logger.Error("acknowledge invalid observation", "subject", message.Subject(), "stream_sequence", metadata.Sequence.Stream, "error", ackErr)
		}
	}

	envelope, err := natswire.Decode[observation](validator, contractsv1.ObservationSchemaID, message.Data())
	if err != nil {
		permanentFailure(err, "")
		return
	}
	route, err := natswire.ParseObservationSubject(message.Subject())
	if err != nil {
		permanentFailure(err, envelope.ID)
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

	adapterReceivedAt, err := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if err != nil {
		permanentFailure(fmt.Errorf("parse adapter_received_at: %w", err), envelope.ID)
		return
	}
	var sourceUpdatedAt *time.Time
	if envelope.Data.SourceUpdatedAt != nil {
		parsed, err := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if err != nil {
			permanentFailure(fmt.Errorf("parse source_updated_at: %w", err), envelope.ID)
			return
		}
		sourceUpdatedAt = &parsed
	}
	if adapterReceivedAt.After(metadata.Timestamp.Add(observationFutureClockThreshold)) {
		logger.Warn("adapter observation clock is ahead of core receipt time",
			"observation_id", envelope.ID,
			"adapter_id", route.AdapterID,
			"entity_id", route.EntityID,
			"adapter_received_at", adapterReceivedAt,
			"observed_at", metadata.Timestamp,
		)
	}

	domain, err := domainObservation(envelope, adapterReceivedAt, sourceUpdatedAt)
	if err != nil {
		permanentFailure(err, envelope.ID)
		return
	}
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	if _, err := projector.ProjectObservation(ctx, route.AdapterID, domain, metadata.Timestamp.UTC()); err != nil {
		logger.Error("project observation",
			"subject", message.Subject(),
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
			"error", err,
		)
		return
	}
	if err := message.Ack(); err != nil {
		logger.Error("acknowledge projected observation",
			"subject", message.Subject(),
			"stream_sequence", metadata.Sequence.Stream,
			"observation_id", envelope.ID,
			"error", err,
		)
	}
}

func domainObservation(
	envelope natswire.Envelope[observation],
	adapterReceivedAt time.Time,
	sourceUpdatedAt *time.Time,
) (devices.Observation, error) {
	observationID, err := devices.ParseObservationID(envelope.ID)
	if err != nil {
		return devices.Observation{}, err
	}
	entityID, err := devices.ParseEntityID(envelope.Data.EntityID)
	if err != nil {
		return devices.Observation{}, err
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
		commandID, err := devices.ParseCommandID(*envelope.Data.RefreshForCommand)
		if err != nil {
			return devices.Observation{}, err
		}
		domain.RefreshForCommand = &commandID
	}
	return domain, nil
}
