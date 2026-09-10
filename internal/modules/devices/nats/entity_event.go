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

// EntityEventRecorder is the only devices capability the Entity Event consumer
// needs: recording a report and learning its disposition. It opens no State,
// Command, enablement, health, or availability path.
type EntityEventRecorder interface {
	RecordEntityEvent(
		context.Context,
		string,
		devices.RuntimeID,
		devices.EntityEvent,
		time.Time,
	) (devices.EntityEventRecordResult, error)
}

type entityEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

type EntityEventConsumer struct {
	consume jetstream.ConsumeContext
	active  atomic.Bool
}

//nolint:dupl // Entity Event and Observation consumers are parallel durables over distinct resources.
func StartEntityEventConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	validator *contractsv1.Validator,
	recorder EntityEventRecorder,
	logger *slog.Logger,
) (*EntityEventConsumer, error) {
	if validator == nil {
		return nil, errors.New("entity event validator is required")
	}
	if recorder == nil {
		return nil, errors.New("entity event recorder is required")
	}
	if logger == nil {
		logger = defaultLogger(logger)
	}
	if baseContext == nil {
		baseContext = context.Background()
	}

	running := &EntityEventConsumer{}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handleEntityEventMessage(baseContext, message, validator, recorder, logger)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, _ error) {
			logger.ErrorContext(baseContext, "entity event consume error",
				slog.String(transportEventKey, "entity_event.processing_failed"),
				slog.String("stage", "consume"),
				slog.String(transportErrorCodeKey, "consumer_error"),
			)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start entity event consumer: %w", err)
	}
	running.consume = consume
	running.active.Store(true)
	go func() {
		<-consume.Closed()
		running.active.Store(false)
	}()
	return running, nil
}

func (consumer *EntityEventConsumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

func (consumer *EntityEventConsumer) Stop() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Stop()
}

func (consumer *EntityEventConsumer) Drain() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Drain()
}

func (consumer *EntityEventConsumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.consume.Closed()
}

// handleEntityEventMessage decodes, validates, records, and acknowledges one
// Entity Event. Wire-invalid input is acknowledged with a safe permanent
// class and creates no row; a recorded report is acknowledged only after the
// SQLite transaction commits; and infrastructure or commit failures stay
// unacknowledged so JetStream redelivers them.
func handleEntityEventMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	recorder EntityEventRecorder,
	logger *slog.Logger,
) {
	// Extract the operation context from headers before decoding so every
	// emission below, including permanent invalid input, preserves it.
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(ctx, "cannot read entity event metadata",
			slog.String(transportEventKey, "entity_event.processing_failed"),
			slog.String("stage", "metadata"),
			slog.String(transportErrorCodeKey, "metadata_unavailable"),
		)
		return
	}
	sequence := metadata.Sequence.Stream
	payloadSize := len(message.Data())
	permanentFailure := func(errorCode, entityEventID string) {
		logInvalidEntityEvent(ctx, logger, message, sequence, payloadSize, errorCode, entityEventID)
	}

	envelope, decodeErr := natswire.Decode[entityEvent](
		validator, contractsv1.EntityEventSchemaID, message.Data(),
	)
	if decodeErr != nil {
		permanentFailure("entity_event_decode_failed", "")
		return
	}
	route, routeErr := natswire.ParseEntityEventSubject(message.Subject())
	if routeErr != nil {
		permanentFailure("entity_event_route_failed", envelope.ID)
		return
	}
	if route.EntityID != envelope.Data.EntityID {
		permanentFailure("entity_event_entity_mismatch", envelope.ID)
		return
	}
	if message.Headers().Get(natsgo.MsgIdHdr) != envelope.ID {
		permanentFailure("entity_event_msg_id_mismatch", envelope.ID)
		return
	}
	// An Entity Event is a report, not a reaction: it never carries causation.
	// The strict envelope has no causation_id field, so a caused payload fails
	// decoding above; this guard keeps the permanent class explicit.
	if envelope.CausationID != nil {
		permanentFailure("entity_event_causation_mismatch", envelope.ID)
		return
	}

	emittedAt, parseErr := time.Parse(time.RFC3339Nano, envelope.EmittedAt)
	if parseErr != nil {
		permanentFailure("entity_event_emitted_at_parse_failed", envelope.ID)
		return
	}
	domain, domainErr := domainEntityEvent(envelope, emittedAt)
	if domainErr != nil {
		permanentFailure("entity_event_identity_failed", envelope.ID)
		return
	}
	result, recordErr := recorder.RecordEntityEvent(
		ctx, route.AdapterID, devices.RuntimeID(route.RuntimeID), domain, metadata.Timestamp.UTC(),
	)
	if recordErr != nil {
		logger.ErrorContext(ctx, "record entity event",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("entity_event_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "entity_event.processing_failed"),
			slog.String("stage", "record"),
			slog.String(transportErrorCodeKey, "record_failed"),
		)
		return
	}
	// A slow diagnostic sink must not delay acknowledgement of committed input.
	ackErr := message.Ack()
	if emittedAt.After(metadata.Timestamp.Add(observationFutureClockThreshold)) {
		// Backlog is consumed: skew is diagnostic only and never a rejection
		// reason, an ordering rule, or a recording gate.
		logger.WarnContext(ctx, "adapter entity event clock is ahead of core receive time",
			slog.String("entity_event_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "entity_event.clock_skew"),
			slog.Time("emitted_at", emittedAt),
			slog.Time("received_at", metadata.Timestamp),
		)
	}
	logEntityEventRecorded(ctx, logger, envelope, route, result)
	if ackErr != nil {
		logger.ErrorContext(ctx, "acknowledge recorded entity event",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("entity_event_id", envelope.ID),
			slog.String(transportEventKey, "entity_event.processing_failed"),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		)
	}
}

// logInvalidEntityEvent acknowledges permanent wire-invalid input before
// recording it at Warn with a fixed validation class and safe sizes and IDs.
// Raw payloads and decode errors are never logged.
//
//nolint:dupl // Entity Event and Observation invalid-input diagnostics share one ack-then-warn shape.
func logInvalidEntityEvent(
	ctx context.Context,
	logger *slog.Logger,
	message jetstream.Msg,
	sequence uint64,
	payloadSize int,
	errorCode, entityEventID string,
) {
	ackErr := message.Ack()
	scoped := logger.With(slog.Uint64("stream_sequence", sequence))
	attributes := []slog.Attr{
		slog.Int("payload_size", payloadSize),
		slog.String(transportEventKey, "entity_event.invalid"),
		slog.String(transportErrorCodeKey, errorCode),
	}
	if entityEventID != "" {
		attributes = append(attributes, slog.String("entity_event_id", entityEventID))
	}
	scoped.LogAttrs(ctx, slog.LevelWarn, "acknowledging invalid entity event", attributes...)
	if ackErr != nil {
		ackAttributes := []slog.Attr{
			slog.String(transportEventKey, "entity_event.processing_failed"),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		}
		if entityEventID != "" {
			ackAttributes = append(ackAttributes, slog.String("entity_event_id", entityEventID))
		}
		scoped.LogAttrs(ctx, slog.LevelError, "acknowledge invalid entity event", ackAttributes...)
	}
}

// logEntityEventRecorded records the committed disposition. A later identity
// conflict for the same event ID is the wire-visible diagnostic that the
// rejected report never entered history. The handler attempts Ack before
// calling it, so it is retained regardless of the Ack outcome.
func logEntityEventRecorded(
	ctx context.Context,
	logger *slog.Logger,
	envelope natswire.Envelope[entityEvent],
	route natswire.EntityEventRoute,
	result devices.EntityEventRecordResult,
) {
	attributes := []slog.Attr{
		slog.String("entity_event_id", envelope.ID),
		slog.String("adapter_id", route.AdapterID),
		slog.String("entity_id", route.EntityID),
		slog.String("name", envelope.Data.Name),
		slog.String("outcome", string(result.Outcome)),
	}
	if result.Rejection != nil {
		attributes = append(attributes, slog.String("rejection_code", string(*result.Rejection)))
	}
	if result.Outcome == devices.EntityEventOutcomeIdentityConflict {
		logger.LogAttrs(ctx, slog.LevelWarn, "entity event identity conflict",
			append(attributes, slog.String(transportEventKey, "entity_event.identity_conflict"))...,
		)
		return
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "entity event recorded",
		append(attributes, slog.String(transportEventKey, "entity_event.recorded"))...,
	)
}

// domainEntityEvent maps one decoded envelope to trusted domain input. Every
// identity is parsed as canonical here, so the repository never receives an
// unvalidated ID.
func domainEntityEvent(
	envelope natswire.Envelope[entityEvent],
	emittedAt time.Time,
) (devices.EntityEvent, error) {
	eventID, eventIDErr := devices.ParseEntityEventID(envelope.ID)
	if eventIDErr != nil {
		return devices.EntityEvent{}, eventIDErr
	}
	entityID, entityIDErr := devices.ParseEntityID(envelope.Data.EntityID)
	if entityIDErr != nil {
		return devices.EntityEvent{}, entityIDErr
	}
	correlationID, correlationIDErr := devices.ParseCorrelationID(envelope.CorrelationID)
	if correlationIDErr != nil {
		return devices.EntityEvent{}, correlationIDErr
	}
	return devices.EntityEvent{
		ID:            eventID,
		EntityID:      entityID,
		Name:          devices.EntityEventName(envelope.Data.Name),
		CorrelationID: correlationID,
		EmittedAt:     emittedAt,
	}, nil
}
