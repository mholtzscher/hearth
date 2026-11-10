//go:build integration

package db //nolint:testpackage // Migration tests inspect the migration transaction.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

const (
	forecastStamp          = "2026-11-10T00:00:00.000000000Z"
	legacyForecast         = `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","temperature_c":1.2300e+1}]}`
	legacyDaily            = `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10","temperature_min_c":-2}]}`
	neutralForecastFixture = ` { "periods": [ {"temperature": 3} ] } `
	hourEntity             = "ent_11111111-1111-1111-1111-111111111111"
)

func forecastMigrationDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "forecast.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider, err := NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 31); err != nil {
		t.Fatal(err)
	}
	forecastExec(
		t,
		database,
		`INSERT INTO devices (id,kind,name,created_at,updated_at) VALUES ('dev_one','weather','Weather',?,?)`,
		forecastStamp,
		forecastStamp,
	)
	return database, ctx
}

func forecastExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func forecastEntity(t *testing.T, database *sql.DB, id, typeID string) {
	t.Helper()
	forecastExec(
		t,
		database,
		`INSERT INTO entities (id,device_id,name,type_id,support_json,created_at,updated_at) VALUES (?,'dev_one','Forecast',?,'{}',?,?)`,
		id,
		typeID,
		forecastStamp,
		forecastStamp,
	)
}

func forecastObservation(t *testing.T, database *sql.DB, order int, id, entity, value string, current bool) {
	t.Helper()
	forecastExec(
		t,
		database,
		`INSERT INTO observations (receive_order,observation_id,adapter_id,entity_id,disposition,state_value_json,adapter_received_at,observed_at) VALUES (?,?,'weather',?,'applied',?,?,?)`,
		order,
		id,
		entity,
		value,
		forecastStamp,
		forecastStamp,
	)
	if current {
		forecastExec(
			t,
			database,
			`INSERT INTO entity_states (entity_id,observation_id,value_json,adapter_received_at,observed_at,receive_order) VALUES (?,?,?,?,?,?)`,
			entity,
			id,
			value,
			forecastStamp,
			forecastStamp,
			order,
		)
	}
}

func forecastRead(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()
	var result string
	if err := database.QueryRow(query, args...).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestForecastNeutralMigrationRetainsEvidence(t *testing.T) {
	t.Parallel()
	database, ctx := forecastMigrationDatabase(t)
	forecastEntity(t, database, hourEntity, "hearth.hourlyforecast/v1")
	forecastEntity(t, database, "ent_day", "hearth.dailyforecast/v1")
	forecastEntity(t, database, "ent_neutral", "hearth.hourlyforecast/v1")
	forecastEntity(t, database, "ent_other", "hearth.temperature/v1")
	forecastObservation(t, database, 1, "obs_old", hourEntity, legacyForecast, false)
	forecastObservation(t, database, 2, "obs_current", hourEntity, legacyForecast, true)
	forecastObservation(t, database, 3, "obs_daily", "ent_day", legacyDaily, true)
	forecastObservation(t, database, 4, "obs_neutral", "ent_neutral", neutralForecastFixture, true)
	forecastObservation(t, database, 5, "obs_other", "ent_other", legacyForecast, true)
	// The pending fact refers to a different Observation than current State.
	source := "obs_" + strings.Repeat("1", 36)
	forecastObservation(t, database, 6, source, hourEntity, legacyDaily, false)
	fact := "fct_" + strings.Repeat("2", 36)
	correlation := "cor_" + strings.Repeat("3", 36)
	forecastExec(
		t,
		database,
		`INSERT INTO device_facts_outbox (fact_id,family,entity_id,variant,source_id,correlation_id,created_at,traceparent,tracestate,value_json,previous_value_json,adapter_received_at,observed_at) VALUES (?,'observation',?,'applied',?,?,?,'','',?,?,?,?)`,
		fact,
		hourEntity,
		source,
		correlation,
		forecastStamp,
		legacyDaily,
		legacyForecast,
		forecastStamp,
		forecastStamp,
	)
	provider, err := NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	hour, err := neutralForecast(legacyForecast)
	if err != nil {
		t.Fatal(err)
	}
	day, err := neutralForecast(legacyDaily)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ query, id, want string }{
		{`SELECT value_json FROM entity_states WHERE entity_id=?`, hourEntity, hour},
		{`SELECT state_value_json FROM observations WHERE observation_id=?`, "obs_current", hour},
		{`SELECT value_json FROM entity_states WHERE entity_id=?`, "ent_day", day},
		{`SELECT state_value_json FROM observations WHERE observation_id=?`, "obs_daily", day},
		{`SELECT state_value_json FROM observations WHERE observation_id=?`, source, day},
		{`SELECT value_json FROM device_facts_outbox WHERE fact_id=?`, fact, day},
		{`SELECT previous_value_json FROM device_facts_outbox WHERE fact_id=?`, fact, hour},
		{`SELECT state_value_json FROM observations WHERE observation_id=?`, "obs_old", legacyForecast},
		{`SELECT value_json FROM entity_states WHERE entity_id=?`, "ent_other", legacyForecast},
		{`SELECT value_json FROM entity_states WHERE entity_id=?`, "ent_neutral", neutralForecastFixture},
		{`SELECT state_value_json FROM observations WHERE observation_id=?`, "obs_neutral", neutralForecastFixture},
		{`SELECT source_id FROM device_facts_outbox WHERE fact_id=?`, fact, source},
		{`SELECT created_at FROM device_facts_outbox WHERE fact_id=?`, fact, forecastStamp},
	} {
		if got := forecastRead(t, database, check.query, check.id); got != check.want {
			t.Errorf("%s %s = %q, want %q", check.query, check.id, got, check.want)
		}
	}
	var violations int
	if err = database.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).
		Scan(&violations); err != nil ||
		violations != 0 {
		t.Fatalf("foreign key violations = %d, %v", violations, err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "pre-upgrade backup") {
		t.Fatalf("Down = %v, want backup guard", err)
	}
}

//nolint:gocognit // Each case verifies the whole rollback.
func TestForecastNeutralMigrationFailureRollsBack(
	t *testing.T,
) {
	t.Parallel()
	for _, bad := range []string{`{"periods":[{"temperature_c":1,"temperature":2}]}`, "mismatch"} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			database, ctx := forecastMigrationDatabase(t)
			forecastEntity(t, database, "ent_good", "hearth.hourlyforecast/v1")
			forecastEntity(t, database, "ent_bad", "hearth.dailyforecast/v1")
			forecastObservation(t, database, 1, "obs_good", "ent_good", legacyForecast, true)
			value := bad
			if bad == "mismatch" {
				value = legacyDaily
			}
			forecastObservation(t, database, 2, "obs_bad", "ent_bad", value, true)
			if bad == "mismatch" {
				forecastExec(
					t,
					database,
					`UPDATE observations SET state_value_json=? WHERE observation_id='obs_bad'`,
					legacyForecast,
				)
			}
			provider, err := NewMigrationProvider(database)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.UpTo(ctx, 32); err == nil {
				t.Fatal("invalid forecast migrated")
			}
			if got := forecastRead(
				t,
				database,
				`SELECT value_json FROM entity_states WHERE entity_id=?`,
				"ent_good",
			); got != legacyForecast {
				t.Fatalf("failed migration changed earlier state: %s", got)
			}
			if got := forecastRead(
				t,
				database,
				`SELECT state_value_json FROM observations WHERE observation_id=?`,
				"obs_good",
			); got != legacyForecast {
				t.Fatalf("failed migration changed earlier observation: %s", got)
			}
			version, err := provider.GetDBVersion(ctx)
			if err != nil || version != 31 {
				t.Fatalf("version = %d, %v; want 31", version, err)
			}
		})
	}
}
