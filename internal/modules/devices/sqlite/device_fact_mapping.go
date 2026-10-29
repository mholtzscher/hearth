package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// deviceFactIdentity is the family-independent part of one pending row, parsed
// once so each family mapper only reads its own columns.
type deviceFactIdentity struct {
	factID        devices.DeviceFactID
	entityID      devices.EntityID
	correlationID devices.CorrelationID
	createdAt     time.Time
	trace         devices.DeviceFactTraceContext
}

func pendingDeviceFactFromRow(row dbsqlc.DeviceFactsOutbox) (devices.PendingDeviceFact, error) {
	identity, err := deviceFactIdentityFromRow(row)
	if err != nil {
		return devices.PendingDeviceFact{}, err
	}
	switch devices.DeviceFactFamily(row.Family) {
	case devices.DeviceFactFamilyObservation:
		fact, observationErr := observationFactFromRow(row, identity)
		if observationErr != nil {
			return devices.PendingDeviceFact{}, observationErr
		}
		return devices.PendingDeviceFact{Sequence: row.EnqueueOrder, Fact: fact}, nil
	case devices.DeviceFactFamilyEntityEvent:
		fact, entityEventErr := entityEventFactFromRow(row, identity)
		if entityEventErr != nil {
			return devices.PendingDeviceFact{}, entityEventErr
		}
		return devices.PendingDeviceFact{Sequence: row.EnqueueOrder, Fact: fact}, nil
	default:
		return devices.PendingDeviceFact{}, fmt.Errorf("unknown device fact family %q", row.Family)
	}
}

func deviceFactIdentityFromRow(row dbsqlc.DeviceFactsOutbox) (deviceFactIdentity, error) {
	factID, err := devices.ParseDeviceFactID(row.FactID)
	if err != nil {
		return deviceFactIdentity{}, fmt.Errorf("parse device fact ID: %w", err)
	}
	entityID, err := devices.ParseEntityID(row.EntityID)
	if err != nil {
		return deviceFactIdentity{}, fmt.Errorf("parse device fact entity ID: %w", err)
	}
	correlationID, err := devices.ParseCorrelationID(row.CorrelationID)
	if err != nil {
		return deviceFactIdentity{}, fmt.Errorf("parse device fact correlation ID: %w", err)
	}
	createdAt, err := parseTime(row.CreatedAt)
	if err != nil {
		return deviceFactIdentity{}, fmt.Errorf("parse device fact created_at: %w", err)
	}
	return deviceFactIdentity{
		factID: factID, entityID: entityID, correlationID: correlationID, createdAt: createdAt,
		trace: devices.DeviceFactTraceContext{Traceparent: row.Traceparent, Tracestate: row.Tracestate},
	}, nil
}

func observationFactFromRow(
	row dbsqlc.DeviceFactsOutbox,
	identity deviceFactIdentity,
) (devices.ObservationFact, error) {
	if !row.ValueJson.Valid || !row.AdapterReceivedAt.Valid || !row.ObservedAt.Valid {
		return devices.ObservationFact{}, errors.New("observation device fact row is incomplete")
	}
	observationID, err := devices.ParseObservationID(row.SourceID)
	if err != nil {
		return devices.ObservationFact{}, fmt.Errorf("parse device fact observation ID: %w", err)
	}
	disposition := devices.ObservationDisposition(row.Variant)
	if disposition != devices.DispositionApplied && disposition != devices.DispositionUnchanged {
		return devices.ObservationFact{}, fmt.Errorf("device fact disposition %q is not eligible evidence", row.Variant)
	}
	adapterReceivedAt, err := parseTime(row.AdapterReceivedAt.String)
	if err != nil {
		return devices.ObservationFact{}, fmt.Errorf("parse device fact adapter_received_at: %w", err)
	}
	observedAt, err := parseTime(row.ObservedAt.String)
	if err != nil {
		return devices.ObservationFact{}, fmt.Errorf("parse device fact observed_at: %w", err)
	}
	sourceUpdatedAt, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return devices.ObservationFact{}, fmt.Errorf("parse device fact source_updated_at: %w", err)
	}
	return devices.ObservationFact{
		ID:                identity.factID,
		ObservationID:     observationID,
		EntityID:          identity.entityID,
		Disposition:       disposition,
		Value:             devices.Value(row.ValueJson.String),
		PreviousValue:     deviceFactValue(row.PreviousValueJson),
		CorrelationID:     identity.correlationID,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		CreatedAt:         identity.createdAt,
		Trace:             identity.trace,
	}, nil
}

func deviceFactValue(value sql.NullString) devices.Value {
	if !value.Valid {
		return nil
	}
	return append(devices.Value(nil), value.String...)
}

func nullableDeviceFactValue(value devices.Value) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}

func entityEventFactFromRow(
	row dbsqlc.DeviceFactsOutbox,
	identity deviceFactIdentity,
) (devices.EntityEventFact, error) {
	if !row.ReportedAt.Valid || !row.ReceivedAt.Valid || !row.RecordedAt.Valid {
		return devices.EntityEventFact{}, errors.New("entity event device fact row is incomplete")
	}
	eventID, err := devices.ParseEntityEventID(row.SourceID)
	if err != nil {
		return devices.EntityEventFact{}, fmt.Errorf("parse device fact event ID: %w", err)
	}
	if _, nameErr := devices.ParseEntityEventName(row.Variant); nameErr != nil {
		return devices.EntityEventFact{}, fmt.Errorf("device fact event name %q is not canonical", row.Variant)
	}
	reportedAt, err := parseTime(row.ReportedAt.String)
	if err != nil {
		return devices.EntityEventFact{}, fmt.Errorf("parse device fact reported_at: %w", err)
	}
	receivedAt, err := parseTime(row.ReceivedAt.String)
	if err != nil {
		return devices.EntityEventFact{}, fmt.Errorf("parse device fact received_at: %w", err)
	}
	recordedAt, err := parseTime(row.RecordedAt.String)
	if err != nil {
		return devices.EntityEventFact{}, fmt.Errorf("parse device fact recorded_at: %w", err)
	}
	return devices.EntityEventFact{
		ID:            identity.factID,
		EventID:       eventID,
		EntityID:      identity.entityID,
		Name:          devices.EntityEventName(row.Variant),
		CorrelationID: identity.correlationID,
		ReportedAt:    reportedAt,
		ReceivedAt:    receivedAt,
		RecordedAt:    recordedAt,
		CreatedAt:     identity.createdAt,
		Trace:         identity.trace,
	}, nil
}
