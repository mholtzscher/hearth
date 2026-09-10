package devices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// DeviceEventHistoryRetention is the fixed SQLite retention window for
// retained Device Event rows. It exceeds the seven-day JetStream window so
// broker-acknowledged history outlives the stream, and it is deliberately not
// configurable.
const DeviceEventHistoryRetention = 30 * 24 * time.Hour

// deviceEventDeleteBatchSize bounds one retention transaction so pruning
// releases the SQLite connection between batches.
const deviceEventDeleteBatchSize = 500

var deviceEventNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// DeviceEvent is one wire-valid occurrence report Core is asked to record. The
// event ID identifies the report, not the occurrence: redelivery of one ID is
// the same event, and a new ID is a new occurrence even when the name repeats.
type DeviceEvent struct {
	ID            DeviceEventID
	EntityID      EntityID
	Name          DeviceEventName
	CorrelationID CorrelationID
	EmittedAt     time.Time
}

// RecordDeviceEventParams carries one trusted recording request. Now is called
// by persistence only when Core records a first-seen row, so duplicates and
// identity conflicts never move or refresh the stored timestamps.
type RecordDeviceEventParams struct {
	AdapterID  string
	RuntimeID  RuntimeID
	Event      DeviceEvent
	ReceivedAt time.Time // JetStream metadata timestamp
	Now        func() time.Time
}

// DeviceEventDisposition is the persisted verdict for one first-seen report.
type DeviceEventDisposition string

const (
	DeviceEventDispositionAccepted DeviceEventDisposition = "accepted"
	DeviceEventDispositionRejected DeviceEventDisposition = "rejected"
)

// DeviceEventRejection enumerates why Core rejected a first-seen report. The
// value describes Core's processing-time rules, never physical truth.
type DeviceEventRejection string

const (
	DeviceEventRejectionStaleRuntime     DeviceEventRejection = "stale_runtime"
	DeviceEventRejectionUnknownEntity    DeviceEventRejection = "unknown_entity"
	DeviceEventRejectionWrongAdapter     DeviceEventRejection = "wrong_adapter"
	DeviceEventRejectionEntityDisabled   DeviceEventRejection = "entity_disabled"
	DeviceEventRejectionUnsupportedEvent DeviceEventRejection = "unsupported_event"
)

// DeviceEventRecordOutcome classifies one recording attempt, including the
// attempts that leave the existing row untouched.
type DeviceEventRecordOutcome string

const (
	DeviceEventOutcomeAccepted         DeviceEventRecordOutcome = "accepted"
	DeviceEventOutcomeRejected         DeviceEventRecordOutcome = "rejected"
	DeviceEventOutcomeDuplicate        DeviceEventRecordOutcome = "duplicate"
	DeviceEventOutcomeIdentityConflict DeviceEventRecordOutcome = "identity_conflict"
)

// DeviceEventRecordResult reports what Core did with one recording attempt.
type DeviceEventRecordResult struct {
	Outcome   DeviceEventRecordOutcome
	Rejection *DeviceEventRejection // only for a newly rejected event
}

// RecordDeviceEvent validates one trusted recording request before writing and
// then records the report inside one devices-owned transaction. The core
// service supplies the clock, so transport cannot set recorded_at.
func (service *Service) RecordDeviceEvent(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	event DeviceEvent,
	receivedAt time.Time,
) (DeviceEventRecordResult, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return DeviceEventRecordResult{}, fmt.Errorf(
			"%w: adapter ID must be a subject-safe slug", ErrInvalidDeviceEvent,
		)
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: parse runtime ID: %w", ErrInvalidDeviceEvent, err)
	}
	if _, err := ParseDeviceEventID(string(event.ID)); err != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: parse event ID: %w", ErrInvalidDeviceEvent, err)
	}
	if _, err := ParseEntityID(string(event.EntityID)); err != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidDeviceEvent, err)
	}
	if _, err := ParseCorrelationID(string(event.CorrelationID)); err != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: parse correlation ID: %w", ErrInvalidDeviceEvent, err)
	}
	if !deviceEventNamePattern.MatchString(string(event.Name)) {
		return DeviceEventRecordResult{}, fmt.Errorf(
			"%w: Device Event name %q is not a canonical name slug", ErrInvalidDeviceEvent, event.Name,
		)
	}
	if event.EmittedAt.IsZero() {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: emitted_at is required", ErrInvalidDeviceEvent)
	}
	if receivedAt.IsZero() {
		return DeviceEventRecordResult{}, fmt.Errorf("%w: received_at is required", ErrInvalidDeviceEvent)
	}

	result, err := service.stores.DeviceEvents.RecordDeviceEvent(ctx, RecordDeviceEventParams{
		AdapterID:  adapterID,
		RuntimeID:  runtimeID,
		Event:      event,
		ReceivedAt: receivedAt.UTC(),
		Now:        service.dependencies.Now,
	})
	if err != nil {
		return DeviceEventRecordResult{}, err
	}
	return copyDeviceEventRecordResult(result), nil
}

// DeleteExpiredDeviceEvents deletes retained Device Events recorded before the
// fixed retention window, in bounded batches of one transaction each. The
// sweep uses one cutoff derived from the supplied Core now and deletes records
// strictly older than it; history has no current-State anchor, so nothing is
// exempt. Never prune at startup: the caller owns the maintenance schedule.
func (service *Service) DeleteExpiredDeviceEvents(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("device event prune time is required")
	}
	cutoff := now.UTC().Add(-DeviceEventHistoryRetention)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := service.stores.DeviceEvents.DeleteDeviceEventsBefore(
			ctx, cutoff, deviceEventDeleteBatchSize,
		)
		if err != nil {
			return err
		}
		if deleted < deviceEventDeleteBatchSize {
			return nil
		}
	}
}

func copyDeviceEventRecordResult(result DeviceEventRecordResult) DeviceEventRecordResult {
	cloned := result
	if result.Rejection != nil {
		rejection := *result.Rejection
		cloned.Rejection = &rejection
	}
	return cloned
}
