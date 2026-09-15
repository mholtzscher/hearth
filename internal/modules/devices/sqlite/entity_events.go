package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// RecordEntityEvent records one wire-valid Entity Event inside one
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
func (repository *DeviceRepository) RecordEntityEvent(
	ctx context.Context,
	params devices.RecordEntityEventParams,
) (devices.EntityEventRecordResult, error) {
	if repository.catalog == nil {
		return devices.EntityEventRecordResult{}, errors.New("record entity event: entity type catalog is required")
	}
	if params.Now == nil {
		return devices.EntityEventRecordResult{}, errors.New("record entity event: clock is required")
	}
	if params.Event.EmittedAt.IsZero() || params.ReceivedAt.IsZero() {
		return devices.EntityEventRecordResult{}, errors.New("record entity event: report times are required")
	}
	fingerprint, err := entityEventFingerprint(params)
	if err != nil {
		return devices.EntityEventRecordResult{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.EntityEventRecordResult{}, fmt.Errorf("begin entity event recording: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)

	existing, err := queries.GetEntityEvent(ctx, dbsqlc.GetEntityEventParams{EventID: string(params.Event.ID)})
	switch {
	case err == nil:
		// The row exists: the ID already means something in this household.
		if bytes.Equal(existing.Fingerprint, fingerprint) {
			return devices.EntityEventRecordResult{Outcome: devices.EntityEventOutcomeDuplicate}, nil
		}
		return devices.EntityEventRecordResult{Outcome: devices.EntityEventOutcomeIdentityConflict}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return devices.EntityEventRecordResult{}, fmt.Errorf("look up entity event: %w", err)
	default:
	}

	disposition, rejection, err := repository.classifyEntityEvent(ctx, queries, params)
	if err != nil {
		return devices.EntityEventRecordResult{}, err
	}
	recordedAt := params.Now().UTC()
	if recordedAt.IsZero() {
		return devices.EntityEventRecordResult{}, errors.New("record entity event: clock returned zero time")
	}
	if _, insertErr := queries.InsertEntityEvent(ctx, dbsqlc.InsertEntityEventParams{
		EventID:       string(params.Event.ID),
		AdapterID:     params.AdapterID,
		RuntimeID:     string(params.RuntimeID),
		EntityID:      string(params.Event.EntityID),
		CorrelationID: string(params.Event.CorrelationID),
		Name:          string(params.Event.Name),
		Fingerprint:   fingerprint,
		Disposition:   string(disposition),
		RejectionCode: nullableEntityEventRejection(rejection),
		// Fixed-width observed encodings keep the retention cutoff
		// lexicographically comparable and preserve receive order.
		EmittedAt:  formatSortableTime(params.Event.EmittedAt),
		ReceivedAt: formatSortableTime(params.ReceivedAt),
		RecordedAt: formatSortableTime(recordedAt),
	}); insertErr != nil {
		return devices.EntityEventRecordResult{}, fmt.Errorf("insert entity event: %w", insertErr)
	}
	// The device fact is queued inside this transaction, so a recorded accepted
	// event and its pending fact are one atomic unit. created_at reuses the
	// committed recorded_at, so the fact reports exactly Core's record time.
	pendingFactID, err := repository.queueAcceptedEntityEventDeviceFact(
		ctx, queries, params, disposition, recordedAt,
	)
	if err != nil {
		return devices.EntityEventRecordResult{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.EntityEventRecordResult{}, fmt.Errorf("commit entity event recording: %w", commitErr)
	}
	return devices.EntityEventRecordResult{
		Outcome: outcomeForDisposition(disposition), Rejection: rejection, RecordedAt: recordedAt,
		PendingFactID: pendingFactID,
	}, nil
}

// classifyEntityEvent applies the processing-time checks in their fixed
// precedence: runtime fencing, Entity existence, ownership, enablement, and
// current event support. Adapter health and Entity availability never appear
// here: historical input is not gated on either. An unknown Entity Type or a
// persisted descriptor that no longer satisfies its schema returns a
// permanent EntityEventDescriptorError rather than a rejection, because
// redelivering such a report can never succeed. Ordinary storage failures
// return their own errors and stay retryable.
func (repository *DeviceRepository) classifyEntityEvent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RecordEntityEventParams,
) (devices.EntityEventDisposition, *devices.EntityEventRejection, error) {
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: params.AdapterID, RuntimeID: string(params.RuntimeID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return rejectedEntityEvent(devices.EntityEventRejectionStaleRuntime)
	}
	if err != nil {
		return "", nil, fmt.Errorf("validate entity event runtime: %w", err)
	}

	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.Event.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return rejectedEntityEvent(devices.EntityEventRejectionUnknownEntity)
	}
	if err != nil {
		return "", nil, fmt.Errorf("get entity for entity event: %w", err)
	}
	entity := devices.Entity{
		ID: devices.EntityID(row.ID), DeviceID: devices.DeviceID(row.DeviceID), AdapterID: row.AdapterID,
		Name: row.Name, TypeID: devices.EntityTypeID(row.TypeID), Support: devices.EntitySupport(row.SupportJson),
		Enabled: row.Enabled != 0,
	}
	if entity.AdapterID != params.AdapterID {
		return rejectedEntityEvent(devices.EntityEventRejectionWrongAdapter)
	}
	if !entity.Enabled {
		// A disabled event source has no Command-linked exception.
		return rejectedEntityEvent(devices.EntityEventRejectionEntityDisabled)
	}
	supported, err := repository.catalog.SupportsEntityEvent(entity, params.Event.Name)
	if err != nil {
		// Only interpreting the persisted descriptor can fail here: an unknown
		// Entity Type or event-source support that no longer satisfies its
		// schema. Both are deterministic for this row, so the report gets the
		// permanent descriptor class instead of an ordinary retryable error.
		return "", nil, devices.NewEntityEventDescriptorError(entity.ID, entity.TypeID, err)
	}
	if !supported {
		return rejectedEntityEvent(devices.EntityEventRejectionUnsupportedEvent)
	}
	return devices.EntityEventDispositionAccepted, nil, nil
}

func rejectedEntityEvent(
	rejection devices.EntityEventRejection,
) (devices.EntityEventDisposition, *devices.EntityEventRejection, error) {
	code := rejection
	return devices.EntityEventDispositionRejected, &code, nil
}

func outcomeForDisposition(disposition devices.EntityEventDisposition) devices.EntityEventRecordOutcome {
	if disposition == devices.EntityEventDispositionRejected {
		return devices.EntityEventOutcomeRejected
	}
	return devices.EntityEventOutcomeAccepted
}

// entityEventFingerprint returns the raw 32-byte SHA-256 of the canonical
// immutable-input tuple. Array order is fixed and the encoding has no extra
// whitespace, so the same reported tuple always hashes the same way.
func entityEventFingerprint(params devices.RecordEntityEventParams) ([]byte, error) {
	tuple, err := json.Marshal([]string{
		params.AdapterID,
		string(params.RuntimeID),
		string(params.Event.EntityID),
		string(params.Event.Name),
		params.Event.EmittedAt.UTC().Format(time.RFC3339Nano),
		string(params.Event.CorrelationID),
	})
	if err != nil {
		return nil, fmt.Errorf("encode entity event fingerprint inputs: %w", err)
	}
	sum := sha256.Sum256(tuple)
	return sum[:], nil
}

func nullableEntityEventRejection(value *devices.EntityEventRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

// ListEntityEvents returns one keyset page of an Entity's retained
// Entity Events, newest-first by receive order. The caller validates the
// parent Entity and pagination; the adapter owns query selection, limit+1
// truncation, row mapping, and error wrapping.
func (repository *DeviceRepository) ListEntityEvents(
	ctx context.Context,
	params devices.ListEntityEventsParams,
) (devices.Page[devices.EntityEventHistoryEntry], error) {
	if !devices.ValidPageLimit(params.Limit) {
		return devices.Page[devices.EntityEventHistoryEntry]{}, devices.ErrInvalidPage
	}
	limit := int64(params.Limit + 1)
	entityID := string(params.EntityID)
	queries := repository.queries
	var rows []entityEventHistoryRow
	if params.BeforeReceiveOrder == nil {
		firstPage, err := queries.ListEntityEventsFirstPage(
			ctx,
			dbsqlc.ListEntityEventsFirstPageParams{EntityID: entityID, Limit: limit},
		)
		if err != nil {
			return devices.Page[devices.EntityEventHistoryEntry]{}, fmt.Errorf("list Entity Events: %w", err)
		}
		rows = make([]entityEventHistoryRow, len(firstPage))
		for index, row := range firstPage {
			rows[index] = entityEventHistoryRowFromFirstPage(row)
		}
	} else {
		after, err := queries.ListEntityEventsAfter(ctx, dbsqlc.ListEntityEventsAfterParams{
			EntityID: entityID, ReceiveOrder: *params.BeforeReceiveOrder, Limit: limit,
		})
		if err != nil {
			return devices.Page[devices.EntityEventHistoryEntry]{}, fmt.Errorf("list Entity Events: %w", err)
		}
		rows = make([]entityEventHistoryRow, len(after))
		for index, row := range after {
			rows[index] = entityEventHistoryRowFromAfter(row)
		}
	}
	items := make([]devices.EntityEventHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry, mappingErr := entityEventHistoryEntryFromRow(row)
		if mappingErr != nil {
			return devices.Page[devices.EntityEventHistoryEntry]{}, fmt.Errorf("map Entity Events: %w", mappingErr)
		}
		items = append(items, entry)
	}
	return pageFromExtra(items, params.Limit), nil
}

// DeleteEntityEventsBefore deletes at most batchSize retained Entity Events
// recorded strictly before cutoff, oldest first, and returns how many rows it
// removed. One call is one transaction; the caller sweeps until a batch is
// short or its context ends.
func (repository *DeviceRepository) DeleteEntityEventsBefore(
	ctx context.Context,
	cutoff time.Time,
	batchSize int,
) (int64, error) {
	if cutoff.IsZero() {
		return 0, errors.New("entity event retention cutoff is required")
	}
	if batchSize < 1 {
		return 0, errors.New("entity event retention batch size must be positive")
	}
	deleted, err := repository.queries.DeleteEntityEventsBefore(ctx, dbsqlc.DeleteEntityEventsBeforeParams{
		RecordedAt: formatSortableTime(cutoff),
		BatchSize:  int64(batchSize),
	})
	if err != nil {
		return 0, fmt.Errorf("delete retained entity events: %w", err)
	}
	return deleted, nil
}

type entityEventHistoryRow struct {
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

func entityEventHistoryRowFromFirstPage(
	row dbsqlc.ListEntityEventsFirstPageRow,
) entityEventHistoryRow {
	return entityEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func entityEventHistoryRowFromAfter(row dbsqlc.ListEntityEventsAfterRow) entityEventHistoryRow {
	return entityEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func entityEventHistoryEntryFromRow(row entityEventHistoryRow) (devices.EntityEventHistoryEntry, error) {
	disposition := devices.EntityEventDisposition(row.Disposition)
	var rejection *devices.EntityEventRejection
	if row.RejectionCode.Valid {
		code := devices.EntityEventRejection(row.RejectionCode.String)
		rejection = &code
	}
	switch disposition {
	case devices.EntityEventDispositionAccepted:
		if rejection != nil {
			return devices.EntityEventHistoryEntry{}, fmt.Errorf(
				"accepted Entity Event row %q carries a rejection code", row.EventID,
			)
		}
	case devices.EntityEventDispositionRejected:
		if rejection == nil {
			return devices.EntityEventHistoryEntry{}, fmt.Errorf(
				"rejected Entity Event row %q is missing its rejection code", row.EventID,
			)
		}
	default:
		return devices.EntityEventHistoryEntry{}, fmt.Errorf(
			"entity event row %q has unknown disposition %q", row.EventID, row.Disposition,
		)
	}
	emittedAt, err := parseTime(row.EmittedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event emitted_at: %w", err)
	}
	receivedAt, err := parseTime(row.ReceivedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event received_at: %w", err)
	}
	recordedAt, err := parseTime(row.RecordedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event recorded_at: %w", err)
	}
	return devices.EntityEventHistoryEntry{
		EventID: devices.EntityEventID(row.EventID), EntityID: devices.EntityID(row.EntityID),
		Name: devices.EntityEventName(row.Name), Disposition: disposition, Rejection: rejection,
		EmittedAt: emittedAt, ReceivedAt: receivedAt, RecordedAt: recordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}, nil
}
