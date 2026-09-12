package devices

import (
	"errors"
	"fmt"
	"time"
)

// DeviceFactID is the stable identity of one queued Device Fact: a canonical
// fct_<UUIDv7>. It identifies the pending outbox row and becomes the published
// envelope identity, so one committed fact keeps one identity for its whole
// life. It is unrelated to the durable Observation or Entity Event the fact
// reports and it is never reused.
type DeviceFactID string

// DeviceFactFamily is the closed family of evidence a Device Fact reports. The
// tokens are the published family tokens, so the relay routes a pending fact
// without a translation table and the stored family names the fact rather than
// an implementation detail.
type DeviceFactFamily string

const (
	DeviceFactFamilyObservation DeviceFactFamily = "observation"
	DeviceFactFamilyEntityEvent DeviceFactFamily = "entity-event"
)

const (
	// deviceFactTraceparentMaxBytes bounds one persisted W3C traceparent. A
	// canonical traceparent is 55 bytes, so the budget accepts every header a
	// transport can hand over without letting an unbounded value reach SQLite.
	deviceFactTraceparentMaxBytes = 128
	// deviceFactTracestateMaxBytes bounds one persisted W3C tracestate. The W3C
	// specification caps a tracestate at 512 bytes.
	deviceFactTracestateMaxBytes = 512
)

// ErrInvalidDeviceFactTrace is the permanent input class for a trace context
// that cannot be persisted bounded. It reports a defect in the caller, not
// physical truth, and ordinary storage failures never match it.
var ErrInvalidDeviceFactTrace = errors.New("invalid device fact trace context")

// ErrInvalidDeviceFactRow is the permanent failure class for one durable outbox
// row that cannot be decoded into a canonical Device Fact. The stored bytes fail
// the same way on every read, so the failure is deterministic and a relay must
// preserve the row and fault instead of retrying a decode that can never
// succeed. A transient storage failure -- an unavailable database, a cancelled
// or timed-out read -- never matches it and stays retryable.
var ErrInvalidDeviceFactRow = errors.New("invalid device fact outbox row")

// DeviceFactRowError names the one durable outbox row Core cannot decode, so a
// fault diagnostic can identify the preserved row. FactID is the raw stored
// identity and is reported even when the malformed value is that identity
// itself, because the raw string is what an operator has to search for.
type DeviceFactRowError struct {
	FactID string
	Cause  error
}

func (rowErr *DeviceFactRowError) Error() string {
	return fmt.Sprintf("%v: fact %q: %v", ErrInvalidDeviceFactRow, rowErr.FactID, rowErr.Cause)
}

// Unwrap exposes the deterministic decode failure, so a caller can inspect the
// specific malformed field without losing the permanent classification.
func (rowErr *DeviceFactRowError) Unwrap() error { return rowErr.Cause }

// Is classifies every decode failure as the one permanent invalid-row class, so
// a relay distinguishes a row that can never be read from a retryable read
// failure without inspecting error text.
func (rowErr *DeviceFactRowError) Is(target error) bool { return target == ErrInvalidDeviceFactRow }

// DeviceFactTraceContext is the inbound W3C trace context one accepted report
// carried, retained so the published fact continues the originating trace
// instead of starting a new one. Fields are empty when the report carried no
// trace. Transport extracts and injects these headers; devices only carries
// them.
type DeviceFactTraceContext struct {
	Traceparent string
	Tracestate  string
}

// Validate reports whether the trace context can be persisted unchanged. It
// bounds size and requires printable ASCII, and it deliberately does not parse
// W3C syntax: validating the traceparent and tracestate shapes belongs to
// transport, which owns the wire vocabulary, so devices never imports it.
func (trace DeviceFactTraceContext) Validate() error {
	if !boundedPrintableASCII(trace.Traceparent, deviceFactTraceparentMaxBytes) {
		return fmt.Errorf(
			"%w: traceparent must be at most %d printable ASCII bytes",
			ErrInvalidDeviceFactTrace, deviceFactTraceparentMaxBytes,
		)
	}
	if !boundedPrintableASCII(trace.Tracestate, deviceFactTracestateMaxBytes) {
		return fmt.Errorf(
			"%w: tracestate must be at most %d printable ASCII bytes",
			ErrInvalidDeviceFactTrace, deviceFactTracestateMaxBytes,
		)
	}
	return nil
}

func boundedPrintableASCII(value string, maxBytes int) bool {
	if len(value) > maxBytes {
		return false
	}
	for index := range len(value) {
		if value[index] < ' ' || value[index] > '~' {
			return false
		}
	}
	return true
}

// ObservationFact is the canonical projection of one accepted Observation. It
// carries only canonical committed data, never Adapter identity or rejected
// input. Disposition is applied or unchanged; rejected and duplicate
// Observations are never facts. ID and CreatedAt identify the pending fact
// itself, Value is the normalized State JSON committed with the Observation,
// and the remaining fields are the evidence the fact reports.
type ObservationFact struct {
	ID                DeviceFactID
	ObservationID     ObservationID
	EntityID          EntityID
	Disposition       ObservationDisposition
	Value             Value // normalized State JSON committed with the Observation
	CorrelationID     CorrelationID
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	ObservedAt        time.Time // JetStream storage time
	CreatedAt         time.Time // Core commit time, published as emitted_at
	Trace             DeviceFactTraceContext
}

// EntityEventFact is the canonical projection of one first-seen accepted Entity
// Event. ReportedAt is the Adapter SDK publication time copied from the durable
// event record, ReceivedAt is JetStream storage time and RecordedAt is Core's
// first-record time. ID and CreatedAt identify the pending fact itself.
type EntityEventFact struct {
	ID            DeviceFactID
	EventID       EntityEventID
	EntityID      EntityID
	Name          EntityEventName
	CorrelationID CorrelationID
	ReportedAt    time.Time
	ReceivedAt    time.Time
	RecordedAt    time.Time
	CreatedAt     time.Time // Core commit time, published as emitted_at
	Trace         DeviceFactTraceContext
}

// DeviceFactFamily reports which evidence this fact reports, so a holder of one
// pending fact can route it without knowing the concrete family.
func (ObservationFact) DeviceFactFamily() DeviceFactFamily { return DeviceFactFamilyObservation }

// DeviceFactFamily reports which evidence this fact reports.
func (EntityEventFact) DeviceFactFamily() DeviceFactFamily { return DeviceFactFamilyEntityEvent }

func (ObservationFact) deviceFact() {}

func (EntityEventFact) deviceFact() {}

// DeviceFact is exactly one typed fact: either an ObservationFact or an
// EntityEventFact. The unexported method seals the set, so a pending fact
// always carries one of the two canonical projections and never an opaque
// payload or a third family.
type DeviceFact interface {
	DeviceFactFamily() DeviceFactFamily
	deviceFact()
}

// PendingDeviceFact is one durable, not-yet-published Device Fact in enqueue
// order. Sequence orders the pending set; Fact is the typed evidence the relay
// publishes.
type PendingDeviceFact struct {
	Sequence int64
	Fact     DeviceFact
}

// notifyPendingDeviceFact wakes the relay after a transaction that committed
// one pending fact. A transaction that queued nothing has no pending fact ID
// and wakes nobody, so the hint states the devices-owned eligibility rule
// instead of restating it. The hint is best-effort: a nil notifier or a lost
// hint only defers publication until the relay's next poll.
func (service *Service) notifyPendingDeviceFact(pendingFactID *DeviceFactID) {
	if pendingFactID == nil {
		return
	}
	if service.deviceFacts != nil {
		service.deviceFacts.NotifyPendingDeviceFacts()
	}
}
