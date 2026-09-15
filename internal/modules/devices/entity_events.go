package devices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// EntityEventHistoryRetention is the fixed SQLite retention window for
// retained Entity Event rows. It exceeds the seven-day JetStream window so
// broker-acknowledged history outlives the stream, and it is deliberately not
// configurable.
const EntityEventHistoryRetention = 30 * 24 * time.Hour

// entityEventDeleteBatchSize bounds one retention transaction so pruning
// releases the SQLite connection between batches.
const entityEventDeleteBatchSize = 500

var entityEventNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ParseEntityEventName validates one canonical Entity Event name: a lowercase
// slug of at most 63 characters that starts with a letter or digit. Persistence
// adapters revalidate a stored event name with it before a Device Fact carries
// that name.
func ParseEntityEventName(value string) (EntityEventName, error) {
	if !entityEventNamePattern.MatchString(value) {
		return "", fmt.Errorf("Entity Event name %q is not a canonical name slug", value)
	}
	return EntityEventName(value), nil
}

// EntityEvent is one wire-valid occurrence report Core is asked to record. The
// event ID identifies the report, not the occurrence: redelivery of one ID is
// the same event, and a new ID is a new occurrence even when the name repeats.
type EntityEvent struct {
	ID            EntityEventID
	EntityID      EntityID
	Name          EntityEventName
	CorrelationID CorrelationID
	EmittedAt     time.Time
	// Trace is the inbound W3C trace context the report carried, retained with
	// any Device Fact this Entity Event produces so publication continues the
	// originating trace.
	Trace DeviceFactTraceContext
}

// ErrEntityEventDescriptorCorrupt is the permanent Entity Event descriptor
// class: Core resolved the Entity but could not interpret its persisted
// descriptor as an Entity Event source, either because the Entity Type is
// unknown or because the persisted event-source support no longer satisfies
// its schema. The failure is deterministic for a given persisted row, so a
// report that hits it can never succeed by redelivery and the consumer
// terminates it instead. Ordinary query, transaction, and commit failures
// never match it.
var ErrEntityEventDescriptorCorrupt = errors.New("entity event source descriptor cannot be interpreted")

// EntityEventDescriptorError carries one permanent descriptor
// interpretation failure for an Entity Event report. Error returns a fixed
// message naming only the Entity and its type, so a diagnostic can classify
// the failure without exposing persisted descriptor bytes. Unwrap keeps both
// the sentinel class and the underlying catalog cause available to callers.
type EntityEventDescriptorError struct {
	EntityID EntityID
	TypeID   EntityTypeID
	cause    error
}

// NewEntityEventDescriptorError builds the permanent descriptor-interpretation
// failure for one Entity Event report from the catalog cause that made the
// persisted descriptor unreadable. The cause stays private, so only
// [EntityEventDescriptorError.Unwrap] can expose it to a caller that explicitly
// unwraps the failure.
func NewEntityEventDescriptorError(
	entityID EntityID,
	typeID EntityTypeID,
	cause error,
) *EntityEventDescriptorError {
	return &EntityEventDescriptorError{EntityID: entityID, TypeID: typeID, cause: cause}
}

func (failure *EntityEventDescriptorError) Error() string {
	return fmt.Sprintf(
		"%s: entity %q has entity type %q",
		ErrEntityEventDescriptorCorrupt, failure.EntityID, failure.TypeID,
	)
}

// Unwrap exposes the permanent class for [errors.Is] and the wrapped catalog
// cause for inspection. Error never renders the cause, so nothing that logs
// this failure can leak raw descriptor bytes.
func (failure *EntityEventDescriptorError) Unwrap() []error {
	return []error{ErrEntityEventDescriptorCorrupt, failure.cause}
}

// RecordEntityEventParams carries one trusted recording request. Now is called
// by persistence only when Core records a first-seen row, so duplicates and
// identity conflicts never move or refresh the stored timestamps.
type RecordEntityEventParams struct {
	AdapterID  string
	RuntimeID  RuntimeID
	Event      EntityEvent
	ReceivedAt time.Time // JetStream metadata timestamp
	Now        func() time.Time
}

// EntityEventDisposition is the persisted verdict for one first-seen report.
type EntityEventDisposition string

const (
	EntityEventDispositionAccepted EntityEventDisposition = "accepted"
	EntityEventDispositionRejected EntityEventDisposition = "rejected"
)

// EntityEventRejection enumerates why Core rejected a first-seen report. The
// value describes Core's processing-time rules, never physical truth.
type EntityEventRejection string

const (
	EntityEventRejectionStaleRuntime     EntityEventRejection = "stale_runtime"
	EntityEventRejectionUnknownEntity    EntityEventRejection = "unknown_entity"
	EntityEventRejectionWrongAdapter     EntityEventRejection = "wrong_adapter"
	EntityEventRejectionEntityDisabled   EntityEventRejection = "entity_disabled"
	EntityEventRejectionUnsupportedEvent EntityEventRejection = "unsupported_event"
)

// EntityEventRecordOutcome classifies one recording attempt, including the
// attempts that leave the existing row untouched.
type EntityEventRecordOutcome string

const (
	EntityEventOutcomeAccepted         EntityEventRecordOutcome = "accepted"
	EntityEventOutcomeRejected         EntityEventRecordOutcome = "rejected"
	EntityEventOutcomeDuplicate        EntityEventRecordOutcome = "duplicate"
	EntityEventOutcomeIdentityConflict EntityEventRecordOutcome = "identity_conflict"
)

// EntityEventRecordResult reports what Core did with one recording attempt.
// RecordedAt is the Core first-record time committed with a first-seen accepted
// or rejected row; duplicates and identity conflicts leave the existing row
// untouched and leave it zero. PendingFactID is the stable identity of the
// Device Fact a first-seen accepted report queued, nil for every other outcome.
type EntityEventRecordResult struct {
	Outcome       EntityEventRecordOutcome
	Rejection     *EntityEventRejection // only for a newly rejected event
	RecordedAt    time.Time
	PendingFactID *DeviceFactID
}

// RecordEntityEvent validates one trusted recording request before writing and
// then records the report inside one devices-owned transaction. The core
// service supplies the clock, so transport cannot set recorded_at.
func (service *Service) RecordEntityEvent(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	event EntityEvent,
	receivedAt time.Time,
) (EntityEventRecordResult, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return EntityEventRecordResult{}, fmt.Errorf(
			"%w: adapter ID must be a subject-safe slug", ErrInvalidEntityEvent,
		)
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: parse runtime ID: %w", ErrInvalidEntityEvent, err)
	}
	if _, err := ParseEntityEventID(string(event.ID)); err != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: parse event ID: %w", ErrInvalidEntityEvent, err)
	}
	if _, err := ParseEntityID(string(event.EntityID)); err != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidEntityEvent, err)
	}
	if _, err := ParseCorrelationID(string(event.CorrelationID)); err != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: parse correlation ID: %w", ErrInvalidEntityEvent, err)
	}
	if _, nameErr := ParseEntityEventName(string(event.Name)); nameErr != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: %w", ErrInvalidEntityEvent, nameErr)
	}
	if event.EmittedAt.IsZero() {
		return EntityEventRecordResult{}, fmt.Errorf("%w: emitted_at is required", ErrInvalidEntityEvent)
	}
	if receivedAt.IsZero() {
		return EntityEventRecordResult{}, fmt.Errorf("%w: received_at is required", ErrInvalidEntityEvent)
	}
	if traceErr := event.Trace.Validate(); traceErr != nil {
		return EntityEventRecordResult{}, fmt.Errorf("%w: %w", ErrInvalidEntityEvent, traceErr)
	}

	result, err := service.stores.EntityEvents.RecordEntityEvent(ctx, RecordEntityEventParams{
		AdapterID:  adapterID,
		RuntimeID:  runtimeID,
		Event:      event,
		ReceivedAt: receivedAt.UTC(),
		Now:        service.dependencies.Now,
	})
	if err != nil {
		return EntityEventRecordResult{}, err
	}
	service.notifyPendingDeviceFact(result.PendingFactID)
	return copyEntityEventRecordResult(result), nil
}

// DeleteExpiredEntityEvents deletes retained Entity Events recorded before the
// fixed retention window, in bounded batches of one transaction each. The
// sweep uses one cutoff derived from the supplied Core now and deletes records
// strictly older than it; history has no current-State anchor, so nothing is
// exempt. The caller owns the startup and periodic maintenance schedule.
func (service *Service) DeleteExpiredEntityEvents(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("entity event prune time is required")
	}
	cutoff := now.UTC().Add(-EntityEventHistoryRetention)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := service.stores.EntityEvents.DeleteEntityEventsBefore(
			ctx, cutoff, entityEventDeleteBatchSize,
		)
		if err != nil {
			return err
		}
		if deleted < entityEventDeleteBatchSize {
			return nil
		}
	}
}

func copyEntityEventRecordResult(result EntityEventRecordResult) EntityEventRecordResult {
	cloned := result
	if result.Rejection != nil {
		rejection := *result.Rejection
		cloned.Rejection = &rejection
	}
	if result.PendingFactID != nil {
		factID := *result.PendingFactID
		cloned.PendingFactID = &factID
	}
	return cloned
}
