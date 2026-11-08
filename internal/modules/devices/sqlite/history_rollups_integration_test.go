//go:build integration

package sqlite //nolint:testpackage // Exercise rollup maintenance at the SQLite boundary.

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
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

func TestReadHistoryRollupBucketsGroupsCells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	seedRollupReports(t, service, entities, base)
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	read := func(entity devices.EntityID, field string, bucket time.Duration) []devices.HistoryRollupBucket {
		t.Helper()
		buckets, err := repository.ReadHistoryRollupBuckets(ctx, devices.HistoryRollupRequest{
			EntityID: entity, Field: field, From: base, End: base.Add(2 * time.Minute), Bucket: bucket,
		})
		if err != nil {
			t.Fatal(err)
		}
		return buckets
	}
	integer := devices.HistoryIntNumber
	if got, want := read(entities.temperature, "", time.Minute), []devices.HistoryRollupBucket{
		{Index: 0, Count: 3, Sum: integer(62600), Min: integer(20000), Max: integer(21300)},
		{Index: 1, Count: 1, Sum: integer(22000), Min: integer(22000), Max: integer(22000)},
	}; !slices.Equal(got, want) {
		t.Fatalf("one-minute buckets = %+v, want %+v", got, want)
	}
	if got, want := read(entities.temperature, "", 2*time.Minute), []devices.HistoryRollupBucket{
		{Index: 0, Count: 4, Sum: integer(84600), Min: integer(20000), Max: integer(22000)},
	}; !slices.Equal(got, want) {
		t.Fatalf("two-minute bucket = %+v, want %+v", got, want)
	}
	// Mixed cells keep each extreme's storage class; the sum becomes real.
	if got, want := read(entities.setting, "/value", 2*time.Minute), []devices.HistoryRollupBucket{{
		Index: 0, Count: 2, Nulls: 1,
		Sum: devices.HistoryRealNumber(500.5), Min: integer(250), Max: devices.HistoryRealNumber(250.5),
	}}; !slices.Equal(got, want) {
		t.Fatalf("setting bucket = %+v, want %+v", got, want)
	}
	if got := read(entities.color, "/y", time.Minute); len(got) != 1 || got[0].Max != integer(3290) {
		t.Fatalf("color /y buckets = %+v", got)
	}
}

func TestReadHistoryRollupBucketsRejectsUnsupportedReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entities := registerRollupEntities(t, service)
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	valid := devices.HistoryRollupRequest{
		EntityID: entities.temperature, From: base, End: base.Add(time.Hour), Bucket: time.Minute,
	}
	for _, test := range []struct {
		name   string
		mutate func(*devices.HistoryRollupRequest)
		want   devices.HistoryQueryError
	}{
		{"unaligned from", func(r *devices.HistoryRollupRequest) { r.From = base.Add(time.Second) },
			devices.HistoryQueryError{Code: "invalid_query"}},
		{"unaligned end", func(r *devices.HistoryRollupRequest) { r.End = base.Add(time.Hour + time.Second) },
			devices.HistoryQueryError{Code: "invalid_query"}},
		{"empty", func(r *devices.HistoryRollupRequest) { r.End = r.From }, devices.HistoryQueryError{Code: "invalid_query"}},
		{"partial bucket", func(r *devices.HistoryRollupRequest) { r.Bucket = 90 * time.Second },
			devices.HistoryQueryError{Code: "invalid_query"}},
		{"boolean field", func(r *devices.HistoryRollupRequest) { r.EntityID, r.Field = entities.color, "/active" },
			devices.HistoryQueryError{Code: "invalid_query", Field: "select_field"}},
		{"no numeric field", func(r *devices.HistoryRollupRequest) { r.EntityID = entities.power },
			devices.HistoryQueryError{Code: "invalid_query", Field: "select_field"}},
	} {
		request := valid
		test.mutate(&request)
		_, err := repository.ReadHistoryRollupBuckets(ctx, request)
		var failure *devices.HistoryQueryError
		if !errors.As(err, &failure) || *failure != test.want {
			t.Fatalf("%s: err = %v, want %+v", test.name, err, test.want)
		}
	}
	unknown := valid
	unknown.EntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ff"
	if _, err := repository.ReadHistoryRollupBuckets(ctx, unknown); !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("unknown entity err = %v", err)
	}
	binding, err := service.Register(ctx, "simulator", testRuntimeID, entityEventRegistration())
	if err != nil {
		t.Fatal(err)
	}
	stateless := valid
	stateless.EntityID = binding.Entities[0].EntityID
	var failure *devices.HistoryQueryError
	if _, err = repository.ReadHistoryRollupBuckets(ctx, stateless); !errors.As(err, &failure) ||
		*failure != (devices.HistoryQueryError{Code: "unsupported_source"}) {
		t.Fatalf("stateless entity err = %v", err)
	}
}

// The final bucket includes the whole cell that contains To.
func TestQueryHistoryCountsTheCellContainingTo(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{
		Now: func() time.Time { return base.Add(time.Hour) },
	})
	entities := registerRollupEntities(t, service)
	projectRollupReport(t, service, entities.temperature, `100`, base.Add(10*time.Second))
	projectRollupReport(t, service, entities.temperature, `200`, base.Add(40*time.Second))
	projectRollupReport(t, service, entities.temperature, `300`, base.Add(70*time.Second))
	result, err := service.QueryHistory(context.Background(), devices.HistoryQuery{
		EntityID: entities.temperature,
		Range:    devices.HistoryRange{From: base, To: base.Add(30 * time.Second)},
		Metrics: map[string]devices.HistoryMetric{
			"max":  devices.HistoryMaximum{},
			"mean": devices.HistorySampleMean{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := result.Data.(devices.HistorySummaryResult).Metrics
	if string(metrics["max"]) != "200" || string(metrics["mean"]) != "150" {
		t.Fatalf(
			"metrics = max %s mean %s, want the report after To in the same minute only",
			metrics["max"],
			metrics["mean"],
		)
	}
}
