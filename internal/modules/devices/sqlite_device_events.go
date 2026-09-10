package devices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

// RecordDeviceEvent records one wire-valid Device Event inside one
// transaction. The event ID is the lookup key: identical immutable input is a
// duplicate and changed input is an identity conflict, and both leave the
// first row, its timestamps, and its disposition untouched. An accepted or
// rejected row is written only after every processing-time check passes in
// order, so the persisted rejection is the first failing rule.
//
// The fingerprint hashes the immutable reported tuple with fixed array order
// and no extra whitespace. Delivery count, Core receipt and recording times,
// and trace headers stay out of it, so Core cannot manufacture a conflict from
// its own metadata.
func (repository *SQLiteRepository) RecordDeviceEvent(
	ctx context.Context,
	params RecordDeviceEventParams,
) (DeviceEventRecordResult, error) {
	if repository.catalog == nil {
		return DeviceEventRecordResult{}, errors.New("record device event: entity type catalog is required")
	}
	if params.Now == nil {
		return DeviceEventRecordResult{}, errors.New("record device event: clock is required")
	}
	if params.Event.EmittedAt.IsZero() || params.ReceivedAt.IsZero() {
		return DeviceEventRecordResult{}, errors.New("record device event: report times are required")
	}
	fingerprint, err := deviceEventFingerprint(params)
	if err != nil {
		return DeviceEventRecordResult{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("begin device event recording: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)

	existing, err := queries.GetDeviceEvent(ctx, dbsqlc.GetDeviceEventParams{EventID: string(params.Event.ID)})
	switch {
	case err == nil:
		// The row exists: the ID already means something in this household.
		if bytes.Equal(existing.Fingerprint, fingerprint) {
			return DeviceEventRecordResult{Outcome: DeviceEventOutcomeDuplicate}, nil
		}
		return DeviceEventRecordResult{Outcome: DeviceEventOutcomeIdentityConflict}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return DeviceEventRecordResult{}, fmt.Errorf("look up device event: %w", err)
	default:
	}

	disposition, rejection, err := repository.classifyDeviceEvent(ctx, queries, params)
	if err != nil {
		return DeviceEventRecordResult{}, err
	}
	recordedAt := params.Now().UTC()
	if recordedAt.IsZero() {
		return DeviceEventRecordResult{}, errors.New("record device event: clock returned zero time")
	}
	if _, insertErr := queries.InsertDeviceEvent(ctx, dbsqlc.InsertDeviceEventParams{
		EventID:       string(params.Event.ID),
		AdapterID:     params.AdapterID,
		RuntimeID:     string(params.RuntimeID),
		EntityID:      string(params.Event.EntityID),
		CorrelationID: string(params.Event.CorrelationID),
		Name:          string(params.Event.Name),
		Fingerprint:   fingerprint,
		Disposition:   string(disposition),
		RejectionCode: nullableDeviceEventRejection(rejection),
		// Fixed-width observed encodings keep the retention cutoff
		// lexicographically comparable and preserve receive order.
		EmittedAt:  formatSortableTime(params.Event.EmittedAt),
		ReceivedAt: formatSortableTime(params.ReceivedAt),
		RecordedAt: formatSortableTime(recordedAt),
	}); insertErr != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("insert device event: %w", insertErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return DeviceEventRecordResult{}, fmt.Errorf("commit device event recording: %w", commitErr)
	}
	return DeviceEventRecordResult{Outcome: outcomeForDisposition(disposition), Rejection: rejection}, nil
}

// classifyDeviceEvent applies the processing-time checks in their fixed
// precedence: runtime fencing, Entity existence, ownership, enablement, and
// current event support. Adapter health and Entity availability never appear
// here: historical input is not gated on either. A corrupt persisted
// descriptor or an unknown type is an infrastructure failure, not a rejection.
func (repository *SQLiteRepository) classifyDeviceEvent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params RecordDeviceEventParams,
) (DeviceEventDisposition, *DeviceEventRejection, error) {
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: params.AdapterID, RuntimeID: string(params.RuntimeID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return rejectedDeviceEvent(DeviceEventRejectionStaleRuntime)
	}
	if err != nil {
		return "", nil, fmt.Errorf("validate device event runtime: %w", err)
	}

	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.Event.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return rejectedDeviceEvent(DeviceEventRejectionUnknownEntity)
	}
	if err != nil {
		return "", nil, fmt.Errorf("get entity for device event: %w", err)
	}
	entity := Entity{
		ID: EntityID(row.ID), DeviceID: DeviceID(row.DeviceID), AdapterID: row.AdapterID,
		Name: row.Name, TypeID: EntityTypeID(row.TypeID), Support: EntitySupport(row.SupportJson),
		Enabled: row.Enabled != 0,
	}
	if entity.AdapterID != params.AdapterID {
		return rejectedDeviceEvent(DeviceEventRejectionWrongAdapter)
	}
	if !entity.Enabled {
		// A disabled event source has no Command-linked exception.
		return rejectedDeviceEvent(DeviceEventRejectionEntityDisabled)
	}
	supported, err := repository.catalog.SupportsDeviceEvent(entity, params.Event.Name)
	if err != nil {
		return "", nil, fmt.Errorf("validate entity event support: %w", err)
	}
	if !supported {
		return rejectedDeviceEvent(DeviceEventRejectionUnsupportedEvent)
	}
	return DeviceEventDispositionAccepted, nil, nil
}

func rejectedDeviceEvent(rejection DeviceEventRejection) (DeviceEventDisposition, *DeviceEventRejection, error) {
	code := rejection
	return DeviceEventDispositionRejected, &code, nil
}

func outcomeForDisposition(disposition DeviceEventDisposition) DeviceEventRecordOutcome {
	if disposition == DeviceEventDispositionRejected {
		return DeviceEventOutcomeRejected
	}
	return DeviceEventOutcomeAccepted
}

// deviceEventFingerprint returns the raw 32-byte SHA-256 of the canonical
// immutable-input tuple. Array order is fixed and the encoding has no extra
// whitespace, so the same reported tuple always hashes the same way.
func deviceEventFingerprint(params RecordDeviceEventParams) ([]byte, error) {
	tuple, err := json.Marshal([]string{
		params.AdapterID,
		string(params.RuntimeID),
		string(params.Event.EntityID),
		string(params.Event.Name),
		params.Event.EmittedAt.UTC().Format(time.RFC3339Nano),
		string(params.Event.CorrelationID),
	})
	if err != nil {
		return nil, fmt.Errorf("encode device event fingerprint inputs: %w", err)
	}
	sum := sha256.Sum256(tuple)
	return sum[:], nil
}

func nullableDeviceEventRejection(value *DeviceEventRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

// ListEntityDeviceEvents returns one keyset page of an Entity's retained
// Device Events, newest-first by receive order. The caller validates the
// parent Entity and pagination; the adapter owns query selection, limit+1
// truncation, row mapping, and error wrapping.
func (repository *SQLiteRepository) ListEntityDeviceEvents(
	ctx context.Context,
	params ListEntityDeviceEventsParams,
) (Page[DeviceEventHistoryEntry], error) {
	if !validPageLimit(params.Limit) {
		return Page[DeviceEventHistoryEntry]{}, ErrInvalidPage
	}
	limit := int64(params.Limit + 1)
	entityID := string(params.EntityID)
	queries := repository.queries
	var rows []deviceEventHistoryRow
	if params.BeforeReceiveOrder == nil {
		firstPage, err := queries.ListEntityDeviceEventsFirstPage(
			ctx,
			dbsqlc.ListEntityDeviceEventsFirstPageParams{EntityID: entityID, Limit: limit},
		)
		if err != nil {
			return Page[DeviceEventHistoryEntry]{}, fmt.Errorf("list entity Device Events: %w", err)
		}
		rows = make([]deviceEventHistoryRow, len(firstPage))
		for index, row := range firstPage {
			rows[index] = deviceEventHistoryRowFromFirstPage(row)
		}
	} else {
		after, err := queries.ListEntityDeviceEventsAfter(ctx, dbsqlc.ListEntityDeviceEventsAfterParams{
			EntityID: entityID, ReceiveOrder: *params.BeforeReceiveOrder, Limit: limit,
		})
		if err != nil {
			return Page[DeviceEventHistoryEntry]{}, fmt.Errorf("list entity Device Events: %w", err)
		}
		rows = make([]deviceEventHistoryRow, len(after))
		for index, row := range after {
			rows[index] = deviceEventHistoryRowFromAfter(row)
		}
	}
	items := make([]DeviceEventHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry, mappingErr := deviceEventHistoryEntryFromRow(row)
		if mappingErr != nil {
			return Page[DeviceEventHistoryEntry]{}, fmt.Errorf("map entity Device Events: %w", mappingErr)
		}
		items = append(items, entry)
	}
	return pageFromExtra(items, params.Limit), nil
}

// DeleteDeviceEventsBefore deletes at most batchSize retained Device Events
// recorded strictly before cutoff, oldest first, and returns how many rows it
// removed. One call is one transaction; the caller sweeps until a batch is
// short or its context ends.
func (repository *SQLiteRepository) DeleteDeviceEventsBefore(
	ctx context.Context,
	cutoff time.Time,
	batchSize int,
) (int64, error) {
	if cutoff.IsZero() {
		return 0, errors.New("device event retention cutoff is required")
	}
	if batchSize < 1 {
		return 0, errors.New("device event retention batch size must be positive")
	}
	deleted, err := repository.queries.DeleteDeviceEventsBefore(ctx, dbsqlc.DeleteDeviceEventsBeforeParams{
		RecordedAt: formatSortableTime(cutoff),
		BatchSize:  int64(batchSize),
	})
	if err != nil {
		return 0, fmt.Errorf("delete retained device events: %w", err)
	}
	return deleted, nil
}

type deviceEventHistoryRow struct {
	EventID       string
	EntityID      string
	Name          string
	Disposition   string
	RejectionCode sql.NullString
	EmittedAt     string
	ReceivedAt    string
	RecordedAt    string
	ReceiveOrder  int64
}

func deviceEventHistoryRowFromFirstPage(
	row dbsqlc.ListEntityDeviceEventsFirstPageRow,
) deviceEventHistoryRow {
	return deviceEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func deviceEventHistoryRowFromAfter(row dbsqlc.ListEntityDeviceEventsAfterRow) deviceEventHistoryRow {
	return deviceEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func deviceEventHistoryEntryFromRow(row deviceEventHistoryRow) (DeviceEventHistoryEntry, error) {
	disposition := DeviceEventDisposition(row.Disposition)
	var rejection *DeviceEventRejection
	if row.RejectionCode.Valid {
		code := DeviceEventRejection(row.RejectionCode.String)
		rejection = &code
	}
	switch disposition {
	case DeviceEventDispositionAccepted:
		if rejection != nil {
			return DeviceEventHistoryEntry{}, fmt.Errorf(
				"accepted Device Event row %q carries a rejection code", row.EventID,
			)
		}
	case DeviceEventDispositionRejected:
		if rejection == nil {
			return DeviceEventHistoryEntry{}, fmt.Errorf(
				"rejected Device Event row %q is missing its rejection code", row.EventID,
			)
		}
	default:
		return DeviceEventHistoryEntry{}, fmt.Errorf(
			"Device Event row %q has unknown disposition %q", row.EventID, row.Disposition,
		)
	}
	emittedAt, err := parseTime(row.EmittedAt)
	if err != nil {
		return DeviceEventHistoryEntry{}, fmt.Errorf("parse Device Event emitted_at: %w", err)
	}
	receivedAt, err := parseTime(row.ReceivedAt)
	if err != nil {
		return DeviceEventHistoryEntry{}, fmt.Errorf("parse Device Event received_at: %w", err)
	}
	recordedAt, err := parseTime(row.RecordedAt)
	if err != nil {
		return DeviceEventHistoryEntry{}, fmt.Errorf("parse Device Event recorded_at: %w", err)
	}
	return DeviceEventHistoryEntry{
		EventID: DeviceEventID(row.EventID), EntityID: EntityID(row.EntityID),
		Name: DeviceEventName(row.Name), Disposition: disposition, Rejection: rejection,
		EmittedAt: emittedAt, ReceivedAt: receivedAt, RecordedAt: recordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}, nil
}
