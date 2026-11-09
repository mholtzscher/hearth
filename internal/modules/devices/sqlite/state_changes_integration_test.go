//go:build integration

package sqlite //nolint:testpackage // Exercise State change recording at the SQLite boundary.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type storedStateChange struct {
	entity, field, observedAt, value string
	receiveOrder                     int64
}

func storedStateChanges(t *testing.T, database *sql.DB) []storedStateChange {
	t.Helper()
	rows, err := database.Query(`SELECT entity_id, field_path, observed_at, receive_order, value_json
		FROM state_changes ORDER BY entity_id, field_path, observed_at, receive_order`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var changes []storedStateChange
	for rows.Next() {
		var change storedStateChange
		if err = rows.Scan(
			&change.entity,
			&change.field,
			&change.observedAt,
			&change.receiveOrder,
			&change.value,
		); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return changes
}

// changeSummary drops receive order, which the assertions do not depend on.
func changeSummary(changes []storedStateChange) []string {
	rendered := make([]string, len(changes))
	for i, change := range changes {
		rendered[i] = fmt.Sprintf("%s %q %s=%s", change.entity, change.field, change.observedAt[11:19], change.value)
	}
	return rendered
}

func TestStateChangesRecordOnlyChangedYesNoAndTextFields(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	seedRollupReports(t, service, entities, base)
	// A brightness-only style change: power stays true, so nothing is recorded.
	projectRollupReport(t, service, entities.power, `true`, base.Add(80*time.Second))
	projectRollupReport(t, service, entities.power, `false`, base.Add(90*time.Second))
	// A duplicate Observation returns before its insert and records nothing.
	observation := newObservation(t, entities.power, `true`, base.Add(100*time.Second))
	for range 2 {
		if _, err := service.ProjectObservation(
			context.Background(), "simulator", testRuntimeID, observation, base.Add(100*time.Second),
		); err != nil {
			t.Fatal(err)
		}
	}
	setting, color, power := string(entities.setting), string(entities.color), string(entities.power)
	want := []string{
		color + ` "/active" 12:00:05=true`,
		power + ` "" 12:00:05=true`,
		power + ` "" 12:01:30=false`,
		power + ` "" 12:01:40=true`,
		// The first report records both fields; a field absent from State is a known null.
		setting + ` "/choice" 12:00:00=null`,
		setting + ` "/choice" 12:00:15="previous"`,
		setting + ` "/choice" 12:00:30=null`,
		setting + ` "/mode" 12:00:00="value"`,
		setting + ` "/mode" 12:00:15="choice"`,
		setting + ` "/mode" 12:00:30="value"`,
	}
	got := changeSummary(storedStateChanges(t, database))
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("changes = %q\nwant      %q", got, want)
	}
}

func TestStateChangeFailureRollsBackProjection(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projectRollupReport(t, service, entities.power, `true`, base)
	counts := func() string {
		var observations, facts, changes int
		var state string
		if err := database.QueryRow(`SELECT (SELECT count(*) FROM observations),
			(SELECT count(*) FROM device_facts_outbox), (SELECT count(*) FROM state_changes),
			(SELECT value_json FROM entity_states WHERE entity_id = ?)`, entities.power,
		).Scan(&observations, &facts, &changes, &state); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("observations=%d facts=%d changes=%d state=%s", observations, facts, changes, state)
	}
	before := counts()
	if _, err := database.Exec(`CREATE TRIGGER fail_changes BEFORE INSERT ON state_changes
		BEGIN SELECT RAISE(ABORT, 'forced state change failure'); END`); err != nil {
		t.Fatal(err)
	}
	observation := newObservation(t, entities.power, `false`, base.Add(time.Minute))
	if _, err := service.ProjectObservation(
		context.Background(), "simulator", testRuntimeID, observation, base.Add(time.Minute),
	); err == nil {
		t.Fatal("projection should fail when its state change insert fails")
	}
	if after := counts(); after != before {
		t.Fatalf("failed projection left changes: before %s, after %s", before, after)
	}
}

func TestStateChangeBackfillMatchesIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	seedRollupReports(t, service, entities, base)
	for i, value := range []string{`false`, `false`, `true`, `"on"`, `true`, `false`} {
		projectRollupReport(t, service, entities.power, value, base.Add(time.Duration(2+i)*time.Minute))
	}
	ingested := storedStateChanges(t, database)
	if len(ingested) == 0 {
		t.Fatal("ingest recorded no changes")
	}

	provider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	var table int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'state_changes'`).
		Scan(&table); err != nil ||
		table != 0 {
		t.Fatalf("Down kept the state_changes table: %d, %v", table, err)
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if backfilled := storedStateChanges(t, database); !slices.Equal(backfilled, ingested) {
		t.Fatalf("backfilled changes = %+v\ningested changes = %+v", backfilled, ingested)
	}
}

func TestPruneHistoryKeepsEachFieldsNewestChangeAtOrBeforeCutoff(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{
		ObservationRetention: devices.MinimumObservationRetention,
		Now:                  func() time.Time { return base.Add(devices.MinimumObservationRetention + time.Hour) },
	})
	entities := registerRollupEntities(t, service)
	for i, value := range []string{`true`, `false`, `true`} {
		projectRollupReport(t, service, entities.power, value, base.Add(time.Duration(i-2)*time.Minute))
	}
	projectRollupReport(t, service, entities.power, `false`, base.Add(30*time.Minute))
	// Cutoff = base: the change at base-1m is the newest at or before it.
	if err := service.PruneHistory(t.Context(), base.Add(devices.MinimumObservationRetention)); err != nil {
		t.Fatal(err)
	}
	power := string(entities.power)
	got := changeSummary(storedStateChanges(t, database))
	if want := []string{power + ` "" 12:00:00=true`, power + ` "" 12:30:00=false`}; !slices.Equal(got, want) {
		t.Fatalf("after prune = %q, want %q", got, want)
	}

	// A timeline starting after the cutoff opens with the seed's value.
	result, err := service.QueryStateChanges(t.Context(), devices.StateChangeQuery{
		EntityID: entities.power,
		Range:    devices.HistoryRange{From: base.Add(10 * time.Minute), To: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	timeline, ok := result.Data.(devices.StateChangeTimeline)
	if !result.Seeded || !ok || len(timeline.Intervals) != 2 ||
		string(timeline.Intervals[0].Value) != "true" || string(timeline.Intervals[1].Value) != "false" {
		t.Fatalf("timeline = %+v (seeded %v)", result.Data, result.Seeded)
	}
}

func TestReadStateChangesRejectsUnsupportedReads(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	for i, value := range []string{`true`, `false`, `true`} {
		projectRollupReport(t, service, entities.power, value, base.Add(time.Duration(i)*time.Minute))
	}
	window := devices.HistoryRange{From: base, To: base.Add(time.Hour)}
	missing, err := devices.ParseEntityID("ent_019a1b2c-3d4e-7000-8000-00000000ffff")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		read devices.StateChangeRead
		want string
	}{
		{
			"numeric field",
			devices.StateChangeRead{EntityID: entities.temperature, Range: window, Limit: 10},
			"invalid_query",
		},
		{"text field in a summary", devices.StateChangeRead{
			EntityID: entities.setting, Field: "/mode", Range: window, Boolean: true, Limit: 10,
		}, "value_type"},
		{"too many changes", devices.StateChangeRead{EntityID: entities.power, Range: window, Limit: 2}, "query_limit"},
		{"unknown entity", devices.StateChangeRead{EntityID: missing, Range: window, Limit: 10}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, readErr := repository.ReadStateChanges(t.Context(), test.read)
			if test.want == "" {
				if !errors.Is(readErr, devices.ErrEntityNotFound) {
					t.Fatalf("error = %v, want ErrEntityNotFound", readErr)
				}
				return
			}
			var failure *devices.HistoryQueryError
			if !errors.As(readErr, &failure) || failure.Code != test.want {
				t.Fatalf("error = %v, want %s", readErr, test.want)
			}
		})
	}
	rows, err := repository.ReadStateChanges(t.Context(), devices.StateChangeRead{
		EntityID: entities.power, Range: window, Limit: 3,
	})
	if err != nil || rows.Seed != nil || len(rows.Rows) != 3 {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
}
