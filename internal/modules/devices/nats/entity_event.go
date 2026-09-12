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

// EntityEventRedeliveryDelay is how long JetStream waits before redelivering a
// report whose recording failed for a transient storage reason. It matches the
// consumer's AckWait, so such a report is retried no more often than once per
// ordinary retry window instead of in a hot loop, and unlimited redelivery
// keeps it repairable for as long as the stream retains it. A deterministic
// descriptor failure never reaches this delay: it is terminated instead.
const EntityEventRedeliveryDelay = EntityEventAckWait

type entityEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

// EntityEventConsumer is the durable Entity Event consumer. It embeds the
// shared durable lifecycle, so activity reporting and shutdown behave exactly
// as they do for the Observation consumer.
type EntityEventConsumer struct {
	*durableConsumer
}

// entityEventClass is the Entity Event wire vocabulary shared consumer code
// uses for diagnostics. An Entity Event is a report, never a reaction, so
// invalid, consume, and processing failures all report under entity_event.*.
func entityEventClass() consumerClass {
	return consumerClass{
		kind:         "entity event",
		invalidEvent: "entity_event.invalid",
		failureEvent: "entity_event.processing_failed",
		idKey:        "entity_event_id",
	}
}

// StartEntityEventConsumer subscribes the Entity Event consumer and starts
// reporting its activity. Nil dependencies fail before any subscription, and a
// subscription that cannot activate leaves no consumer running.
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
	durable, err := startDurableConsumer(
		baseContext,
		consumer,
		entityEventClass(),
		logger,
		func(ctx context.Context, logger *slog.Logger, message jetstream.Msg) {
			handleEntityEventMessage(ctx, message, validator, recorder, logger)
		},
	)
	if err != nil {
		return nil, err
	}
	return &EntityEventConsumer{durableConsumer: durable}, nil
}

// handleEntityEventMessage decodes, validates, records, and acknowledges one
// Entity Event. Wire-invalid input is acknowledged with a safe permanent class
// and creates no row. A recorded report is acknowledged only after the SQLite
// transaction commits. A recording failure is never positively acknowledged
// and never writes a partial row: a failure to interpret the Entity's persisted
// event-source descriptor is deterministic, so the report is terminated and
// never redelivered to this consumer, while every other recording failure is
// negatively acknowledged with EntityEventRedeliveryDelay so it stays
// redeliverable without retrying in a hot loop.
func handleEntityEventMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	recorder EntityEventRecorder,
	logger *slog.Logger,
) {
	opened, ok := openConsumerMessage(baseContext, message, logger, entityEventClass())
	if !ok {
		return
	}
	ctx := opened.operation
	metadata := opened.metadata
	permanentFailure := opened.reject

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
	domain, domainErr := domainEntityEvent(envelope, emittedAt, deviceFactTraceFromHeaders(message.Headers()))
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
		resolveEntityEventRecordFailure(
			ctx, message, logger, envelope.ID, metadata.Sequence.Stream, recordErr,
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

// resolveEntityEventRecordFailure resolves one already logged record failure
// without a positive acknowledgement. A deterministic descriptor failure is
// terminated: redelivery would fail identically forever, and the report keeps
// its raw bytes in the bounded stream as the only remaining evidence while no
// disposition row is invented for it. Every other record or commit failure is
// negatively acknowledged after a bounded delay, which returns it for
// redelivery without retrying it in a hot loop. A failed Term or Nak is
// reported through its own safe structured diagnostic.
func resolveEntityEventRecordFailure(
	ctx context.Context,
	message jetstream.Msg,
	logger *slog.Logger,
	envelopeID string,
	streamSequence uint64,
	recordErr error,
) {
	if errors.Is(recordErr, devices.ErrEntityEventDescriptorCorrupt) {
		if termErr := message.Term(); termErr != nil {
			logger.ErrorContext(ctx, "terminate unrecordable entity event",
				slog.Uint64("stream_sequence", streamSequence),
				slog.String("entity_event_id", envelopeID),
				slog.String(transportEventKey, "entity_event.processing_failed"),
				slog.String("stage", "term"),
				slog.String(transportErrorCodeKey, "term_failed"),
			)
		}
		return
	}
	if nakErr := message.NakWithDelay(EntityEventRedeliveryDelay); nakErr != nil {
		logger.ErrorContext(ctx, "negatively acknowledge failed entity event",
			slog.Uint64("stream_sequence", streamSequence),
			slog.String("entity_event_id", envelopeID),
			slog.String(transportEventKey, "entity_event.processing_failed"),
			slog.String("stage", "nak"),
			slog.String(transportErrorCodeKey, "nak_failed"),
		)
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
	trace devices.DeviceFactTraceContext,
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
		Trace:         trace,
	}, nil
}
