package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Persisted effective availability sources remain distinct from the system
// that supplied authoritative Adapter health.
const (
	healthSourceCore                = "core"
	availabilitySourceAdapterHealth = "adapter_health"
)

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
