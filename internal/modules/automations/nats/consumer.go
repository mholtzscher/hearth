package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// Shared log field keys; event values remain searchable literals at each log site.
const (
	transportEventKey     = "event"
	transportErrorCodeKey = "error_code"
)

// Device Fact diagnostic stages and fixed codes. A diagnostic names the stage
// that failed and one fixed code; it never carries a payload, a full subject, or
// upstream error text.
const (
	transportStageConsume   = "consume"
	transportStageMetadata  = "metadata"
	transportStageAdmission = "admission"
	transportStageAck       = "ack"
	transportStageNak       = "nak"
	transportStageTerm      = "term"
)

const (
	transportCodeConsumerError       = "consumer_error"
	transportCodeConsumerFault       = "consumer_fault"
	transportCodeMetadataUnavailable = "metadata_unavailable"
	transportCodeAdmissionFailed     = "admission_failed"
	transportCodeAckFailed           = "ack_failed"
	transportCodeNakFailed           = "nak_failed"
	transportCodeTermFailed          = "term_failed"
)

// DeviceFactReceiver admits a mapped Fact synchronously. Implementations must
// honor the context deadline and leave Command execution to detached workers.
type DeviceFactReceiver interface {
	ReceiveDeviceFact(context.Context, automations.DeviceFact) (automations.AdmissionOutcome, error)
}

// AdmissionGate optionally lets the consumer close its receiver's admission
// after unexpected termination. Receivers without it manage their own gate.
type AdmissionGate interface {
	StopAdmission()
}

// StartDeviceFactConsumer subscribes with strict Fact decoding and trace propagation.
// It acknowledges only after synchronous admission succeeds, using
// DeviceFactAdmissionTimeout for each admission's context deadline.
// Unexpected termination closes the optional AdmissionGate; intentional shutdown does not.
func StartDeviceFactConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	receiver DeviceFactReceiver,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*platformnats.Consumer, error) {
	switch {
	case consumer == nil:
		return nil, errors.New("device fact consumer is required")
	case receiver == nil:
		return nil, errors.New("device fact receiver is required")
	case validator == nil:
		return nil, errors.New("device fact validator is required")
	}
	if baseContext == nil {
		baseContext = context.Background()
	}
	logger = defaultLogger(logger)

	managed, err := platformnats.StartConsumer(
		consumer,
		func(message jetstream.Msg) {
			handleDeviceFactMessage(baseContext, message, validator, receiver, logger)
		},
		platformnats.ConsumerOptions{
			OnConsumeError: func(_ error) {
				logger.ErrorContext(baseContext, "automation device fact consume error",
					slog.String(transportEventKey, "automation.fact_processing_failed"),
					slog.String("stage", transportStageConsume),
					slog.String(transportErrorCodeKey, transportCodeConsumerError),
				)
			},
			// Close admission before diagnostics can block fault reporting.
			OnUnexpectedTermination: func() {
				if gate, ok := receiver.(AdmissionGate); ok {
					gate.StopAdmission()
				}
				logger.ErrorContext(baseContext, "automation device fact consumer terminated unexpectedly",
					slog.String(transportEventKey, "automation.fact_processing_failed"),
					slog.String("stage", transportStageConsume),
					slog.String(transportErrorCodeKey, transportCodeConsumerFault),
				)
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("start device fact consumer: %w", err)
	}
	return managed, nil
}

// handleDeviceFactMessage maps and admits exactly one received Device Fact. It
// acknowledges only after successful admission, terminates deterministic
// malformed wire input, and negatively acknowledges transient admission or
// storage failures with a bounded delay.
func handleDeviceFactMessage(
	baseContext context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	receiver DeviceFactReceiver,
	logger *slog.Logger,
) {
	// Extract the trace context before mapping so every diagnostic and the
	// admission call itself continue the originating trace.
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(ctx, "read automation device fact metadata",
			slog.String(transportEventKey, "automation.fact_processing_failed"),
			slog.String("stage", transportStageMetadata),
			slog.String(transportErrorCodeKey, transportCodeMetadataUnavailable),
		)
		return
	}
	wire := deviceFactWireMessage{
		subject:   message.Subject(),
		messageID: message.Headers().Get(natsgo.MsgIdHdr),
		payload:   message.Data(),
	}
	fact, mapErr := mapDeviceFactMessage(validator, wire)
	if mapErr != nil {
		terminateDeviceFactMessage(
			ctx, message, logger, metadata.Sequence.Stream, rejectionCode(mapErr), "malformed",
		)
		return
	}
	admissionContext, cancelAdmission := context.WithTimeout(ctx, DeviceFactAdmissionTimeout)
	defer cancelAdmission()
	_, admissionErr := receiver.ReceiveDeviceFact(admissionContext, fact)
	if admissionErr != nil {
		resolveDeviceFactAdmissionFailure(
			ctx, message, logger, metadata.Sequence.Stream, fact, admissionErr,
		)
		return
	}
	// Admission committed; acknowledge immediately so a slow diagnostic sink
	// cannot delay it. The service owns the admitted-outcome diagnostics.
	if ackErr := message.Ack(); ackErr != nil {
		logDeviceFactFailure(
			ctx, logger, metadata.Sequence.Stream, fact, transportStageAck, transportCodeAckFailed,
		)
	}
}

// resolveDeviceFactAdmissionFailure terminates invalid Facts. Other failures,
// including closed admission and timeouts, receive a delayed Nak for redelivery.
func resolveDeviceFactAdmissionFailure(
	ctx context.Context,
	message jetstream.Msg,
	logger *slog.Logger,
	streamSequence uint64,
	fact automations.DeviceFact,
	admissionErr error,
) {
	if isDeterministicDeviceFactFailure(admissionErr) {
		terminateDeviceFactMessage(
			ctx, message, logger, streamSequence, rejectionCode(admissionErr), "inadmissible",
		)
		return
	}
	nakErr := message.NakWithDelay(DeviceFactConsumerNakDelay)
	logDeviceFactFailure(
		ctx, logger, streamSequence, fact, transportStageAdmission, transportCodeAdmissionFailed,
	)
	if nakErr != nil {
		logger.ErrorContext(ctx, "negatively acknowledge automation device fact",
			slog.Uint64("stream_sequence", streamSequence),
			slog.String(transportEventKey, "automation.fact_processing_failed"),
			slog.String("stage", transportStageNak),
			slog.String(transportErrorCodeKey, transportCodeNakFailed),
		)
	}
}

// isDeterministicDeviceFactFailure reports whether one admission error is
// permanent for this Fact's bytes rather than a transient storage or gate
// failure.
func isDeterministicDeviceFactFailure(err error) bool {
	return errors.Is(err, automations.ErrInvalidDeviceFact)
}

// terminateDeviceFactMessage terminates one deterministic malformed or
// inadmissible message and records the fixed rejection code at Warn. Raw
// payloads, subjects, and decode errors are never logged.
func terminateDeviceFactMessage(
	ctx context.Context,
	message jetstream.Msg,
	logger *slog.Logger,
	streamSequence uint64,
	code string,
	stage string,
) {
	termErr := message.Term()
	logger.WarnContext(ctx, "terminating rejected automation device fact",
		slog.Uint64("stream_sequence", streamSequence),
		slog.Int("payload_size", len(message.Data())),
		slog.String("stage", stage),
		slog.String(transportEventKey, "automation.fact_invalid"),
		slog.String(transportErrorCodeKey, code),
	)
	if termErr != nil {
		logger.ErrorContext(ctx, "terminate rejected automation device fact",
			slog.Uint64("stream_sequence", streamSequence),
			slog.String(transportEventKey, "automation.fact_invalid"),
			slog.String("stage", transportStageTerm),
			slog.String(transportErrorCodeKey, transportCodeTermFailed),
		)
	}
}

// logDeviceFactFailure records one transient admission, ack, or nak failure with
// the Fact's safe identity fields. It never logs a value, a parameter, or a
// subject.
func logDeviceFactFailure(
	ctx context.Context,
	logger *slog.Logger,
	streamSequence uint64,
	fact automations.DeviceFact,
	stage string,
	code string,
) {
	attributes := []slog.Attr{
		slog.Uint64("stream_sequence", streamSequence),
		slog.String(transportEventKey, "automation.fact_processing_failed"),
		slog.String("stage", stage),
		slog.String(transportErrorCodeKey, code),
	}
	attributes = append(attributes, deviceFactAttributes(fact)...)
	logger.LogAttrs(ctx, slog.LevelError, "process automation device fact", attributes...)
}

// deviceFactAttributes returns Fact identity, family, and variant without payload values.
func deviceFactAttributes(fact automations.DeviceFact) []slog.Attr {
	switch fact.Family {
	case automations.DeviceFactObservation:
		if fact.Observation == nil {
			return nil
		}
		return []slog.Attr{
			slog.String("fact_id", string(fact.Observation.FactID)),
			slog.String("family", string(automations.DeviceFactObservation)),
			slog.String("variant", string(fact.Observation.Disposition)),
		}
	case automations.DeviceFactEntityEvent:
		if fact.EntityEvent == nil {
			return nil
		}
		return []slog.Attr{
			slog.String("fact_id", string(fact.EntityEvent.FactID)),
			slog.String("family", string(automations.DeviceFactEntityEvent)),
			slog.String("variant", string(fact.EntityEvent.Name)),
		}
	default:
		return nil
	}
}

// defaultLogger guarantees no message can lose its diagnostics to a nil logger.
func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default().With(slog.String("component", "automations.nats"))
	}
	return logger
}
