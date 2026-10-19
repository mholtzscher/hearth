package adapters

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/propagation"
)

type PendingHealthUpdate struct {
	ID            HealthUpdateID
	Snapshot      HealthSnapshot
	EnqueuedAt    time.Time
	CorrelationID string
	CausationID   *string
	TraceParent   string
	TraceState    string
}

type HealthPublicationStore interface {
	// ListPendingHealthUpdates returns oldest-first work and the valid prefix
	// before any corrupt row. Other read failures return no prefix.
	ListPendingHealthUpdates(context.Context, int) ([]PendingHealthUpdate, error)
	DeleteHealthUpdate(context.Context, HealthUpdateID) error
}

type publicationMetadataKey struct{}

// WithHealthPublicationMetadata carries the originating request's identities to
// a runtime transaction; trace inputs are captured from the active context.
func WithHealthPublicationMetadata(ctx context.Context, correlationID string, causationID *string) context.Context {
	metadata := PendingHealthUpdate{CorrelationID: correlationID}
	if causationID != nil {
		value := *causationID
		metadata.CausationID = &value
	}
	return context.WithValue(ctx, publicationMetadataKey{}, metadata)
}

// HealthPublicationMetadata returns retained request and W3C tracing inputs.
// Core-originated operations mint a fresh correlation when no request exists.
func HealthPublicationMetadata(ctx context.Context) (PendingHealthUpdate, error) {
	metadata, _ := ctx.Value(publicationMetadataKey{}).(PendingHealthUpdate)
	if metadata.CorrelationID == "" {
		id, err := NewHealthUpdateID()
		if err != nil {
			return PendingHealthUpdate{}, err
		}
		metadata.CorrelationID = "cor_" + string(id)[4:]
	}
	if err := validateID(metadata.CorrelationID, "cor"); err != nil {
		return PendingHealthUpdate{}, fmt.Errorf("publication correlation: %w", err)
	}
	if metadata.CausationID != nil {
		if err := validatePublicationCausation(*metadata.CausationID); err != nil {
			return PendingHealthUpdate{}, err
		}
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	metadata.TraceParent = carrier.Get("traceparent")
	metadata.TraceState = carrier.Get("tracestate")
	return metadata, nil
}

func validatePublicationCausation(value string) error {
	// Existing triggering envelope identities are claims, heartbeats, or releases.
	for _, prefix := range []string{"clm", "hbt", "rel"} {
		if validateID(value, prefix) == nil {
			return nil
		}
	}
	return fmt.Errorf("invalid publication causation ID")
}

// ValidateHealthPublicationMetadata checks executable retained publication data.
func ValidateHealthPublicationMetadata(update PendingHealthUpdate) error {
	if _, err := ParseHealthUpdateID(string(update.ID)); err != nil {
		return err
	}
	if err := ValidateUTCTime(update.EnqueuedAt); err != nil {
		return err
	}
	if err := validateID(update.CorrelationID, "cor"); err != nil {
		return err
	}
	if update.CausationID != nil {
		if err := validatePublicationCausation(*update.CausationID); err != nil {
			return err
		}
	}
	return ValidateHealthTraceMetadata(update.TraceParent, update.TraceState)
}

// ValidateHealthTraceMetadata rejects malformed retained W3C tracing inputs.
func ValidateHealthTraceMetadata(traceParent, traceState string) error {
	if traceParent == "" && traceState == "" {
		return nil
	}
	carrier := propagation.MapCarrier{"traceparent": traceParent, "tracestate": traceState}
	extracted := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(propagation.TraceContext{}.Extract(context.Background(), carrier), extracted)
	if extracted.Get("traceparent") != traceParent || extracted.Get("tracestate") != traceState {
		return fmt.Errorf("invalid publication trace metadata")
	}
	return nil
}
