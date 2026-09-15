package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// ListPendingDeviceFacts returns one bounded, oldest-first slice of the
// unpublished Device Facts. Ordering is enqueue order, so a relay that deletes
// what it publishes always makes progress. A non-positive limit is a caller
// defect and leaves the outbox unread.
//
// A row that cannot be decoded is reported as [DeviceFactRowError], which
// matches [ErrInvalidDeviceFactRow] and is permanent: the same stored bytes fail
// the same way on every read, so the caller must preserve the row instead of
// retrying. Because decoding stops at the first such row, the returned slice is
// the valid older prefix read before it, and the caller delivers that prefix
// before faulting on the preserved row. A storage failure of the read itself is
// returned as an ordinary error, carries no prefix, and stays retryable.
func (repository *DeviceRepository) ListPendingDeviceFacts(
	ctx context.Context,
	limit int,
) ([]devices.PendingDeviceFact, error) {
	if limit < 1 {
		return nil, devices.ErrInvalidDeviceFactLimit
	}
	rows, err := repository.queries.ListPendingDeviceFacts(
		ctx, dbsqlc.ListPendingDeviceFactsParams{Limit: int64(limit)},
	)
	if err != nil {
		return nil, fmt.Errorf("list pending device facts: %w", err)
	}
	facts := make([]devices.PendingDeviceFact, 0, len(rows))
	for _, row := range rows {
		fact, mapErr := pendingDeviceFactFromRow(row)
		if mapErr != nil {
			// Return the valid older prefix with the error: the caller publishes
			// and deletes it, so only the poison row and the rows behind it stay
			// blocked. Discarding the prefix would lose durable evidence the
			// fault did not have to block.
			return facts, &devices.DeviceFactRowError{FactID: row.FactID, Cause: mapErr}
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

// DeleteDeviceFact removes one published fact. A fact that is already gone is
// not an error: the row is a pending-work marker, so deleting it twice, or
// after another drain consumed it, has the same meaning as deleting it once.
func (repository *DeviceRepository) DeleteDeviceFact(ctx context.Context, factID devices.DeviceFactID) error {
	if _, err := devices.ParseDeviceFactID(string(factID)); err != nil {
		return fmt.Errorf("delete device fact: %w", err)
	}
	if err := repository.queries.DeleteDeviceFact(
		ctx, dbsqlc.DeleteDeviceFactParams{FactID: string(factID)},
	); err != nil {
		return fmt.Errorf("delete device fact: %w", err)
	}
	return nil
}

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
		CorrelationID:     identity.correlationID,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		CreatedAt:         identity.createdAt,
		Trace:             identity.trace,
	}, nil
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

// queueAcceptedObservationDeviceFact durably enqueues the pending Device Fact
// for one Observation inside the projection transaction, returning its stable
// identity. Eligibility is the devices-owned rule and is enforced here, before
// any ID is minted: only an applied or unchanged Observation is evidence, so a
// rejected outcome and a duplicate that never reached this transaction mint no
// ID and insert no row. A mint or insert failure is returned to roll the
// evidence back, so inbound redelivery retries the evidence and its fact
// together.
func (repository *DeviceRepository) queueAcceptedObservationDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.ProjectObservationParams,
	disposition devices.ObservationDisposition,
	value devices.Value,
) (*devices.DeviceFactID, error) {
	if disposition != devices.DispositionApplied && disposition != devices.DispositionUnchanged {
		return nil, nil //nolint:nilnil // No pending fact is a successful projection.
	}
	createdAt := params.Now().UTC()
	if createdAt.IsZero() {
		return nil, errors.New("queue observation device fact: clock returned zero time")
	}
	factID, err := repository.mintDeviceFactID()
	if err != nil {
		return nil, err
	}
	if insertErr := queries.InsertObservationDeviceFact(
		ctx,
		dbsqlc.InsertObservationDeviceFactParams{
			FactID:        string(factID),
			EntityID:      string(params.Observation.EntityID),
			Variant:       string(disposition),
			SourceID:      string(params.Observation.ID),
			CorrelationID: string(params.Observation.CorrelationID),
			// created_at becomes the published envelope emitted_at, so a fact
			// published late still reports when Core committed it.
			CreatedAt:         formatTime(createdAt),
			Traceparent:       params.Observation.Trace.Traceparent,
			Tracestate:        params.Observation.Trace.Tracestate,
			ValueJson:         sql.NullString{String: string(value), Valid: true},
			AdapterReceivedAt: sql.NullString{String: formatTime(params.Observation.AdapterReceivedAt), Valid: true},
			SourceUpdatedAt:   nullableTime(params.Observation.SourceUpdatedAt),
			ObservedAt:        sql.NullString{String: formatTime(params.ObservedAt), Valid: true},
		},
	); insertErr != nil {
		return nil, fmt.Errorf("insert observation device fact: %w", insertErr)
	}
	return &factID, nil
}

// queueAcceptedEntityEventDeviceFact durably enqueues the pending Device Fact
// for one first-seen accepted Entity Event inside the recording transaction,
// returning its stable identity. Only an accepted disposition is evidence, so a
// rejected, duplicate or identity-conflict outcome mints no ID and inserts no
// row. A mint or insert failure is returned to roll the recorded event back, so
// inbound redelivery retries the event and its fact together.
func (repository *DeviceRepository) queueAcceptedEntityEventDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RecordEntityEventParams,
	disposition devices.EntityEventDisposition,
	recordedAt time.Time,
) (*devices.DeviceFactID, error) {
	if disposition != devices.EntityEventDispositionAccepted {
		return nil, nil //nolint:nilnil // No pending fact is a successful record.
	}
	factID, err := repository.mintDeviceFactID()
	if err != nil {
		return nil, err
	}
	if insertErr := queries.InsertEntityEventDeviceFact(
		ctx,
		dbsqlc.InsertEntityEventDeviceFactParams{
			FactID:        string(factID),
			EntityID:      string(params.Event.EntityID),
			Variant:       string(params.Event.Name),
			SourceID:      string(params.Event.ID),
			CorrelationID: string(params.Event.CorrelationID),
			CreatedAt:     formatTime(recordedAt),
			Traceparent:   params.Event.Trace.Traceparent,
			Tracestate:    params.Event.Trace.Tracestate,
			ReportedAt:    sql.NullString{String: formatTime(params.Event.EmittedAt), Valid: true},
			ReceivedAt:    sql.NullString{String: formatTime(params.ReceivedAt), Valid: true},
			RecordedAt:    sql.NullString{String: formatTime(recordedAt), Valid: true},
		},
	); insertErr != nil {
		return nil, fmt.Errorf("insert entity event device fact: %w", insertErr)
	}
	return &factID, nil
}

// mintDeviceFactID mints one stable fact identity and validates it before it
// reaches SQLite, so a defect in the generator fails the transaction that would
// have carried it instead of persisting a row no relay can publish.
func (repository *DeviceRepository) mintDeviceFactID() (devices.DeviceFactID, error) {
	factID, err := repository.deviceFactID()
	if err != nil {
		return "", fmt.Errorf("mint device fact ID: %w", err)
	}
	if _, parseErr := devices.ParseDeviceFactID(string(factID)); parseErr != nil {
		return "", fmt.Errorf("mint device fact ID: %w", parseErr)
	}
	return factID, nil
}
