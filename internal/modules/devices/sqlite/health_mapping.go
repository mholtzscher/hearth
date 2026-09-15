package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Persisted evidence tokens. healthSourceCore and healthSourceAdapter name the
// system that supplied current Adapter health, availabilitySourceAdapterHealth
// names health-implied Entity availability, and healthResourceAdapter is the
// health_transitions resource kind for Adapter rows. They are stored as-is and
// read back into domain values, so no domain rule branches on them.
const (
	healthSourceCore                = "core"
	healthSourceAdapter             = "adapter"
	availabilitySourceAdapterHealth = "adapter_health"
	healthResourceAdapter           = "adapter"
)

type sqliteAdapterView struct {
	adapterID              string
	healthStatus           string
	healthReasonCode       sql.NullString
	healthSource           string
	healthSince            string
	healthEvidenceAt       string
	healthSourceObservedAt sql.NullString
	runtimeID              sql.NullString
	softwareName           sql.NullString
	softwareVersion        sql.NullString
	claimedAt              sql.NullString
	lastHeartbeatAt        sql.NullString
	leaseExpiresAt         sql.NullString
	endedAt                sql.NullString
}

func adapterInstanceFromView(view sqliteAdapterView) (devices.AdapterInstance, error) {
	since, err := parseTime(view.healthSince)
	if err != nil {
		return devices.AdapterInstance{}, fmt.Errorf("parse Adapter health since: %w", err)
	}
	evidenceAt, err := parseTime(view.healthEvidenceAt)
	if err != nil {
		return devices.AdapterInstance{}, fmt.Errorf("parse Adapter health evidence: %w", err)
	}
	sourceObservedAt, err := parseOptionalTime(view.healthSourceObservedAt)
	if err != nil {
		return devices.AdapterInstance{}, fmt.Errorf("parse Adapter health source observation time: %w", err)
	}
	instance := devices.AdapterInstance{ID: view.adapterID, Health: devices.AdapterHealth{
		Status: devices.AdapterHealthStatus(view.healthStatus), Source: view.healthSource,
		Since: since, EvidenceAt: evidenceAt, SourceObservedAt: sourceObservedAt,
		Reason: healthReasonFromNull(view.healthReasonCode),
	}}
	if view.runtimeID.Valid {
		claimed, parseErr := parseRequiredTime(view.claimedAt, "Adapter runtime claimed_at")
		if parseErr != nil {
			return devices.AdapterInstance{}, parseErr
		}
		lease, parseErr := parseRequiredTime(view.leaseExpiresAt, "Adapter runtime lease_expires_at")
		if parseErr != nil {
			return devices.AdapterInstance{}, parseErr
		}
		lastHeartbeat, parseErr := parseOptionalTime(view.lastHeartbeatAt)
		if parseErr != nil {
			return devices.AdapterInstance{}, fmt.Errorf("parse Adapter runtime last_heartbeat_at: %w", parseErr)
		}
		status := devices.RuntimeStatusOnline
		if view.endedAt.Valid {
			status = devices.RuntimeStatusOffline
		}
		instance.Health.Runtime = &devices.RuntimeEvidence{
			ID: devices.RuntimeID(view.runtimeID.String), Status: status, SoftwareName: view.softwareName.String,
			SoftwareVersion: view.softwareVersion.String, ClaimedAt: claimed, LastHeartbeatAt: lastHeartbeat,
			LeaseExpiresAt: lease,
		}
	}
	return instance, nil
}

type sqliteTransition struct {
	receiveOrder     int64
	status           string
	source           string
	reasonCode       sql.NullString
	sourceObservedAt sql.NullString
	observedAt       string
}

func newSQLiteTransition(
	receiveOrder int64,
	status string,
	source string,
	reasonCode sql.NullString,
	sourceObservedAt sql.NullString,
	observedAt string,
) sqliteTransition {
	return sqliteTransition{
		receiveOrder: receiveOrder, status: status, source: source,
		reasonCode: reasonCode, sourceObservedAt: sourceObservedAt, observedAt: observedAt,
	}
}

func healthTransitionsFromValues(values []sqliteTransition) ([]devices.HealthTransition, error) {
	transitions := make([]devices.HealthTransition, len(values))
	for index, value := range values {
		transition, err := healthTransitionFromValues(
			value.receiveOrder, value.status, value.source, value.reasonCode,
			value.sourceObservedAt, value.observedAt,
		)
		if err != nil {
			return nil, err
		}
		transitions[index] = transition
	}
	return transitions, nil
}

func healthTransitionFromValues(
	receiveOrder int64,
	status, source string,
	reasonCode, sourceObservedAt sql.NullString,
	observedAt string,
) (devices.HealthTransition, error) {
	observed, err := parseTime(observedAt)
	if err != nil {
		return devices.HealthTransition{}, fmt.Errorf("parse health transition observed_at: %w", err)
	}
	sourceObserved, err := parseOptionalTime(sourceObservedAt)
	if err != nil {
		return devices.HealthTransition{}, fmt.Errorf("parse health transition source_observed_at: %w", err)
	}
	return devices.HealthTransition{
		ReceiveOrder: receiveOrder, Status: status, Source: source,
		Reason: healthReasonFromNull(reasonCode), SourceObservedAt: sourceObserved,
		ObservedAt: observed,
	}, nil
}

func trimTransitionPage(transitions []devices.HealthTransition, limit int) devices.Page[devices.HealthTransition] {
	page := devices.Page[devices.HealthTransition]{Items: transitions, HasMore: len(transitions) > limit}
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	return page
}

func nullableReasonCode(reason *devices.HealthReason) sql.NullString {
	if reason == nil {
		return sql.NullString{}
	}
	return nullableText(reason.Code)
}

func healthReasonCode(reason *devices.HealthReason) string {
	if reason == nil {
		return ""
	}
	return reason.Code
}

func healthReasonFromNull(code sql.NullString) *devices.HealthReason {
	if !code.Valid {
		return nil
	}
	return &devices.HealthReason{Code: code.String}
}
