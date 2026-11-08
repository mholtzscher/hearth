//go:build integration

package sqlite //nolint:testpackage // Exercise the SQLite boundary and its fixtures.

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestHistoryQuerySnapshotOrderingAndContinuation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, svc, entity, base)
	// Two accepted rows share an Observation time, while a rejected row remains excluded.
	if _, err := db.ExecContext(
		ctx,
		"UPDATE observations SET observed_at = ? WHERE observation_id = ?",
		formatTime(base.Add(time.Second)),
		projected[2].id,
	); err != nil {
		t.Fatal(err)
	}
	request := devices.HistoryReadRequest{
		EntityID:    entity,
		Range:       devices.HistoryRange{From: base.Add(time.Second), To: base.Add(4 * time.Second)},
		Order:       devices.HistoryAscending,
		IncludeSeed: true,
	}
	window, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if window.Seed == nil || window.Seed.ObservationID != projected[0].id || len(window.Rows) != 2 ||
		window.Rows[0].ObservationID != projected[1].id ||
		window.Rows[1].ObservationID != projected[2].id ||
		window.HasMore {
		t.Fatalf("unexpected window: %+v", window)
	}
	request.IncludeSeed = false
	request.Order = devices.HistoryDescending
	reverse, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || len(reverse.Rows) != 2 || reverse.Rows[0].ObservationID != projected[2].id ||
		reverse.Rows[1].ObservationID != projected[1].id {
		t.Fatalf("reverse = %+v, %v", reverse, err)
	}
	// Cursor remains usable after its row is pruned; later inserts cannot enter traversal.
	cursor := &devices.HistoryReadCursor{
		ObservedAt:   window.Rows[0].ObservedAt,
		ReceiveOrder: window.Rows[0].ReceiveOrder,
		HighWater:    window.HighWater,
	}
	if _, err = db.ExecContext(ctx, "DELETE FROM observations WHERE observation_id = ?", projected[1].id); err != nil {
		t.Fatal(err)
	}
	obs := newObservation(t, entity, `true`, base.Add(2*time.Second))
	if _, err = svc.ProjectObservation(ctx, "simulator", testRuntimeID, obs, obs.AdapterReceivedAt); err != nil {
		t.Fatal(err)
	}
	request.Order = devices.HistoryAscending
	request.Cursor = cursor
	after, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || len(after.Rows) != 1 || after.Rows[0].ObservationID != projected[2].id {
		t.Fatalf("continuation = %+v, %v", after, err)
	}
}

func TestHistoryQueryValidationAndCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	request := devices.HistoryReadRequest{
		EntityID: entity,
		Range:    devices.HistoryRange{From: base, To: base.Add(time.Hour)},
		Order:    devices.HistoryAscending,
	}
	empty, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || empty.Rows == nil || len(empty.Rows) != 0 || empty.HighWater != 0 {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	bad := request
	bad.Cursor = &devices.HistoryReadCursor{ObservedAt: base, ReceiveOrder: 1, HighWater: 0}
	_, err = repo.ReadHistoryWindow(ctx, bad)
	var queryErr *devices.HistoryQueryError
	if !errors.As(err, &queryErr) || queryErr.Code != "invalid_query" {
		t.Fatalf("invalid cursor: %v", err)
	}
	bad = request
	bad.EntityID = "bad"
	_, err = repo.ReadHistoryWindow(ctx, bad)
	if !errors.As(err, &queryErr) || queryErr.Code != "invalid_query" {
		t.Fatalf("invalid ID: %v", err)
	}
	bad = request
	bad.Range = devices.HistoryRange{
		From: time.Date(9998, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(9998, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	future, err := repo.ReadHistoryWindow(ctx, bad)
	if err != nil || future.Rows == nil || len(future.Rows) != 0 {
		t.Fatalf("structurally valid future window = %+v, %v", future, err)
	}
	bad = request
	bad.EntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	_, err = repo.ReadHistoryWindow(ctx, bad)
	if !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("missing: %v", err)
	}
	projected := seedStateHistory(t, svc, entity, base)
	if _, err = db.ExecContext(
		ctx,
		"UPDATE observations SET observed_at = ? WHERE observation_id = ?",
		"2026-08-22T12:00:00.5Z",
		projected[1].id,
	); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ReadHistoryWindow(ctx, request)
	if err == nil || errors.As(err, &queryErr) {
		t.Fatalf("corruption should be internal: %v", err)
	}
	if _, err = db.ExecContext(
		ctx,
		"UPDATE entities SET type_id = ? WHERE id = ?",
		"hearth.enumevent/v1",
		entity,
	); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ReadHistoryWindow(ctx, request)
	if !errors.As(err, &queryErr) || queryErr.Code != "unsupported_source" {
		t.Fatalf("stateless: %v", err)
	}
}

func TestHistoryQueryOversizedValueAndIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, svc, entity, base)
	if _, err := db.ExecContext(
		ctx,
		"UPDATE observations SET state_value_json = ? WHERE observation_id = ?",
		`"`+strings.Repeat("x", devices.HistoryMaxSourceBytes)+`"`,
		projected[0].id,
	); err != nil {
		t.Fatal(err)
	}
	request := devices.HistoryReadRequest{
		EntityID: entity,
		Range:    devices.HistoryRange{From: base, To: base.Add(time.Hour)},
		Order:    devices.HistoryAscending,
	}
	_, err := repo.ReadHistoryWindow(ctx, request)
	var queryErr *devices.HistoryQueryError
	if !errors.As(err, &queryErr) || queryErr.Code != "query_limit" {
		t.Fatalf("oversized: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE observations SET state_value_json =
		(SELECT state_value_json FROM observations WHERE observation_id = ?) WHERE observation_id = ?`,
		projected[0].id, projected[1].id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE observations SET state_value_json = 'true' WHERE observation_id = ?`,
		projected[0].id,
	); err != nil {
		t.Fatal(err)
	}
	prefix, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || len(prefix.Rows) != 1 || !prefix.HasMore {
		t.Fatalf("byte-limited prefix = %+v, %v", prefix, err)
	}
}

func TestHistoryQueryRowBudgetAndSeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	// One seed and 10,000 range rows share a timestamp within their respective groups.
	_, err := db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<10000)
 INSERT INTO observations(observation_id, adapter_id, entity_id, disposition, state_value_json, adapter_received_at, observed_at)
 SELECT printf('obs_01890f47-7a6b-7c4d-8e9f-%012x',i), 'simulator', ?, 'applied', 'true', ?, CASE WHEN i=0 THEN ? ELSE ? END FROM n`, entity, formatTime(base), formatTime(base.Add(-time.Second)), formatTime(base))
	if err != nil {
		t.Fatal(err)
	}
	req := devices.HistoryReadRequest{
		EntityID:    entity,
		Range:       devices.HistoryRange{From: base, To: base.Add(time.Hour)},
		Order:       devices.HistoryAscending,
		IncludeSeed: true,
	}
	window, err := repo.ReadHistoryWindow(ctx, req)
	if err != nil || window.Seed == nil || len(window.Rows) != devices.HistoryMaxSourceRows-1 || !window.HasMore {
		t.Fatalf("bounded rows = %d, hasMore = %v, error = %v", len(window.Rows), window.HasMore, err)
	}
	last := window.Rows[len(window.Rows)-1]
	req.IncludeSeed = false
	req.Cursor = &devices.HistoryReadCursor{
		ObservedAt:   last.ObservedAt,
		ReceiveOrder: last.ReceiveOrder,
		HighWater:    window.HighWater,
	}
	remainder, err := repo.ReadHistoryWindow(ctx, req)
	if err != nil || len(remainder.Rows) != 1 || remainder.HasMore {
		t.Fatalf("remainder = %+v, error = %v", remainder, err)
	}
}

// TestHistoryQueryFoldsRangeLargerThanOneRead checks that a timeline and a
// temporal summary read a range holding more rows than one read admits.
func TestHistoryQueryFoldsRangeLargerThanOneRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	// A true seed one second before the range, then rows alternating false/true
	// every second: three reads, with odd rows true.
	rows := 2*devices.HistoryMaxSourceRows + 5000
	_, err := db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (VALUES(-1) UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO observations(observation_id, adapter_id, entity_id, disposition, state_value_json, adapter_received_at, observed_at)
 SELECT printf('obs_01890f47-7a6b-7c4d-8e9f-%012x',i+1), 'simulator', ?, 'applied',
        CASE WHEN i % 2 = 0 THEN 'false' ELSE 'true' END, ?,
        strftime('%Y-%m-%dT%H:%M:%S', ?, printf('%+d seconds', i)) || '.000000000Z' FROM n`,
		rows-1, entity, formatTime(base), base.Format(time.DateTime))
	if err != nil {
		t.Fatal(err)
	}
	span := devices.HistoryRange{From: base, To: base.Add(time.Duration(rows) * time.Second)}
	summary, err := svc.QueryHistory(ctx, devices.HistoryQuery{
		EntityID: entity,
		Range:    span,
		Result: devices.HistorySummary{Metrics: map[string]devices.HistoryMetric{
			"on_seconds": devices.HistoryDurationWhere{WhereExpr: "value == true", Unit: "seconds"},
			"turn_ons":   devices.HistoryTransitionsInto{WhereExpr: "value == true"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := summary.Data.(devices.HistorySummaryResult)
	half := strconv.Itoa(rows / 2)
	if string(got.Metrics["on_seconds"]) != half || string(got.Metrics["turn_ons"]) != half ||
		!got.Coverage.SeedAvailable || got.Coverage.InferredKnownSeconds != float64(rows) {
		t.Fatalf("summary = %s, %s, coverage %+v", got.Metrics["on_seconds"], got.Metrics["turn_ons"], got.Coverage)
	}
	timeline, err := svc.QueryHistory(ctx, devices.HistoryQuery{
		EntityID: entity, Range: span, Result: devices.HistoryTimeline{},
	})
	if err != nil {
		t.Fatal(err)
	}
	intervals := timeline.Data.(devices.HistoryTimelineResult).Intervals
	first, firstOK := intervals[0].(devices.HistoryValueInterval)
	last, lastOK := intervals[len(intervals)-1].(devices.HistoryValueInterval)
	if len(intervals) != rows || !firstOK || string(first.Value) != "false" ||
		!first.Range.To.Equal(base.Add(time.Second)) || !lastOK || string(last.Value) != "true" ||
		!last.Range.To.Equal(span.To) {
		t.Fatalf(
			"timeline has %d intervals, first %+v, last %+v",
			len(intervals),
			intervals[0],
			intervals[len(intervals)-1],
		)
	}
}

func TestHistoryQueryIndexMigrationPreservesRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(
		ctx,
		`INSERT INTO observations(observation_id, adapter_id, entity_id, disposition, state_value_json, adapter_received_at, observed_at) VALUES ('obs_01890f47-7a6b-7c4d-8e9f-0123456789ab','simulator','ent_01890f47-7a6b-7c4d-8e9f-0123456789ab','applied','true','2026-01-01T00:00:00.000000000Z','2026-01-01T00:00:00.000000000Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = database.QueryRowContext(ctx, `SELECT state_value_json FROM observations WHERE observation_id = 'obs_01890f47-7a6b-7c4d-8e9f-0123456789ab'`).
		Scan(&value); err != nil ||
		value != "true" {
		t.Fatalf("retained value = %q, %v", value, err)
	}
}

func TestHistoryQueryDirectInvalidInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	valid := devices.HistoryReadRequest{
		EntityID: entity,
		Range:    devices.HistoryRange{From: base, To: base.Add(time.Hour)},
		Order:    devices.HistoryAscending,
	}
	tests := []struct {
		name   string
		mutate func(*devices.HistoryReadRequest)
	}{
		{"zero from", func(r *devices.HistoryReadRequest) { r.Range.From = time.Time{} }},
		{"equal endpoints", func(r *devices.HistoryReadRequest) { r.Range.To = r.Range.From }},
		{"reversed endpoints", func(r *devices.HistoryReadRequest) { r.Range.To = r.Range.From.Add(-time.Second) }},
		{
			"non UTC range",
			func(r *devices.HistoryReadRequest) { r.Range.From = r.Range.From.In(time.FixedZone("offset", 3600)) },
		},
		{
			"year outside storage encoding",
			func(r *devices.HistoryReadRequest) { r.Range.To = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		},
		{"empty order", func(r *devices.HistoryReadRequest) { r.Order = "" }},
		{"negative source limit", func(r *devices.HistoryReadRequest) { r.SourceRowLimit = -1 }},
		{
			"excess source limit",
			func(r *devices.HistoryReadRequest) { r.SourceRowLimit = devices.HistoryMaxSourceRows + 1 },
		},
		{"unknown order", func(r *devices.HistoryReadRequest) { r.Order = "sideways" }},
		{
			"descending seed",
			func(r *devices.HistoryReadRequest) { r.Order = devices.HistoryDescending; r.IncludeSeed = true },
		},
		{"cursor before range", func(r *devices.HistoryReadRequest) {
			r.Cursor = &devices.HistoryReadCursor{ObservedAt: base.Add(-time.Nanosecond), ReceiveOrder: 1, HighWater: 1}
		}},
		{"cursor at range end", func(r *devices.HistoryReadRequest) {
			r.Cursor = &devices.HistoryReadCursor{ObservedAt: r.Range.To, ReceiveOrder: 1, HighWater: 1}
		}},
		{"cursor non UTC", func(r *devices.HistoryReadRequest) {
			r.Cursor = &devices.HistoryReadCursor{
				ObservedAt:   base.In(time.FixedZone("offset", 3600)),
				ReceiveOrder: 1,
				HighWater:    1,
			}
		}},
		{"cursor zero order", func(r *devices.HistoryReadRequest) {
			r.Cursor = &devices.HistoryReadCursor{ObservedAt: base, ReceiveOrder: 0, HighWater: 1}
		}},
		{"cursor above high water", func(r *devices.HistoryReadRequest) {
			r.Cursor = &devices.HistoryReadCursor{ObservedAt: base, ReceiveOrder: 2, HighWater: 1}
		}},
		{"seed with cursor", func(r *devices.HistoryReadRequest) {
			r.IncludeSeed = true
			r.Cursor = &devices.HistoryReadCursor{ObservedAt: base, ReceiveOrder: 1, HighWater: 1}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := valid
			test.mutate(&request)
			_, err := repo.ReadHistoryWindow(ctx, request)
			var failure *devices.HistoryQueryError
			if !errors.As(err, &failure) || failure.Code != "invalid_query" {
				t.Fatalf("error = %v, want invalid_query", err)
			}
		})
	}
}

func TestHistoryQueryRequestedPrefixStopsBeforeLaterValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	rows := seedStateHistory(t, svc, entity, base)
	request := devices.HistoryReadRequest{
		EntityID: entity, Range: devices.HistoryRange{From: base, To: base.Add(time.Hour)},
		Order: devices.HistoryAscending, SourceRowLimit: 2,
	}
	first, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || len(first.Rows) != 2 || !first.HasMore || first.Rows[0].ObservationID != rows[0].id ||
		first.Rows[1].ObservationID != rows[1].id {
		t.Fatalf("first prefix = %+v, %v", first, err)
	}
	last := first.Rows[1]
	request.Cursor = &devices.HistoryReadCursor{
		ObservedAt:   last.ObservedAt,
		ReceiveOrder: last.ReceiveOrder,
		HighWater:    first.HighWater,
	}
	second, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || len(second.Rows) != 2 || second.HasMore || second.Rows[0].ObservationID != rows[2].id ||
		second.Rows[1].ObservationID != rows[4].id {
		t.Fatalf("final prefix = %+v, %v", second, err)
	}
}

func TestHistoryQuerySeedUsesLatestTimestampThenReceiveOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	svc := openHistoryService(t, db)
	entity := registerHistoryEntity(t, svc)
	repo := NewDeviceRepository(db, firstLightCatalog(t))
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, svc, entity, base)
	// The last received accepted report is earlier in time; two others tie at the
	// latest prior timestamp, and the rejected report at that time is ineligible.
	times := []time.Time{
		base.Add(time.Second),
		base.Add(2 * time.Second),
		base.Add(2 * time.Second),
		base.Add(2 * time.Second),
		base,
	}
	for i, at := range times {
		if _, err := db.ExecContext(
			ctx,
			"UPDATE observations SET observed_at = ? WHERE observation_id = ?",
			formatTime(at),
			projected[i].id,
		); err != nil {
			t.Fatal(err)
		}
	}
	request := devices.HistoryReadRequest{
		EntityID:    entity,
		Range:       devices.HistoryRange{From: base.Add(3 * time.Second), To: base.Add(time.Hour)},
		Order:       devices.HistoryAscending,
		IncludeSeed: true,
	}
	window, err := repo.ReadHistoryWindow(ctx, request)
	if err != nil || window.Seed == nil || window.Seed.ObservationID != projected[2].id || len(window.Rows) != 0 {
		t.Fatalf("seed window = %+v, %v", window, err)
	}
	// Evidence exactly at the lower boundary must be a range row, never a seed.
	request.Range.From = base.Add(2 * time.Second)
	window, err = repo.ReadHistoryWindow(ctx, request)
	if err != nil || window.Seed == nil || window.Seed.ObservationID != projected[0].id || len(window.Rows) != 2 ||
		window.Rows[0].ObservationID != projected[1].id ||
		window.Rows[1].ObservationID != projected[2].id {
		t.Fatalf("boundary window = %+v, %v", window, err)
	}
}
