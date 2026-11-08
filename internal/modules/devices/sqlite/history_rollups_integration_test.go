//go:build integration

package sqlite //nolint:testpackage // Exercise rollup maintenance at the SQLite boundary.

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// storedRollupCell renders one cell with SQLite storage classes, so integer
// and real values that compare equal still differ.
type storedRollupCell struct {
	entity, field         string
	cell                  int64
	count, nulls, invalid int64
	sum, minimum, maximum string
}

func storedRollupCells(t *testing.T, database *sql.DB) []storedRollupCell {
	t.Helper()
	rows, err := database.Query(`SELECT entity_id, field_path, cell_start, sample_count, null_count, invalid_count,
		typeof(sum_value) || ':' || coalesce(sum_value, ''), typeof(min_value) || ':' || coalesce(min_value, ''),
		typeof(max_value) || ':' || coalesce(max_value, '')
		FROM observation_rollups ORDER BY entity_id, field_path, cell_start`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cells []storedRollupCell
	for rows.Next() {
		var cell storedRollupCell
		if err = rows.Scan(&cell.entity, &cell.field, &cell.cell, &cell.count, &cell.nulls, &cell.invalid,
			&cell.sum, &cell.minimum, &cell.maximum); err != nil {
			t.Fatal(err)
		}
		cells = append(cells, cell)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cells
}

type rollupTestEntities struct {
	temperature, setting, color, power devices.EntityID
}

func registerRollupEntities(t *testing.T, service *devices.Service) rollupTestEntities {
	t.Helper()
	externalID := "rollup-device"
	binding, err := service.Register(context.Background(), "simulator", testRuntimeID, devices.Registration{
		BindingKey: "rollups",
		Device: devices.DeviceDescriptor{
			ExternalID: &externalID,
			Name:       "Rollup device",
			Kind:       devices.DeviceKindLight,
		},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "temperature",
				ExternalID: "rollup.temperature",
				Name:       "Temperature",
				TypeID:     devices.EntityTypeTemperatureV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{}}`),
			},
			{Key: "setting", ExternalID: "rollup.setting", Name: "Setting", TypeID: devices.EntityTypeNumericsettingV1,
				Support: devices.EntitySupport(`{"state":{"minimum":142,"maximum":454,"unit":"mired",` +
					`"choices":["previous"]},"operations":{"set":{}}}`)},
			{Key: "color", ExternalID: "rollup.color", Name: "Color", TypeID: devices.EntityTypeColorxyV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`)},
			{Key: "power", ExternalID: "rollup.power", Name: "Power", TypeID: devices.EntityTypePowerV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]devices.EntityID{}
	for _, entity := range binding.Entities {
		ids[entity.Key] = entity.EntityID
	}
	return rollupTestEntities{ids["temperature"], ids["setting"], ids["color"], ids["power"]}
}

func projectRollupReport(
	t *testing.T, service *devices.Service, entity devices.EntityID, value string, observedAt time.Time,
) devices.ProjectionResult {
	t.Helper()
	observation := newObservation(t, entity, value, observedAt)
	result, err := service.ProjectObservation(context.Background(), "simulator", testRuntimeID, observation, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// seedRollupReports projects accepted, unchanged, rejected, null, absent, and
// mixed integer/real reports across two cells.
func seedRollupReports(t *testing.T, service *devices.Service, entities rollupTestEntities, base time.Time) {
	t.Helper()
	for _, report := range []struct {
		entity devices.EntityID
		value  string
		offset time.Duration
		want   devices.ObservationDisposition
	}{
		{entities.temperature, `21300`, 0, devices.DispositionApplied},
		{entities.temperature, `21300`, 10 * time.Second, devices.DispositionUnchanged},
		{entities.temperature, `20000`, 20 * time.Second, devices.DispositionApplied},
		{entities.temperature, `"warm"`, 30 * time.Second, devices.DispositionRejected},
		{entities.temperature, `22000`, 70 * time.Second, devices.DispositionApplied},
		{entities.setting, `{"mode":"value","value":250.5}`, 0, devices.DispositionApplied},
		{entities.setting, `{"mode":"choice","choice":"previous"}`, 15 * time.Second, devices.DispositionApplied},
		{entities.setting, `{"mode":"value","value":250}`, 30 * time.Second, devices.DispositionApplied},
		{entities.color, `{"active":true,"x":3127,"y":3290}`, 5 * time.Second, devices.DispositionApplied},
		{entities.power, `true`, 5 * time.Second, devices.DispositionApplied},
	} {
		if got := projectRollupReport(
			t,
			service,
			report.entity,
			report.value,
			base.Add(report.offset),
		); got.Disposition != report.want {
			t.Fatalf("%s %s disposition = %s, want %s", report.entity, report.value, got.Disposition, report.want)
		}
	}
}

func TestObservationRollupsTrackAcceptedReports(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	seedRollupReports(t, service, entities, base)
	cell := base.Unix()
	firstTemperature := storedRollupCell{
		string(entities.temperature), "", cell, 3, 0, 0, "integer:62600", "integer:20000", "integer:21300",
	}
	expected := func() []storedRollupCell {
		cells := []storedRollupCell{
			{string(entities.color), "/x", cell, 1, 0, 0, "integer:3127", "integer:3127", "integer:3127"},
			{string(entities.color), "/y", cell, 1, 0, 0, "integer:3290", "integer:3290", "integer:3290"},
			{string(entities.setting), "/value", cell, 2, 1, 0, "real:500.5", "integer:250", "real:250.5"},
			firstTemperature,
			{string(entities.temperature), "", cell + 60, 1, 0, 0, "integer:22000", "integer:22000", "integer:22000"},
		}
		slices.SortFunc(cells, func(a, b storedRollupCell) int {
			return cmp.Or(
				strings.Compare(a.entity, b.entity),
				strings.Compare(a.field, b.field),
				cmp.Compare(a.cell, b.cell),
			)
		})
		return cells
	}
	if got, want := storedRollupCells(t, database), expected(); !slices.Equal(got, want) {
		t.Fatalf("cells = %+v\nwant  %+v", got, want)
	}

	// A duplicate Observation returns before its insert and changes no cell.
	observation := newObservation(t, entities.temperature, `25000`, base.Add(40*time.Second))
	for range 2 {
		if _, err := service.ProjectObservation(
			context.Background(), "simulator", testRuntimeID, observation, base.Add(40*time.Second),
		); err != nil {
			t.Fatal(err)
		}
	}
	firstTemperature.count, firstTemperature.sum, firstTemperature.maximum = 4, "integer:87600", "integer:25000"
	if got, want := storedRollupCells(t, database), expected(); !slices.Equal(got, want) {
		t.Fatalf("after duplicate cells = %+v\nwant  %+v", got, want)
	}
}

func TestObservationRollupFailureRollsBackProjection(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projectRollupReport(t, service, entities.temperature, `21300`, base)
	counts := func() string {
		var observations, facts, cells int
		var state string
		if err := database.QueryRow(`SELECT (SELECT count(*) FROM observations),
			(SELECT count(*) FROM device_facts_outbox), (SELECT count(*) FROM observation_rollups),
			(SELECT value_json FROM entity_states WHERE entity_id = ?)`, entities.temperature,
		).Scan(&observations, &facts, &cells, &state); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("observations=%d facts=%d cells=%d state=%s", observations, facts, cells, state)
	}
	before := counts()
	if _, err := database.Exec(`CREATE TRIGGER fail_rollups BEFORE INSERT ON observation_rollups
		BEGIN SELECT RAISE(ABORT, 'forced rollup failure'); END`); err != nil {
		t.Fatal(err)
	}
	observation := newObservation(t, entities.temperature, `22000`, base.Add(time.Minute))
	if _, err := service.ProjectObservation(
		context.Background(), "simulator", testRuntimeID, observation, base.Add(time.Minute),
	); err == nil {
		t.Fatal("projection should fail when its rollup upsert fails")
	}
	if after := counts(); after != before {
		t.Fatalf("failed projection left changes: before %s, after %s", before, after)
	}
}

func TestObservationRollupBackfillMatchesIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	seedRollupReports(t, service, entities, base)
	ingested := storedRollupCells(t, database)
	if len(ingested) == 0 {
		t.Fatal("ingest built no cells")
	}

	provider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	var index int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE name = 'observations_entity_observed_history_idx'`).Scan(&index); err != nil || index != 1 {
		t.Fatalf("Down did not restore the history index: %d, %v", index, err)
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE name = 'observations_entity_observed_history_idx'`).Scan(&index); err != nil || index != 0 {
		t.Fatalf("migration 30 kept the history index: %d, %v", index, err)
	}
	if backfilled := storedRollupCells(t, database); !slices.Equal(backfilled, ingested) {
		t.Fatalf("backfilled cells = %+v\ningested cells = %+v", backfilled, ingested)
	}
}
