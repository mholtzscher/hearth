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

// DeviceEventRecorder is the only devices capability the Device Event consumer
// needs: recording a report and learning its disposition. It opens no State,
// Command, enablement, health, or availability path.
type DeviceEventRecorder interface {
	RecordDeviceEvent(
		context.Context,
		string,
		devices.RuntimeID,
		devices.DeviceEvent,
		time.Time,
	) (devices.DeviceEventRecordResult, error)
}

type deviceEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

type DeviceEventConsumer struct {
	consume jetstream.ConsumeContext
	active  atomic.Bool
}

//nolint:dupl // Device Event and Observation consumers are parallel durables over distinct resources.
func StartDeviceEventConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	validator *contractsv1.Validator,
	recorder DeviceEventRecorder,
	logger *slog.Logger,
) (*DeviceEventConsumer, error) {
	if validator == nil {
		return nil, errors.New("device event validator is required")
	}
	if recorder == nil {
		return nil, errors.New("device event recorder is required")
	}
	if logger == nil {
		logger = defaultLogger(logger)
	}
	if baseContext == nil {
		baseContext = context.Background()
	}

	running := &DeviceEventConsumer{}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handleDeviceEventMessage(baseContext, message, validator, recorder, logger)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, _ error) {
			logger.ErrorContext(baseContext, "device event consume error",
				slog.String(transportEventKey, "device_event.processing_failed"),
				slog.String("stage", "consume"),
				slog.String(transportErrorCodeKey, "consumer_error"),
			)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start device event consumer: %w", err)
	}
	running.consume = consume
	running.active.Store(true)
	go func() {
		<-consume.Closed()
		running.active.Store(false)
	}()
	return running, nil
}

func (consumer *DeviceEventConsumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

func (consumer *DeviceEventConsumer) Stop() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Stop()
}

func (consumer *DeviceEventConsumer) Drain() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Drain()
}

func (consumer *DeviceEventConsumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.consume.Closed()
}

// handleDeviceEventMessage decodes, validates, records, and acknowledges one
// Device Event. Wire-invalid input is acknowledged with a safe permanent
// class and creates no row; a recorded report is acknowledged only after the
// SQLite transaction commits; and infrastructure or commit failures stay
// unacknowledged so JetStream redelivers them.
func handleDeviceEventMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	recorder DeviceEventRecorder,
	logger *slog.Logger,
) {
	// Extract the operation context from headers before decoding so every
	// emission below, including permanent invalid input, preserves it.
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(ctx, "cannot read device event metadata",
			slog.String(transportEventKey, "device_event.processing_failed"),
			slog.String("stage", "metadata"),
			slog.String(transportErrorCodeKey, "metadata_unavailable"),
		)
		return
	}
	sequence := metadata.Sequence.Stream
	payloadSize := len(message.Data())
	permanentFailure := func(errorCode, deviceEventID string) {
		logInvalidDeviceEvent(ctx, logger, message, sequence, payloadSize, errorCode, deviceEventID)
	}

	envelope, decodeErr := natswire.Decode[deviceEvent](
		validator, contractsv1.DeviceEventSchemaID, message.Data(),
	)
	if decodeErr != nil {
		permanentFailure("device_event_decode_failed", "")
		return
	}
	route, routeErr := natswire.ParseDeviceEventSubject(message.Subject())
	if routeErr != nil {
		permanentFailure("device_event_route_failed", envelope.ID)
		return
	}
	if route.EntityID != envelope.Data.EntityID {
		permanentFailure("device_event_entity_mismatch", envelope.ID)
		return
	}
	if message.Headers().Get(natsgo.MsgIdHdr) != envelope.ID {
		permanentFailure("device_event_msg_id_mismatch", envelope.ID)
		return
	}
	// A Device Event is a report, not a reaction: it never carries causation.
	// The strict envelope has no causation_id field, so a caused payload fails
	// decoding above; this guard keeps the permanent class explicit.
	if envelope.CausationID != nil {
		permanentFailure("device_event_causation_mismatch", envelope.ID)
		return
	}

	emittedAt, parseErr := time.Parse(time.RFC3339Nano, envelope.EmittedAt)
	if parseErr != nil {
		permanentFailure("device_event_emitted_at_parse_failed", envelope.ID)
		return
	}
	domain, domainErr := domainDeviceEvent(envelope, emittedAt)
	if domainErr != nil {
		permanentFailure("device_event_identity_failed", envelope.ID)
		return
	}
	result, recordErr := recorder.RecordDeviceEvent(
		ctx, route.AdapterID, devices.RuntimeID(route.RuntimeID), domain, metadata.Timestamp.UTC(),
	)
	if recordErr != nil {
		logger.ErrorContext(ctx, "record device event",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("device_event_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "device_event.processing_failed"),
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
		logger.WarnContext(ctx, "adapter device event clock is ahead of core receive time",
			slog.String("device_event_id", envelope.ID),
			slog.String("adapter_id", route.AdapterID),
			slog.String("entity_id", route.EntityID),
			slog.String(transportEventKey, "device_event.clock_skew"),
			slog.Time("emitted_at", emittedAt),
			slog.Time("received_at", metadata.Timestamp),
		)
	}
	logDeviceEventRecorded(ctx, logger, envelope, route, result)
	if ackErr != nil {
		logger.ErrorContext(ctx, "acknowledge recorded device event",
			slog.Uint64("stream_sequence", metadata.Sequence.Stream),
			slog.String("device_event_id", envelope.ID),
			slog.String(transportEventKey, "device_event.processing_failed"),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		)
	}
}

// logInvalidDeviceEvent acknowledges permanent wire-invalid input before
// recording it at Warn with a fixed validation class and safe sizes and IDs.
// Raw payloads and decode errors are never logged.
//
//nolint:dupl // Device Event and Observation invalid-input diagnostics share one ack-then-warn shape.
func logInvalidDeviceEvent(
	ctx context.Context,
	logger *slog.Logger,
	message jetstream.Msg,
	sequence uint64,
	payloadSize int,
	errorCode, deviceEventID string,
) {
	ackErr := message.Ack()
	scoped := logger.With(slog.Uint64("stream_sequence", sequence))
	attributes := []slog.Attr{
		slog.Int("payload_size", payloadSize),
		slog.String(transportEventKey, "device_event.invalid"),
		slog.String(transportErrorCodeKey, errorCode),
	}
	if deviceEventID != "" {
		attributes = append(attributes, slog.String("device_event_id", deviceEventID))
	}
	scoped.LogAttrs(ctx, slog.LevelWarn, "acknowledging invalid device event", attributes...)
	if ackErr != nil {
		ackAttributes := []slog.Attr{
			slog.String(transportEventKey, "device_event.processing_failed"),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		}
		if deviceEventID != "" {
			ackAttributes = append(ackAttributes, slog.String("device_event_id", deviceEventID))
		}
		scoped.LogAttrs(ctx, slog.LevelError, "acknowledge invalid device event", ackAttributes...)
	}
}

// logDeviceEventRecorded records the committed disposition. A later identity
// conflict for the same event ID is the wire-visible diagnostic that the
// rejected report never entered history. The handler attempts Ack before
// calling it, so it is retained regardless of the Ack outcome.
func logDeviceEventRecorded(
	ctx context.Context,
	logger *slog.Logger,
	envelope natswire.Envelope[deviceEvent],
	route natswire.DeviceEventRoute,
	result devices.DeviceEventRecordResult,
) {
	attributes := []slog.Attr{
		slog.String("device_event_id", envelope.ID),
		slog.String("adapter_id", route.AdapterID),
		slog.String("entity_id", route.EntityID),
		slog.String("name", envelope.Data.Name),
		slog.String("outcome", string(result.Outcome)),
	}
	if result.Rejection != nil {
		attributes = append(attributes, slog.String("rejection_code", string(*result.Rejection)))
	}
	if result.Outcome == devices.DeviceEventOutcomeIdentityConflict {
		logger.LogAttrs(ctx, slog.LevelWarn, "device event identity conflict",
			append(attributes, slog.String(transportEventKey, "device_event.identity_conflict"))...,
		)
		return
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "device event recorded",
		append(attributes, slog.String(transportEventKey, "device_event.recorded"))...,
	)
}

// domainDeviceEvent maps one decoded envelope to trusted domain input. Every
// identity is parsed as canonical here, so the repository never receives an
// unvalidated ID.
func domainDeviceEvent(
	envelope natswire.Envelope[deviceEvent],
	emittedAt time.Time,
) (devices.DeviceEvent, error) {
	eventID, eventIDErr := devices.ParseDeviceEventID(envelope.ID)
	if eventIDErr != nil {
		return devices.DeviceEvent{}, eventIDErr
	}
	entityID, entityIDErr := devices.ParseEntityID(envelope.Data.EntityID)
	if entityIDErr != nil {
		return devices.DeviceEvent{}, entityIDErr
	}
	correlationID, correlationIDErr := devices.ParseCorrelationID(envelope.CorrelationID)
	if correlationIDErr != nil {
		return devices.DeviceEvent{}, correlationIDErr
	}
	return devices.DeviceEvent{
		ID:            eventID,
		EntityID:      entityID,
		Name:          devices.DeviceEventName(envelope.Data.Name),
		CorrelationID: correlationID,
		EmittedAt:     emittedAt,
	}, nil
}
