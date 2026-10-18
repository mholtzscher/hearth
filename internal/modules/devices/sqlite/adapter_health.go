package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

type preparedAdapterHealthUpdate struct {
	update devices.AdapterHealthUpdate
	body   []byte
}

func prepareAdapterHealthUpdate(
	update devices.AdapterHealthUpdate,
	appliedAt time.Time,
) (preparedAdapterHealthUpdate, error) {
	if !validProjectionTime(appliedAt) {
		return preparedAdapterHealthUpdate{}, devices.ErrInvalidAdapterHealthUpdate
	}
	if _, err := adapters.ParseHealthUpdateID(update.MessageID); err != nil {
		return preparedAdapterHealthUpdate{}, fmt.Errorf(
			"%w: notification identity",
			devices.ErrInvalidAdapterHealthUpdate,
		)
	}
	snapshot, body, err := adapters.PrepareHealthSnapshot(update.Snapshot)
	if err != nil {
		return preparedAdapterHealthUpdate{}, fmt.Errorf("%w: snapshot encoding", devices.ErrInvalidAdapterHealthUpdate)
	}
	return preparedAdapterHealthUpdate{
		update: devices.AdapterHealthUpdate{MessageID: update.MessageID, Snapshot: snapshot},
		body:   body,
	}, nil
}

func validProjectionTime(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0 && at.Year() >= 1 && at.Year() <= 9999
}

func (repository *DeviceRepository) ApplyAdapterHealth(
	ctx context.Context,
	update devices.AdapterHealthUpdate,
	appliedAt time.Time,
) (devices.HealthProjectionResult, error) {
	prepared, err := prepareAdapterHealthUpdate(update, appliedAt)
	if err != nil {
		return devices.HealthProjectionResult{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.HealthProjectionResult{}, fmt.Errorf("begin Adapter health projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	previous, previousBody, err := projectedAdapterHealth(ctx, queries, prepared.update.Snapshot.AdapterID)
	if err != nil {
		return devices.HealthProjectionResult{}, err
	}
	if previous != nil {
		if prepared.update.Snapshot.Revision < previous.Revision {
			return devices.HealthProjectionResult{Disposition: devices.HealthProjectionObsolete}, nil
		}
		if prepared.update.Snapshot.Revision == previous.Revision {
			if !bytes.Equal(prepared.body, previousBody) {
				return devices.HealthProjectionResult{}, devices.ErrHealthRevisionConflict
			}
			return devices.HealthProjectionResult{Disposition: devices.HealthProjectionDuplicate}, nil
		}
	}
	snapshot := prepared.update.Snapshot
	if err = queries.UpsertProjectedAdapterHealth(ctx, dbsqlc.UpsertProjectedAdapterHealthParams{
		AdapterID: snapshot.AdapterID, Revision: int64(snapshot.Revision), SnapshotJson: string(prepared.body),
		LastMessageID: prepared.update.MessageID, AppliedAt: formatTime(appliedAt),
	}); err != nil {
		return devices.HealthProjectionResult{}, fmt.Errorf("persist Adapter health projection: %w", err)
	}
	entityIDs, err := queries.ListHealthProjectionEntities(
		ctx,
		dbsqlc.ListHealthProjectionEntitiesParams{AdapterID: snapshot.AdapterID},
	)
	if err != nil {
		return devices.HealthProjectionResult{}, fmt.Errorf("list projected availability owners: %w", err)
	}
	for _, entityID := range entityIDs {
		if _, parseErr := devices.ParseEntityID(entityID); parseErr != nil {
			return devices.HealthProjectionResult{}, corruptProjection("entity identity")
		}
		if err = recomputeEffectiveAvailability(
			ctx,
			queries,
			snapshot.AdapterID,
			devices.EntityID(entityID),
			&snapshot,
			appliedAt,
			prepared.update.MessageID,
		); err != nil {
			return devices.HealthProjectionResult{}, err
		}
	}
	if err = queries.InvalidateOlderAvailabilityReports(ctx, dbsqlc.InvalidateOlderAvailabilityReportsParams{
		AdapterID: snapshot.AdapterID, HealthRevision: int64(snapshot.Revision),
	}); err != nil {
		return devices.HealthProjectionResult{}, fmt.Errorf("invalidate older availability reports: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return devices.HealthProjectionResult{}, fmt.Errorf("commit Adapter health projection: %w", err)
	}
	return devices.HealthProjectionResult{Disposition: devices.HealthProjectionApplied}, nil
}

func projectedAdapterHealth(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
) (*adapters.HealthSnapshot, []byte, error) {
	row, err := queries.GetProjectedAdapterHealth(ctx, dbsqlc.GetProjectedAdapterHealthParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read projected Adapter health: %w", err)
	}
	snapshot, body, err := adapters.CanonicalizeHealthSnapshot([]byte(row.SnapshotJson))
	if err != nil || snapshot.AdapterID != adapterID || int64(snapshot.Revision) != row.Revision {
		return nil, nil, corruptProjection("snapshot")
	}
	if _, err = adapters.ParseHealthUpdateID(row.LastMessageID); err != nil {
		return nil, nil, corruptProjection("notification identity")
	}
	at, err := parseTime(row.AppliedAt)
	if err != nil || !validProjectionTime(at) {
		return nil, nil, corruptProjection("application time")
	}
	return &snapshot, body, nil
}

func corruptProjection(code string) error {
	return fmt.Errorf("%w: %s", devices.ErrCorruptHealthProjection, code)
}

type effectiveAvailability struct {
	status     devices.EntityAvailabilityStatus
	source     string
	reason     sql.NullString
	sourceAt   sql.NullString
	evidenceAt time.Time
}

// The selected read model deliberately never consults authoritative Adapter health.
func effectiveFromEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	entityID devices.EntityID,
	snapshot *adapters.HealthSnapshot,
	appliedAt time.Time,
) (effectiveAvailability, error) {
	value := effectiveAvailability{status: devices.EntityAvailabilityUnknown, source: healthSourceCore,
		reason: nullableText("hearth.awaiting_adapter_projection"), evidenceAt: appliedAt}
	report, err := queries.GetEntityAvailabilityCurrent(
		ctx,
		dbsqlc.GetEntityAvailabilityCurrentParams{EntityID: string(entityID)},
	)
	hasReport := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return effectiveAvailability{}, fmt.Errorf("read availability report: %w", err)
	}
	if hasReport {
		if err = validateRetainedAvailabilityReport(report, adapterID); err != nil {
			return effectiveAvailability{}, err
		}
	}
	if snapshot == nil {
		return value, nil
	}
	value.evidenceAt = snapshot.EvidenceAt
	switch snapshot.Status {
	case adapters.AdapterHealthUnhealthy, adapters.AdapterHealthUnknown:
		value.source = availabilitySourceAdapterHealth
		value.reason = nullableText(snapshot.Reason.Code)
		if snapshot.Status == adapters.AdapterHealthUnhealthy {
			value.status = devices.EntityAvailabilityUnavailable
		}
		if snapshot.SourceObservedAt != nil {
			value.sourceAt = formatNullableTime(*snapshot.SourceObservedAt)
		}
	case adapters.AdapterHealthHealthy:
		if hasReport && report.HealthRevision > int64(snapshot.Revision) {
			return value, nil
		}
		value.reason = nullableText("hearth.awaiting_entity_report")
		if hasReport && report.HealthRevision == int64(snapshot.Revision) &&
			report.RuntimeID == string(*snapshot.RuntimeID) {
			value.status = devices.EntityAvailabilityStatus(report.Status)
			value.source = "entity_report"
			value.reason = report.ReasonCode
			value.sourceAt = nullableText(report.SourceObservedAt)
			value.evidenceAt, _ = parseTime(report.EvidenceAt) // Retained report was validated above.
		}
	}
	return value, nil
}

func validateRetainedAvailabilityReport(report dbsqlc.GetEntityAvailabilityCurrentRow, adapterID string) error {
	if report.AdapterID != adapterID || report.HealthRevision < 0 {
		return corruptProjection("report generation")
	}
	if report.LatestTransitionReceiveOrder < 0 {
		return corruptProjection("report history position")
	}
	if _, err := adapters.ParseRuntimeID(report.RuntimeID); err != nil {
		return corruptProjection("report runtime")
	}
	sourceAt, err := parseTime(report.SourceObservedAt)
	if err != nil || !validProjectionTime(sourceAt) {
		return corruptProjection("report source time")
	}
	for _, value := range []string{report.EvidenceAt, report.CurrentSince} {
		at, parseErr := parseTime(value)
		if parseErr != nil || !validProjectionTime(at) {
			return corruptProjection("report time")
		}
	}
	if err = devices.ValidateEntityAvailabilityReport(devices.EntityAvailabilityReport{
		EntityID: devices.EntityID(report.EntityID), Status: devices.EntityAvailabilityStatus(report.Status),
		SourceObservedAt: sourceAt, Reason: healthReasonFromNull(report.ReasonCode),
	}); err != nil {
		return corruptProjection("report status/reason")
	}
	return nil
}

func recomputeEffectiveAvailability(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	entityID devices.EntityID,
	snapshot *adapters.HealthSnapshot,
	appliedAt time.Time,
	messageID string,
) error {
	value, err := effectiveFromEvidence(ctx, queries, adapterID, entityID, snapshot, appliedAt)
	if err != nil {
		return err
	}
	history, err := effectiveHistoryPosition(ctx, queries, entityID, value, appliedAt)
	if err != nil {
		return err
	}
	if history.changed {
		params := dbsqlc.InsertEffectiveAvailabilityTransitionParams{
			AdapterID:        adapterID,
			EntityID:         nullableText(string(entityID)),
			Status:           string(value.status),
			Source:           value.source,
			ReasonCode:       value.reason,
			SourceObservedAt: value.sourceAt,
			ObservedAt:       formatTime(appliedAt),
		}
		if snapshot != nil && snapshot.RuntimeID != nil {
			params.RuntimeID = nullableText(string(*snapshot.RuntimeID))
		}
		if snapshot != nil && messageID != "" {
			params.SourceHealthRevision = sql.NullInt64{Int64: int64(snapshot.Revision), Valid: true}
			params.SourceHealthUpdateID = nullableText(messageID)
		}
		order, writeErr := queries.InsertEffectiveAvailabilityTransition(ctx, params)
		if writeErr != nil {
			return fmt.Errorf("insert effective availability history: %w", writeErr)
		}
		history.order = sql.NullInt64{Int64: order, Valid: true}
	}
	if err = queries.UpsertEffectiveAvailability(ctx, dbsqlc.UpsertEffectiveAvailabilityParams{
		EntityID:   string(entityID),
		Status:     string(value.status),
		Source:     value.source,
		ReasonCode: value.reason,
		CurrentSince: formatTime(
			history.since,
		),
		EvidenceAt:                   formatTime(value.evidenceAt),
		SourceObservedAt:             value.sourceAt,
		LatestTransitionReceiveOrder: history.order,
	}); err != nil {
		return fmt.Errorf("persist effective availability: %w", err)
	}
	return nil
}

type effectiveHistory struct {
	since   time.Time
	order   sql.NullInt64
	changed bool
}

func effectiveHistoryPosition(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entityID devices.EntityID,
	value effectiveAvailability,
	appliedAt time.Time,
) (effectiveHistory, error) {
	current, err := queries.GetEffectiveAvailability(
		ctx,
		dbsqlc.GetEffectiveAvailabilityParams{EntityID: string(entityID)},
	)
	next := effectiveHistory{since: appliedAt, changed: true}
	if err == nil {
		if err = validateEffectiveAvailability(current); err != nil {
			return effectiveHistory{}, err
		}
		if current.Status != string(value.status) || current.ReasonCode != value.reason {
			return next, nil
		}
		since, _ := parseTime(current.CurrentSince)
		return effectiveHistory{
			since: since,
			order: sql.NullInt64{
				Int64: current.LatestTransitionReceiveOrder,
				Valid: current.LatestTransitionReceiveOrder > 0,
			},
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return effectiveHistory{}, fmt.Errorf("read effective availability: %w", err)
	}
	// Bootstrap reuses equivalent retained history without rewriting its old meaning.
	latest, err := queries.GetLatestEntityAvailabilityTransition(
		ctx,
		dbsqlc.GetLatestEntityAvailabilityTransitionParams{EntityID: nullableText(string(entityID))},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return next, nil
	}
	if err != nil {
		return effectiveHistory{}, fmt.Errorf("read availability bootstrap history: %w", err)
	}
	if latest.Status != string(value.status) || latest.ReasonCode != value.reason {
		return next, nil
	}
	since, err := parseTime(latest.ObservedAt)
	if err != nil || !validProjectionTime(since) {
		return effectiveHistory{}, corruptProjection("retained transition time")
	}
	return effectiveHistory{since: since, order: sql.NullInt64{Int64: latest.ReceiveOrder, Valid: true}}, nil
}

func validateEffectiveAvailability(value dbsqlc.GetEffectiveAvailabilityRow) error {
	if value.Source != "core" && value.Source != "entity_report" && value.Source != "adapter_health" {
		return corruptProjection("effective availability source")
	}
	switch devices.EntityAvailabilityStatus(value.Status) {
	case devices.EntityAvailabilityAvailable:
		if value.ReasonCode.Valid {
			return corruptProjection("effective available reason")
		}
	case devices.EntityAvailabilityUnknown, devices.EntityAvailabilityUnavailable:
		if !value.ReasonCode.Valid ||
			devices.ValidateHealthReason(&devices.HealthReason{Code: value.ReasonCode.String}) != nil {
			return corruptProjection("effective availability reason")
		}
	default:
		return corruptProjection("effective availability status")
	}
	if value.LatestTransitionReceiveOrder < 0 {
		return corruptProjection("effective history position")
	}
	for _, text := range []string{value.CurrentSince, value.EvidenceAt} {
		at, err := parseTime(text)
		if err != nil || !validProjectionTime(at) {
			return corruptProjection("effective availability time")
		}
	}
	if value.SourceObservedAt.Valid {
		at, err := parseTime(value.SourceObservedAt.String)
		if err != nil || !validProjectionTime(at) {
			return corruptProjection("effective source time")
		}
	}
	return nil
}
