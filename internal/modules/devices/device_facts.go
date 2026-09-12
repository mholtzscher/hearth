package devices

import (
	"context"
	"time"
)

// DeviceFactID is the envelope identity of one ephemeral Device Fact
// publication: a canonical fct_<UUIDv7>. It identifies the published message,
// not the durable Observation or Entity Event the fact reports, and it is
// never reused, retried or replayed.
type DeviceFactID string

// ObservationFact is the external projection of one accepted Observation. It
// carries only canonical committed data, never Adapter identity or rejected
// input. Disposition is applied or unchanged; rejected and duplicate
// Observations are never facts.
type ObservationFact struct {
	ObservationID     ObservationID
	EntityID          EntityID
	Disposition       ObservationDisposition
	Value             Value // normalized State JSON committed with the Observation
	CorrelationID     CorrelationID
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	ObservedAt        time.Time // JetStream storage time
}

// EntityEventFact is the external projection of one first-seen accepted Entity
// Event. ReportedAt is the Adapter SDK publication time copied from the durable
// event record, ReceivedAt is JetStream storage time and RecordedAt is Core's
// first-record time.
type EntityEventFact struct {
	EventID       EntityEventID
	EntityID      EntityID
	Name          EntityEventName
	CorrelationID CorrelationID
	ReportedAt    time.Time
	ReceivedAt    time.Time
	RecordedAt    time.Time
}

// DeviceFactSink receives only facts whose owning SQLite transition committed.
// Implementations own transport validation, freshness, logging and delivery.
// They must never return an error, block on NATS I/O, retry, durably retain a
// fact, or make a committed devices operation depend on publication.
//
// The methods make invalid family and type combinations unrepresentable and
// state the devices-owned eligibility rule at each call site. A nil sink is a
// no-op, so focused devices tests and non-NATS assembly need no transport
// setup.
type DeviceFactSink interface {
	ObservationAccepted(ctx context.Context, fact ObservationFact)
	EntityEventAccepted(ctx context.Context, fact EntityEventFact)
}

// emitObservationFact enqueues one accepted Observation fact after its owning
// transaction committed. Only applied and unchanged Observations are facts:
// rejected and duplicate Observations are never facts, and a nil sink is a
// no-op. The normalized value comes from the committed State, never from the
// raw report, and the value is copied so the sink owns no committed buffer.
func (service *Service) emitObservationFact(
	ctx context.Context,
	observation Observation,
	result ProjectionResult,
	observedAt time.Time,
) {
	if service.deviceFacts == nil {
		return
	}
	if result.Disposition != DispositionApplied && result.Disposition != DispositionUnchanged {
		return
	}
	if result.State == nil {
		return
	}
	service.deviceFacts.ObservationAccepted(ctx, ObservationFact{
		ObservationID:     observation.ID,
		EntityID:          observation.EntityID,
		Disposition:       result.Disposition,
		Value:             append(Value(nil), result.State.Value...),
		CorrelationID:     observation.CorrelationID,
		AdapterReceivedAt: observation.AdapterReceivedAt.UTC(),
		SourceUpdatedAt:   copyTimePointer(observation.SourceUpdatedAt),
		ObservedAt:        observedAt.UTC(),
	})
}

// emitEntityEventFact enqueues one first-seen accepted Entity Event fact after
// its owning transaction committed. Rejected, duplicate and identity-conflict
// results are never facts, and a nil sink is a no-op. ReportedAt is the Adapter
// SDK publication time from the trusted report, ReceivedAt is JetStream storage
// time and RecordedAt is the Core record time returned by the commit.
func (service *Service) emitEntityEventFact(
	ctx context.Context,
	event EntityEvent,
	receivedAt time.Time,
	result EntityEventRecordResult,
) {
	if service.deviceFacts == nil {
		return
	}
	if result.Outcome != EntityEventOutcomeAccepted {
		return
	}
	service.deviceFacts.EntityEventAccepted(ctx, EntityEventFact{
		EventID:       event.ID,
		EntityID:      event.EntityID,
		Name:          event.Name,
		CorrelationID: event.CorrelationID,
		ReportedAt:    event.EmittedAt.UTC(),
		ReceivedAt:    receivedAt.UTC(),
		RecordedAt:    result.RecordedAt.UTC(),
	})
}
