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
	"github.com/mholtzscher/hearth/internal/platform/outbox"
)

// healthSnapshot is a transport DTO, not a serialization of domain structs.
type healthSnapshot struct {
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

// HealthRelay owns exactly one publisher, including during drain. Its hints
// carry no durable work; the repository and periodic poll are authoritative.
type HealthRelay struct {
	*outbox.Relay[adapters.PendingHealthUpdate]
}

// StartHealthRelay starts the sole publisher after stream provisioning and
// startup QueueCurrentHealthSnapshots have succeeded. Backlog is not a readiness
// gate: assembly checks Active, Fault, and live resource compatibility instead.
func StartHealthRelay(
	js jetstream.JetStream,
	store adapters.HealthPublicationStore,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*HealthRelay, error) {
	if js == nil || store == nil || validator == nil {
		return nil, errors.New("health relay dependencies required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	source := &healthPublicationSource{store: store, validator: validator, logger: logger}
	relay, err := outbox.Start(js, outbox.Config[adapters.PendingHealthUpdate]{
		Source: source, Stream: HealthStreamName, Timing: outbox.DefaultTiming(),
		IsPoison: func(err error) bool { return errors.Is(err, adapters.ErrCorruptHealthEvidence) },
		Report:   source.report,
	})
	if err != nil {
		return nil, err
	}
	return &HealthRelay{Relay: relay}, nil
}

func (r *HealthRelay) NotifyPendingHealthUpdates() {
	r.Notify()
}

type healthPublicationSource struct {
	store     adapters.HealthPublicationStore
	validator *contractsv1.Validator
	logger    *slog.Logger
}

func (source *healthPublicationSource) Read(ctx context.Context, limit int) ([]adapters.PendingHealthUpdate, error) {
	return source.store.ListPendingHealthUpdates(ctx, limit)
}

func (source *healthPublicationSource) Delete(ctx context.Context, row adapters.PendingHealthUpdate) error {
	return source.store.DeleteHealthUpdate(ctx, row.ID)
}

func (source *healthPublicationSource) report(
	ctx context.Context,
	failure outbox.DeliveryError[adapters.PendingHealthUpdate],
) {
	level := slog.LevelWarn
	message, event := "health publication retry", "adapter_health.retry"
	if failure.Permanent {
		level = slog.LevelError
		message, event = "health publication poison", "adapter_health.poison"
	}
	source.logger.LogAttrs(ctx, level, message,
		slog.String("event", event),
		slog.String("stage", failure.Stage),
		slog.String("error_code", failure.Code),
	)
}

func (source *healthPublicationSource) Prepare(row adapters.PendingHealthUpdate) (*natsgo.Msg, error) {
	if err := adapters.ValidateHealthPublicationMetadata(row); err != nil {
		return nil, err
	}
	snapshot, err := adapters.NormalizeHealthSnapshot(row.Snapshot)
	if err != nil {
		return nil, err
	}
	dto := healthSnapshot{
		AdapterID:  snapshot.AdapterID,
		Revision:   int64(snapshot.Revision),
		Status:     string(snapshot.Status),
		Source:     snapshot.Source,
		ChangedAt:  snapshot.ChangedAt.Format(time.RFC3339Nano),
		EvidenceAt: snapshot.EvidenceAt.Format(time.RFC3339Nano),
	}
	if snapshot.RuntimeID != nil {
		value := string(*snapshot.RuntimeID)
		dto.RuntimeID = &value
	}
	if snapshot.Reason != nil {
		value := snapshot.Reason.Code
		dto.ReasonCode = &value
	}
	if snapshot.SourceObservedAt != nil {
		value := snapshot.SourceObservedAt.Format(time.RFC3339Nano)
		dto.SourceObservedAt = &value
	}
	payload, err := natswire.Encode(
		source.validator,
		contractsv1.AdapterHealthUpdateSchemaID,
		natswire.Envelope[healthSnapshot]{
			ID:            string(row.ID),
			Schema:        contractsv1.AdapterHealthUpdateSchemaID,
			EmittedAt:     row.EnqueuedAt.Format(time.RFC3339Nano),
			CorrelationID: row.CorrelationID,
			CausationID:   row.CausationID,
			Data:          dto,
		},
	)
	if err != nil {
		return nil, err
	}
	subject, err := natswire.AdapterHealthSubject(snapshot.AdapterID)
	if err != nil {
		return nil, err
	}
	message := natsgo.NewMsg(subject)
	message.Data = payload
	message.Header.Set(natsgo.MsgIdHdr, string(row.ID))
	if row.TraceParent != "" {
		message.Header.Set("traceparent", row.TraceParent)
	}
	if row.TraceState != "" {
		message.Header.Set("tracestate", row.TraceState)
	}
	if headerErr := natswire.ValidateAdapterHealthHeaders(message.Header, string(row.ID)); headerErr != nil {
		return nil, headerErr
	}
	if message.Size() > HealthMaxMessageSize {
		return nil, errors.New("health message too large")
	}
	return message, nil
}
