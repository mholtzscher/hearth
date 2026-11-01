//go:build integration

package sqlite //nolint:testpackage // Query plan and retained-row corruption are persistence concerns.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// Capture the exact SQL and argument order actually issued by the generated query.
type temperatureSeriesPlanDB struct {
	*sql.DB

	query string
	args  []any
}

func (db *temperatureSeriesPlanDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.query, db.args = query, append([]any(nil), args...)
	return db.DB.QueryContext(ctx, query, args...)
}

//nolint:gocognit // Proves the aggregate and query plan together.
func TestTemperatureSeriesRetainedEvidence(
	t *testing.T,
) {
	t.Parallel()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "series.db"))
	svc := openHistoryService(t, db)
	id := registerHistoryEntity(t, svc)
	from := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	params := devices.TemperatureSeriesParams{EntityID: id, From: from, To: from.Add(6 * time.Hour)}
	if _, err := svc.GetTemperatureSeries(t.Context(), params); !errors.Is(err, devices.ErrInvalidTemperatureSeries) {
		t.Fatalf("wrong type: %v", err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE entities SET type_id='hearth.temperature/v1' WHERE id=?`,
		id,
	); err != nil {
		t.Fatal(err)
	}
	insert := func(index int, at time.Time, disposition, value string) {
		t.Helper()
		var rejection, state any = nil, value
		if disposition == "rejected" {
			rejection, state = "invalid_value", nil
		}
		_, err := db.ExecContext(
			t.Context(),
			`INSERT INTO observations(observation_id, adapter_id, entity_id, disposition, rejection_code, state_value_json, adapter_received_at, observed_at) VALUES(?, 'simulator', ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("obs_%d", index),
			id,
			disposition,
			rejection,
			state,
			formatTime(at),
			formatTime(at),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 250 {
		disposition := "applied"
		if i%2 == 0 {
			disposition = "unchanged"
		}
		insert(i, from, disposition, "1000")
	}
	insert(250, from.Add(75*time.Second), "applied", "-1000")
	insert(251, from.Add(75*time.Second), "unchanged", "3000")
	insert(252, from, "rejected", "0")
	insert(253, from.Add(-time.Second), "applied", "42")
	insert(254, params.To, "applied", "42")
	insert(256, from.Add(12*time.Hour), "unchanged", "8000")
	insert(257, from.Add(24*time.Hour), "applied", "9000")
	insert(258, from.Add(75*time.Second-time.Nanosecond), "applied", "1000")
	insert(259, from.Add(-time.Nanosecond), "applied", "2000")
	result, err := svc.GetTemperatureSeries(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Buckets) != 288 || result.Buckets[0].Count != 251 || result.Buckets[1].Count != 2 ||
		*result.Buckets[1].MinMilliCelsius != -1000 ||
		*result.Buckets[1].MaxMilliCelsius != 3000 ||
		*result.Buckets[1].MeanMilliCelsius != 1000 ||
		result.Buckets[2].Count != 0 ||
		result.Buckets[2].MeanMilliCelsius != nil {
		t.Fatalf("unexpected buckets: %+v", result.Buckets[:3])
	}
	params.To = from.Add(24 * time.Hour)
	result, err = svc.GetTemperatureSeries(t.Context(), params)
	if err != nil || result.Buckets[0].Count != 253 ||
		result.Buckets[0].End.Sub(result.Buckets[0].Start) != 300*time.Second {
		t.Fatalf("24h series: %+v %v", result.Buckets[0], err)
	}
	if result.Buckets[144].Count != 1 || *result.Buckets[144].MeanMilliCelsius != 8000 ||
		result.Buckets[287].Count != 0 {
		t.Fatalf("24h boundaries: %+v %+v", result.Buckets[144], result.Buckets[287])
	}
	planDB := &temperatureSeriesPlanDB{DB: db}
	_, err = dbsqlc.New(planDB).GetTemperatureSeries(t.Context(), dbsqlc.GetTemperatureSeriesParams{
		EntityID: string(id), FromTime: formatTime(from), ToTime: formatTime(params.To),
		StartUnix: from.Unix(), BucketSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+planDB.query, planDB.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var a, b, c int
		var detail string
		if scanErr := rows.Scan(&a, &b, &c, &detail); scanErr != nil {
			t.Fatal(scanErr)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !strings.Contains(plan.String(), "observations_temperature_series_idx") {
		t.Fatalf("query plan did not use partial index: %s", plan.String())
	}
	t.Logf("dense query plan: %s", plan.String())
	insert(255, from.Add(time.Second), "applied", "1.0")
	if _, corruptErr := svc.GetTemperatureSeries(t.Context(), params); corruptErr == nil {
		t.Fatal("invalid retained numeric representation accepted")
	}
}
