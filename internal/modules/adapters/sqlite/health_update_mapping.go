package sqlite

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

func retainedSnapshot(adapterID string, revision int64, body string) (adapters.HealthSnapshot, []byte, error) {
	snapshot, canonical, err := adapters.CanonicalizeHealthSnapshot([]byte(body))
	if err != nil || snapshot.AdapterID != adapterID || int64(snapshot.Revision) != revision {
		return adapters.HealthSnapshot{}, nil, fmt.Errorf("%w: invalid snapshot", adapters.ErrCorruptHealthEvidence)
	}
	return snapshot, canonical, nil
}

func healthSnapshotFromRow(instance dbsqlc.AdapterInstance) (adapters.HealthSnapshot, error) {
	since, err := parseTime(instance.HealthSince)
	if err != nil {
		return adapters.HealthSnapshot{}, err
	}
	evidence, err := parseTime(instance.HealthEvidenceAt)
	if err != nil {
		return adapters.HealthSnapshot{}, err
	}
	observed, err := parseOptionalTime(instance.HealthSourceObservedAt)
	if err != nil {
		return adapters.HealthSnapshot{}, err
	}
	snapshot := adapters.HealthSnapshot{
		AdapterID:        instance.AdapterID,
		Revision:         1,
		Status:           adapters.AdapterHealthStatus(instance.HealthStatus),
		Source:           instance.HealthSource,
		ChangedAt:        since,
		EvidenceAt:       evidence,
		SourceObservedAt: observed,
		Reason:           healthReasonFromNull(instance.HealthReasonCode),
	}
	if instance.HealthRuntimeID.Valid {
		runtime := adapters.RuntimeID(instance.HealthRuntimeID.String)
		snapshot.RuntimeID = &runtime
	}
	return snapshot, nil
}

func pendingHealthUpdateFromRow(row dbsqlc.ListPendingHealthUpdatesRow) (adapters.PendingHealthUpdate, error) {
	snapshot, _, err := retainedSnapshot(row.AdapterID, row.Revision, row.SnapshotJson)
	if err != nil {
		return adapters.PendingHealthUpdate{}, err
	}
	enqueuedAt, err := parseTime(row.EnqueuedAt)
	if err != nil {
		return adapters.PendingHealthUpdate{}, fmt.Errorf(
			"%w: invalid enqueue time",
			adapters.ErrCorruptHealthEvidence,
		)
	}
	update := adapters.PendingHealthUpdate{
		ID:            adapters.HealthUpdateID(row.MessageID),
		Snapshot:      snapshot,
		EnqueuedAt:    enqueuedAt,
		CorrelationID: row.CorrelationID,
		TraceParent:   row.Traceparent,
		TraceState:    row.Tracestate,
	}
	if row.CausationID.Valid {
		value := row.CausationID.String
		update.CausationID = &value
	}
	if err = adapters.ValidateHealthPublicationMetadata(update); err != nil {
		return adapters.PendingHealthUpdate{}, fmt.Errorf(
			"%w: invalid publication metadata",
			adapters.ErrCorruptHealthEvidence,
		)
	}
	return update, nil
}
