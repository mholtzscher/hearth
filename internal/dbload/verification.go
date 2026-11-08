package dbload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	tableObservations               = "observations"
	tableAutomationHistory          = "automation_history"
	tableAdapterFunctionInvocations = "adapter_function_invocations"
	pruneBatchSize                  = 500
)

type mutationOracle struct {
	table    string
	before   int64
	expected int64
}

func prepareMutationOracle(
	ctx context.Context,
	db *sql.DB,
	m Manifest,
	w Workload,
	samples int,
) (mutationOracle, error) {
	o := mutationOracle{}
	var predicate string
	var args []any
	switch w.Name {
	case "devices.observation.append", "devices.observation.append-numeric":
		o.table = tableObservations
		o.expected = int64(samples)
	case "devices.observations.prune":
		o.table = tableObservations
		predicate = "observed_at < ? AND NOT EXISTS (SELECT 1 FROM entity_states s WHERE s.observation_id=observations.observation_id)"
		args = []any{m.Clock.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05.000000000Z")}
	case "devices.events.prune-batch":
		o.table = "entity_events"
		predicate = "recorded_at < ?"
		args = []any{m.Clock.Format("2006-01-02T15:04:05.000000000Z")}
	case "automations.history.prune-batch":
		o.table = tableAutomationHistory
		predicate = "recorded_at < ? AND (kind='skip' OR run_status <> 'running')"
		args = []any{m.Clock.Format("2006-01-02T15:04:05.000000000Z")}
	case "notifications.history.prune-batch":
		o.table = "notification_attempts"
		predicate = "completed_at < ?"
		args = []any{m.Clock.Format("2006-01-02T15:04:05.000000000Z")}
	case "adapters.functions.prune-batch":
		o.table = tableAdapterFunctionInvocations
		predicate = "completed_at < ?"
		args = []any{m.Clock.Format("2006-01-02T15:04:05.000000000Z")}
	default:
		return o, nil
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+o.table).Scan(&o.before); err != nil {
		return o, err
	}
	if predicate != "" {
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+o.table+" WHERE "+predicate, args...).
			Scan(&o.expected); err != nil {
			return o, err
		}
		if w.Once && !strings.Contains(w.Name, "observations.prune") {
			o.expected = min(o.expected, pruneBatchSize)
		}
	}
	return o, nil
}

func verifyMutation(ctx context.Context, db *sql.DB, o mutationOracle, w Workload) (int64, error) {
	if o.table == "" {
		return 0, nil
	}
	var after int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+o.table).Scan(&after); err != nil {
		return 0, err
	}
	actual := o.before - after
	if !w.Once {
		actual = after - o.before
	}
	if actual != o.expected {
		return actual, fmt.Errorf(
			"durable effect mismatch: %s changed %d rows, expected %d",
			o.table,
			actual,
			o.expected,
		)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return actual, err
	}
	defer rows.Close()
	if rows.Next() {
		return actual, errors.New("foreign-key violation after mutation")
	}
	return actual, rows.Err()
}
