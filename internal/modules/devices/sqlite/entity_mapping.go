package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

type sqliteEntityAvailability struct {
	entityCreatedAt    string
	adapterStatus      sql.NullString
	adapterReasonCode  sql.NullString
	adapterSince       sql.NullString
	adapterEvidenceAt  sql.NullString
	adapterSourceAt    sql.NullString
	reportedStatus     sql.NullString
	reportedReasonCode sql.NullString
	reportedSourceAt   sql.NullString
	reportedEvidenceAt sql.NullString
	reportedSince      sql.NullString
}

func entityWithStateFromRow(row dbsqlc.EntityReadProjection) (devices.EntityWithState, error) {
	availability, err := entityAvailabilityFromValues(sqliteEntityAvailability{
		entityCreatedAt: row.EntityCreatedAt,
		adapterStatus:   row.AdapterHealthStatus, adapterReasonCode: row.AdapterHealthReasonCode,
		adapterSince: row.AdapterHealthSince, adapterEvidenceAt: row.AdapterHealthEvidenceAt,
		adapterSourceAt:    row.AdapterHealthSourceObservedAt,
		reportedStatus:     row.ReportedAvailabilityStatus,
		reportedReasonCode: row.ReportedAvailabilityReasonCode,
		reportedSourceAt:   row.ReportedAvailabilitySourceObservedAt,
		reportedEvidenceAt: row.ReportedAvailabilityEvidenceAt,
		reportedSince:      row.ReportedAvailabilitySince,
	})
	if err != nil {
		return devices.EntityWithState{}, err
	}
	view := devices.EntityWithState{
		Entity: devices.Entity{
			ID: devices.EntityID(row.ID), DeviceID: devices.DeviceID(row.DeviceID), AdapterID: row.AdapterID,
			Name: row.Name, TypeID: devices.EntityTypeID(row.TypeID), Support: devices.EntitySupport(row.SupportJson),
			Enabled: row.Enabled != 0,
		},
		Availability: availability,
	}
	if !row.ObservationID.Valid {
		return view, nil
	}
	if !row.ValueJson.Valid || !row.AdapterReceivedAt.Valid || !row.ObservedAt.Valid || !row.ReceiveOrder.Valid {
		return devices.EntityWithState{}, errors.New("entity state row is incomplete")
	}
	adapterTime, err := parseTime(row.AdapterReceivedAt.String)
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("parse state adapter_received_at: %w", err)
	}
	observedTime, err := parseTime(row.ObservedAt.String)
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("parse state observed_at: %w", err)
	}
	sourceTime, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("parse state source_updated_at: %w", err)
	}
	view.State = &devices.State{
		EntityID: devices.EntityID(row.ID), Value: devices.Value(row.ValueJson.String),
		ObservationID: devices.ObservationID(row.ObservationID.String), AdapterReceivedAt: adapterTime,
		SourceUpdatedAt: sourceTime, ObservedAt: observedTime, ReceiveOrder: row.ReceiveOrder.Int64,
	}
	return view, nil
}

func entityAvailabilityFromValues(values sqliteEntityAvailability) (devices.EntityAvailability, error) {
	if !values.adapterStatus.Valid {
		observedAt, err := parseTime(values.entityCreatedAt)
		if err != nil {
			return devices.EntityAvailability{}, fmt.Errorf("parse Entity creation time for availability: %w", err)
		}
		return devices.EntityAvailability{
			Status:     devices.EntityAvailabilityUnknown,
			Source:     healthSourceCore,
			Since:      observedAt,
			EvidenceAt: observedAt,
			Reason:     &devices.HealthReason{Code: "hearth.awaiting_runtime"},
		}, nil
	}

	adapterStatus := devices.AdapterHealthStatus(values.adapterStatus.String)
	if adapterStatus == devices.AdapterHealthHealthy && values.reportedStatus.Valid {
		since, evidenceAt, sourceObservedAt, err := parseAvailabilityTimes(
			values.reportedSince, values.reportedEvidenceAt, values.reportedSourceAt,
		)
		if err != nil {
			return devices.EntityAvailability{}, err
		}
		return devices.EntityAvailability{
			Status: devices.EntityAvailabilityStatus(values.reportedStatus.String), Source: "entity_report",
			Since: since, EvidenceAt: evidenceAt, SourceObservedAt: sourceObservedAt,
			Reason: healthReasonFromNull(values.reportedReasonCode),
		}, nil
	}

	since, err := parseRequiredTime(values.adapterSince, "Adapter health since for Entity availability")
	if err != nil {
		return devices.EntityAvailability{}, err
	}
	evidenceAt, err := parseRequiredTime(values.adapterEvidenceAt, "Adapter health evidence for Entity availability")
	if err != nil {
		return devices.EntityAvailability{}, err
	}
	createdAt, err := parseTime(values.entityCreatedAt)
	if err != nil {
		return devices.EntityAvailability{}, fmt.Errorf("parse Entity creation time for availability: %w", err)
	}
	if since.Before(createdAt) {
		since = createdAt
	}
	if evidenceAt.Before(createdAt) {
		evidenceAt = createdAt
	}
	if adapterStatus == devices.AdapterHealthHealthy {
		return devices.EntityAvailability{
			Status: devices.EntityAvailabilityUnknown, Source: healthSourceCore, Since: since, EvidenceAt: evidenceAt,
			Reason: &devices.HealthReason{Code: "hearth.awaiting_entity_report"},
		}, nil
	}
	status := devices.EntityAvailabilityUnknown
	if adapterStatus == devices.AdapterHealthUnhealthy {
		status = devices.EntityAvailabilityUnavailable
	}
	sourceObservedAt, err := parseOptionalTime(values.adapterSourceAt)
	if err != nil {
		return devices.EntityAvailability{}, fmt.Errorf("parse Adapter source time for Entity availability: %w", err)
	}
	return devices.EntityAvailability{
		Status: status, Source: availabilitySourceAdapterHealth, Since: since, EvidenceAt: evidenceAt,
		SourceObservedAt: sourceObservedAt, Reason: healthReasonFromNull(values.adapterReasonCode),
	}, nil
}

func parseAvailabilityTimes(
	sinceValue, evidenceValue, sourceValue sql.NullString,
) (time.Time, time.Time, *time.Time, error) {
	since, err := parseRequiredTime(sinceValue, "Entity availability since")
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	evidenceAt, err := parseRequiredTime(evidenceValue, "Entity availability evidence")
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	sourceObservedAt, err := parseOptionalTime(sourceValue)
	if err != nil {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("parse Entity availability source time: %w", err)
	}
	return since, evidenceAt, sourceObservedAt, nil
}
