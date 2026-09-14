package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Structured field keys shared by this transport's log sites. Event values stay
// whole literals at each site, so searching an event name finds its
// implementation.
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

// DeviceFactReceiver is the only automation capability the consumer needs:
// synchronous admission of one already-mapped Device Fact. It cannot execute a
// Command, so no Command can ever run in the consumer callback.
type DeviceFactReceiver interface {
	ReceiveDeviceFact(context.Context, automations.DeviceFact) (automations.AdmissionOutcome, error)
}

// AdmissionGate is the optional receiver capability the consumer uses to close
// automation admission when its consume loop terminates unexpectedly. The
// automations Service implements it as StopAdmission, so a consumer fault closes
// automation admission and fails readiness until process restart; a receiver
// that does not implement it leaves admission under its own control.
type AdmissionGate interface {
	StopAdmission()
}

// DeviceFactConsumer is the automations-owned durable Device Fact consumer. It
// reads both fact families through one subscription, admits one Fact at a time,
// and reports activity so shutdown and readiness drain before dependencies. An
// unexpected termination latches automation admission closed through the
// receiver's optional [AdmissionGate]; an intentional [DeviceFactConsumer.Drain]
// never does.
type DeviceFactConsumer struct {
	consume jetstream.ConsumeContext
	active  atomic.Bool
	// draining marks an intentional Drain before it stops the subscription, so
	// the termination watcher can tell a requested shutdown from a fault.
	draining atomic.Bool
	// terminated is closed after the termination watcher has applied its fault
	// decision, so Drain and lifecycle tests can join it without racing it.
	terminated chan struct{}
	gate       AdmissionGate
}

// StartDeviceFactConsumer subscribes the durable consumer and starts reporting
// its activity. Nil dependencies fail before any subscription, and a
// subscription that cannot activate leaves no consumer running.
//
// Each message restores its trace context, maps one strict Device Fact, calls
// ReceiveDeviceFact synchronously under a two-second admission context, and only
// then acknowledges. It never executes a Command.
func StartDeviceFactConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	receiver DeviceFactReceiver,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*DeviceFactConsumer, error) {
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

	running := &DeviceFactConsumer{terminated: make(chan struct{})}
	if gate, ok := receiver.(AdmissionGate); ok {
		running.gate = gate
	}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handleDeviceFactMessage(baseContext, message, validator, receiver, logger)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, _ error) {
			logger.ErrorContext(baseContext, "automation device fact consume error",
				slog.String(transportEventKey, "automation.fact_processing_failed"),
				slog.String("stage", transportStageConsume),
				slog.String(transportErrorCodeKey, transportCodeConsumerError),
			)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start device fact consumer: %w", err)
	}
	running.consume = consume
	running.active.Store(true)
	go running.watchTermination(baseContext, logger)
	return running, nil
}

// watchTermination closes automation admission when the consume loop ends
// without an intentional Drain. Drain marks draining before it stops the
// subscription, so a requested shutdown never latches a fault while any other
// closure does. The consume error handler records a real broker or connection
// fault's cause under its own code; this latch records the outcome under the
// fixed consumer_fault code using the already documented
// automation.fact_processing_failed event.
func (consumer *DeviceFactConsumer) watchTermination(ctx context.Context, logger *slog.Logger) {
	defer close(consumer.terminated)
	<-consumer.consume.Closed()
	consumer.active.Store(false)
	if consumer.draining.Load() {
		return
	}
	if consumer.gate != nil {
		consumer.gate.StopAdmission()
	}
	logger.ErrorContext(ctx, "automation device fact consumer terminated unexpectedly",
		slog.String(transportEventKey, "automation.fact_processing_failed"),
		slog.String("stage", transportStageConsume),
		slog.String(transportErrorCodeKey, transportCodeConsumerFault),
	)
}

// Active reports whether the subscription is still consuming. Drain clears it
// before teardown begins, so readiness fails as soon as shutdown starts.
func (consumer *DeviceFactConsumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

// Drain stops new deliveries and waits for in-flight callbacks to finish, so no
// new automatic admission enters and no callback outlives the drain. In-flight
// admission is already bounded by DeviceFactAdmissionTimeout, so the join is
// finite. It also joins the termination watcher, so once Drain returns its fault
// decision is final and no late latch can close admission.
func (consumer *DeviceFactConsumer) Drain() error {
	if consumer == nil || consumer.consume == nil {
		return nil
	}
	consumer.draining.Store(true)
	consumer.active.Store(false)
	consumer.consume.Drain()
	<-consumer.consume.Closed()
	if consumer.terminated != nil {
		<-consumer.terminated
	}
	return nil
}

// Closed reports subscription termination. A consumer that never subscribed is
// already closed.
func (consumer *DeviceFactConsumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.consume.Closed()
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

// resolveDeviceFactAdmissionFailure disposes one already-mapped Fact whose
// admission failed. A deterministic fact-invalid rejection is terminated because
// redelivery would fail identically forever. Every other failure, including a
// closed admission gate and a bounded-timeout expiry, is negatively
// acknowledged with the fixed delay so the Fact stays redeliverable.
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

// deviceFactAttributes returns the safe structured identity of one mapped Fact:
// its stable identity, family, and variant. It never includes the Observation
// value or any Command parameter.
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
