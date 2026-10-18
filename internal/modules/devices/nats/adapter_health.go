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
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type AdapterHealthApplier interface {
	ApplyAdapterHealth(context.Context, devices.AdapterHealthUpdate) (devices.HealthProjectionResult, error)
}

type adapterHealthSnapshot struct {
	AdapterID        string  `json:"adapter_id"`
	RuntimeID        *string `json:"runtime_id"`
	Revision         int64   `json:"revision"`
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	ReasonCode       *string `json:"reason_code,omitempty"`
	SourceObservedAt *string `json:"source_observed_at,omitempty"`
	ChangedAt        string  `json:"changed_at"`
	EvidenceAt       string  `json:"evidence_at"`
}

// StartAdapterHealthConsumer uses the shared tracked consumer. Assembly owns
// the callback context: keep it alive until Drain joins callbacks and hooks.
func StartAdapterHealthConsumer(
	ctx context.Context,
	consumer jetstream.Consumer,
	validator *contractsv1.Validator,
	applier AdapterHealthApplier,
	logger *slog.Logger,
) (*platformnats.Consumer, error) {
	if validator == nil || applier == nil {
		return nil, errors.New("adapter health consumer dependencies required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return platformnats.StartConsumer(consumer, func(message jetstream.Msg) {
		handleAdapterHealth(ctx, message, validator, applier, logger)
	}, platformnats.ConsumerOptions{OnConsumeError: func(_ error) {
		logger.ErrorContext(
			ctx,
			"health consumer failure",
			"event",
			"adapter_health.consumer_failed",
			"error_code",
			"consume_failed",
		)
	}})
}

func handleAdapterHealth(
	ctx context.Context,
	message jetstream.Msg,
	validator *contractsv1.Validator,
	applier AdapterHealthApplier,
	logger *slog.Logger,
) {
	terminate := func(code string) {
		err := message.Term()
		logger.WarnContext(ctx, "terminate health update", "event", "adapter_health.invalid", "error_code", code)
		if err != nil {
			logger.ErrorContext(ctx, "health termination failed", "error_code", "term_failed")
		}
	}
	// Match the producer's complete encoded NATS message bound, including headers.
	wire := &natsgo.Msg{Subject: message.Subject(), Header: message.Headers(), Data: message.Data()}
	if wire.Size() > 16*1024 {
		terminate("message_too_large")
		return
	}
	envelope, err := natswire.Decode[adapterHealthSnapshot](
		validator,
		contractsv1.AdapterHealthUpdateSchemaID,
		message.Data(),
	)
	if err != nil {
		terminate("invalid_envelope")
		return
	}
	route, err := natswire.ParseAdapterHealthSubject(message.Subject())
	if err != nil || route.AdapterID != envelope.Data.AdapterID {
		terminate("route_mismatch")
		return
	}
	if headerErr := natswire.ValidateAdapterHealthHeaders(message.Headers(), envelope.ID); headerErr != nil {
		terminate("invalid_metadata")
		return
	}
	ctx = natswire.ExtractTrace(ctx, message.Headers())
	update, err := mapAdapterHealth(envelope)
	if err != nil {
		terminate("invalid_snapshot")
		return
	}
	_, err = applier.ApplyAdapterHealth(ctx, update)
	if err != nil {
		if errors.Is(err, devices.ErrInvalidAdapterHealthUpdate) {
			terminate("invalid_projection_update")
			return
		}
		if errors.Is(err, devices.ErrHealthRevisionConflict) {
			terminate("health_revision_conflict")
			return
		}
		if errors.Is(err, devices.ErrCorruptHealthProjection) {
			terminate("corrupt_projection")
			return
		}
		logger.WarnContext(
			ctx,
			"health projection retry",
			"event",
			"adapter_health.retry",
			"error_code",
			"projection_failed",
		)
		if nakErr := message.NakWithDelay(time.Second); nakErr != nil {
			logger.ErrorContext(ctx, "health negative acknowledgement failed", "error_code", "nak_failed")
		}
		return
	}
	// All successful dispositions represent a committed operation, including
	// duplicate and obsolete. No acknowledgement precedes the domain call.
	if ackErr := message.Ack(); ackErr != nil {
		logger.ErrorContext(ctx, "health acknowledgement failed", "error_code", "ack_failed")
	}
}

func mapAdapterHealth(envelope natswire.Envelope[adapterHealthSnapshot]) (devices.AdapterHealthUpdate, error) {
	dto := envelope.Data
	changed, err := time.Parse(time.RFC3339Nano, dto.ChangedAt)
	if err != nil {
		return devices.AdapterHealthUpdate{}, err
	}
	evidence, err := time.Parse(time.RFC3339Nano, dto.EvidenceAt)
	if err != nil {
		return devices.AdapterHealthUpdate{}, err
	}
	snapshot := adapters.HealthSnapshot{
		AdapterID:  dto.AdapterID,
		Revision:   adapters.HealthRevision(dto.Revision),
		Status:     adapters.AdapterHealthStatus(dto.Status),
		Source:     dto.Source,
		ChangedAt:  changed,
		EvidenceAt: evidence,
	}
	if dto.RuntimeID != nil {
		runtime := adapters.RuntimeID(*dto.RuntimeID)
		snapshot.RuntimeID = &runtime
	}
	if dto.ReasonCode != nil {
		snapshot.Reason = &adapters.HealthReason{Code: *dto.ReasonCode}
	}
	if dto.SourceObservedAt != nil {
		observed, parseErr := time.Parse(time.RFC3339Nano, *dto.SourceObservedAt)
		if parseErr != nil {
			return devices.AdapterHealthUpdate{}, parseErr
		}
		snapshot.SourceObservedAt = &observed
	}
	normalized, err := adapters.NormalizeHealthSnapshot(snapshot)
	if err != nil {
		return devices.AdapterHealthUpdate{}, err
	}
	return devices.AdapterHealthUpdate{MessageID: envelope.ID, Snapshot: normalized}, nil
}
