package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type historyProjection struct {
	id          ObservationID
	disposition ObservationDisposition
}

func seedStateHistory(t *testing.T, service *Service, entityID EntityID, base time.Time) []historyProjection {
	t.Helper()
	ctx := context.Background()
	source := base.Add(-time.Hour)
	sequence := []struct {
		value  string
		source *time.Time
	}{
		{`true`, &source},
		{`true`, nil},
		{`false`, &source},
		{`1`, &source},
		{`false`, nil},
	}
	projected := make([]historyProjection, 0, len(sequence))
	for index, step := range sequence {
		observation := newObservation(t, entityID, step.value, base.Add(time.Duration(index)*time.Second))
		observation.SourceUpdatedAt = step.source
		result, err := service.ProjectObservation(
			ctx, "simulator", testRuntimeID, observation, observation.AdapterReceivedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		projected = append(projected, historyProjection{id: observation.ID, disposition: result.Disposition})
	}
	want := []ObservationDisposition{
		DispositionApplied, DispositionUnchanged, DispositionApplied, DispositionRejected, DispositionUnchanged,
	}
	for index := range want {
		if projected[index].disposition != want[index] {
			t.Fatalf("projection %d disposition = %q, want %q", index, projected[index].disposition, want[index])
		}
	}
	return projected
}

func collectStateHistory(
	t *testing.T,
	service *Service,
	entityID EntityID,
	filter EntityStateHistoryFilter,
	limit int,
) []EntityStateHistoryEntry {
	t.Helper()
	ctx := context.Background()
	var collected []EntityStateHistoryEntry
	var before *int64
	for {
		page, err := service.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
			EntityID: entityID, Filter: filter, BeforeReceiveOrder: before, Limit: limit,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Items {
			if before != nil && entry.ReceiveOrder >= *before {
				t.Fatalf("history row %q breaks strict keyset order", entry.ObservationID)
			}
			cursor := entry.ReceiveOrder
			before = &cursor
			collected = append(collected, entry)
		}
		if !page.HasMore {
			if len(page.Items) == 0 && len(collected) == 0 {
				return []EntityStateHistoryEntry{}
			}
			return collected
		}
		if len(page.Items) != limit {
			t.Fatalf("non-final page returned %d items with limit %d", len(page.Items), limit)
		}
	}
}

func historyIDs(entries []EntityStateHistoryEntry) []ObservationID {
	ids := make([]ObservationID, len(entries))
	for index, entry := range entries {
		ids[index] = entry.ObservationID
	}
	return ids
}

func assertHistoryIDs(t *testing.T, filter EntityStateHistoryFilter, got, want []ObservationID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s history IDs = %v, want %v", filter, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s history IDs = %v, want %v", filter, got, want)
		}
	}
}

func openHistoryService(t *testing.T, database *sql.DB) *Service {
	t.Helper()
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	return newTestService(repository, nil, catalog, Dependencies{})
}

func registerHistoryEntity(t *testing.T, service *Service) EntityID {
	t.Helper()
	binding, err := service.Register(context.Background(), "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	return binding.Entities[0].EntityID
}

func TestSQLiteEntityStateHistoryFiltersOrderAndPaginate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entityID := registerHistoryEntity(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, service, entityID, base)
	// Newest-first receive order: 5, 4, 3, 2, 1.
	id := func(index int) ObservationID { return projected[index].id }

	for _, test := range []struct {
		filter EntityStateHistoryFilter
		want   []ObservationID
	}{
		{EntityStateHistoryFilterAll, []ObservationID{id(4), id(3), id(2), id(1), id(0)}},
		{EntityStateHistoryFilterUpdates, []ObservationID{id(4), id(2), id(1), id(0)}},
		{EntityStateHistoryFilterApplied, []ObservationID{id(2), id(0)}},
		{EntityStateHistoryFilterUnchanged, []ObservationID{id(4), id(1)}},
		{EntityStateHistoryFilterRejected, []ObservationID{id(3)}},
	} {
		collected := collectStateHistory(t, service, entityID, test.filter, 2)
		assertHistoryIDs(t, test.filter, historyIDs(collected), test.want)
		for _, entry := range collected {
			if entry.Disposition == DispositionRejected && entry.Value != nil {
				t.Fatalf("%s rejected entry %q carries a value", test.filter, entry.ObservationID)
			}
			if entry.Disposition != DispositionRejected && entry.Value == nil {
				t.Fatalf("%s accepted entry %q is missing its value", test.filter, entry.ObservationID)
			}
		}
	}

	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	unfiltered, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(
		t,
		"empty-filter",
		historyIDs(unfiltered.Items),
		[]ObservationID{id(4), id(2), id(1), id(0)},
	)

	if _, filterErr := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: "recent", Limit: 50,
	}); filterErr == nil {
		t.Fatal("unknown adapter filter unexpectedly succeeded")
	}
}

func TestSQLiteEntityStateHistoryMapsOwnedDomainValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	entityID := registerHistoryEntity(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, service, entityID, base)

	page, err := service.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterAll, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(projected) {
		t.Fatalf("history items = %d, want %d", len(page.Items), len(projected))
	}
	applied := page.Items[4]
	if applied.Disposition != DispositionApplied || string(applied.Value) != "true" ||
		applied.Rejection != nil || applied.SourceUpdatedAt == nil ||
		!applied.SourceUpdatedAt.Equal(base.Add(-time.Hour)) ||
		applied.AdapterReceivedAt.Location() != time.UTC || applied.ObservedAt.Location() != time.UTC {
		t.Fatalf("applied entry = %#v", applied)
	}
	rejected := page.Items[1]
	if rejected.Disposition != DispositionRejected || rejected.Value != nil ||
		rejected.Rejection == nil || *rejected.Rejection != RejectionInvalidValue ||
		rejected.SourceUpdatedAt == nil {
		t.Fatalf("rejected entry = %#v", rejected)
	}

	page.Items[4].Value[0] = 'f'
	*page.Items[4].SourceUpdatedAt = time.Time{}
	mutation := RejectionStaleRuntime
	page.Items[4].Rejection = &mutation

	reread, err := service.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterAll, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(reread.Items[4].Value) != "true" || reread.Items[4].Rejection != nil ||
		!reread.Items[4].SourceUpdatedAt.Equal(base.Add(-time.Hour)) {
		t.Fatalf("reread applied entry = %#v", reread.Items[4])
	}
}

func TestSQLiteEntityStateHistoryListsObservationsForUnknownEntities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	unknownEntityID, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	observation := newObservation(t, unknownEntityID, `true`, observedAt)
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, observation, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionRejected {
		t.Fatalf("unknown-entity projection = %#v", result)
	}

	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	page, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: unknownEntityID, Filter: EntityStateHistoryFilterAll, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ObservationID != observation.ID ||
		page.Items[0].Disposition != DispositionRejected || page.Items[0].Value != nil {
		t.Fatalf("unknown-entity history = %#v", page)
	}

	if _, unknownErr := service.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: unknownEntityID, Limit: 50,
	}); unknownErr == nil {
		t.Fatal("service history for unknown Entity unexpectedly succeeded")
	}
}

func TestSQLiteEntityStateHistorySurvivesInsertsAndPruning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	entityID := registerHistoryEntity(t, service)
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	projected := seedStateHistory(t, service, entityID, base)

	first, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterUpdates, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("first history page = %#v", first)
	}
	cursor := first.Items[len(first.Items)-1].ReceiveOrder

	newer := []ObservationID{}
	for index, value := range []string{`true`, `false`} {
		observation := newObservation(t, entityID, value, base.Add(time.Duration(10+index)*time.Second))
		if _, projectionErr := service.ProjectObservation(
			ctx, "simulator", testRuntimeID, observation, observation.AdapterReceivedAt,
		); projectionErr != nil {
			t.Fatal(projectionErr)
		}
		newer = append(newer, observation.ID)
	}

	second, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterUpdates,
		BeforeReceiveOrder: &cursor, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Concurrent inserts carry higher receive orders, so the old cursor still
	// returns only the rows that were already below it, in strict order.
	assertHistoryIDs(
		t,
		"continuation",
		historyIDs(second.Items),
		[]ObservationID{projected[1].id, projected[0].id},
	)
	if second.HasMore {
		t.Fatalf("continuation page still reports more rows: %#v", second)
	}
	for _, entry := range second.Items {
		if entry.ObservationID == newer[0] || entry.ObservationID == newer[1] {
			t.Fatalf("concurrent insert leaked below an old cursor: %q", entry.ObservationID)
		}
	}

	// Age every observation below the cursor past the retention window,
	// including the cursor row itself, then prune. The cursor must stay valid
	// and report an empty final page.
	if _, err = database.ExecContext(ctx,
		`UPDATE observations SET observed_at = ? WHERE entity_id = ? AND receive_order < ?`,
		formatSortableTime(base.Add(-time.Hour)), string(entityID), newerCursor(t, database, newer[0]),
	); err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteExpiredObservations(ctx, base.Add(time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	afterPrune, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterUpdates,
		BeforeReceiveOrder: &cursor, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterPrune.Items == nil || len(afterPrune.Items) != 0 || afterPrune.HasMore {
		t.Fatalf("pruned continuation page = %#v", afterPrune)
	}
}

func newerCursor(t *testing.T, database *sql.DB, id ObservationID) int64 {
	t.Helper()
	var order int64
	if err := database.QueryRow(
		`SELECT receive_order FROM observations WHERE observation_id = ?`, id,
	).Scan(&order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestSQLiteEntityStateHistoryRetentionKeepsCurrentAnchor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := openHistoryService(t, database)
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	entityID := registerHistoryEntity(t, service)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first := newObservation(t, entityID, `false`, base)
	second := newObservation(t, entityID, `true`, base.Add(time.Hour))
	if _, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, first, base); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, second, base.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	// Retention derives from observed_at on each prune, so both rows predate
	// the cutoff; the prune must still keep the observation backing current
	// State until a newer State supersedes it.
	if err := service.DeleteExpiredObservations(
		ctx, base.Add(30*24*time.Hour+2*time.Hour), 30*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: entityID, Filter: EntityStateHistoryFilterAll, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryIDs(t, "retained", historyIDs(page.Items), []ObservationID{second.ID})
}

// loadObservationHistoryQueries reads the named history queries from the actual
// dbqueries/observations.sql source so EXPLAIN QUERY PLAN exercises the exact
// predicates, ordering, and limits the adapter binds. The file is parsed in
// the test only; production code exposes no query text.
func loadObservationHistoryQueries(t *testing.T) map[string]string {
	t.Helper()
	_, caller, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate history query source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(caller), "dbqueries", "observations.sql"))
	if err != nil {
		t.Fatal(err)
	}
	queries := make(map[string]string)
	var current string
	var builder strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		queries[current] = strings.TrimRight(strings.TrimSpace(builder.String()), ";")
		current = ""
		builder.Reset()
	}
	for line := range strings.Lines(string(source)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- name: ") {
			flush()
			fields := strings.Fields(strings.TrimPrefix(trimmed, "-- name: "))
			if len(fields) == 0 {
				t.Fatalf("malformed query name line %q", line)
			}
			current = fields[0]
			continue
		}
		if current != "" {
			builder.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				builder.WriteString("\n")
			}
		}
	}
	flush()
	for _, name := range []string{
		"ListEntityStateHistoryFirstPage", "ListEntityStateHistoryAfter",
		"ListEntityStateUpdatesFirstPage", "ListEntityStateUpdatesAfter",
		"ListEntityStateHistoryByDispositionFirstPage", "ListEntityStateHistoryByDispositionAfter",
	} {
		if queries[name] == "" {
			t.Fatalf("history query %q not found in observations.sql", name)
		}
	}
	return queries
}

func assertHistoryQueryPlan(
	t *testing.T,
	database *sql.DB,
	query string,
	args []any,
	wantIndex string,
) {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if scanErr := rows.Scan(&id, &parent, &notused, &detail); scanErr != nil {
			t.Fatal(scanErr)
		}
		upper := strings.ToUpper(detail)
		if strings.Contains(upper, "SCAN ") || strings.Contains(upper, "TEMP B-TREE") {
			t.Fatalf("query plan scans or sorts: %q", detail)
		}
		if strings.Contains(detail, "USING INDEX "+wantIndex) {
			indexed = true
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if !indexed {
		t.Fatalf("query plan does not use index %s for %q", wantIndex, query)
	}
}

// seedTargetHistory inserts applied, unchanged, then rejected observations for the
// target Entity itself, so sparse filters must skip the dominant disposition
// within one Entity history rather than across unrelated Entities.
func seedTargetHistory(
	t *testing.T,
	database *sql.DB,
	target EntityID,
	applied, unchanged, rejected int,
) {
	t.Helper()
	ctx := context.Background()
	timestamp := formatTime(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	observedAt := formatSortableTime(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	// Insert each disposition in receive order with deterministic observation IDs.
	insertDisposition := func(offset, count int, disposition string, rejection, value sql.NullString) {
		t.Helper()
		if count <= 0 {
			return
		}
		if _, execErr := transaction.ExecContext(ctx, `
			WITH RECURSIVE seq(n) AS (
				SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < ?
			)
			INSERT INTO observations (
				observation_id, adapter_id, entity_id, disposition, rejection_code,
				state_value_json, adapter_received_at, observed_at
			) SELECT printf('obs_seed_%08d', ? + n - 1), 'simulator', ?, ?, ?, ?, ?, ?
			FROM seq ORDER BY n`,
			count, offset, string(target), disposition, rejection, value, timestamp, observedAt,
		); execErr != nil {
			t.Fatal(execErr)
		}
	}
	insertDisposition(0, applied, string(DispositionApplied),
		sql.NullString{}, sql.NullString{String: `true`, Valid: true})
	insertDisposition(applied, unchanged, string(DispositionUnchanged),
		sql.NullString{}, sql.NullString{String: `true`, Valid: true})
	insertDisposition(applied+unchanged, rejected, string(DispositionRejected),
		sql.NullString{String: string(RejectionInvalidValue), Valid: true}, sql.NullString{})
	if commitErr := transaction.Commit(); commitErr != nil {
		t.Fatal(commitErr)
	}
}

func assertAllHistoryQueryPlans(t *testing.T, database *sql.DB, entityID string, cursor int64) {
	t.Helper()
	queries := loadObservationHistoryQueries(t)
	byDispositionFirst := queries["ListEntityStateHistoryByDispositionFirstPage"]
	byDispositionAfter := queries["ListEntityStateHistoryByDispositionAfter"]
	absent := "ent_01890f47-7a6b-7c4d-8e9f-0123456789ff"
	for _, test := range []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			"all first page", queries["ListEntityStateHistoryFirstPage"],
			[]any{entityID, 51}, "observations_entity_history_idx",
		},
		{
			"all continuation", queries["ListEntityStateHistoryAfter"],
			[]any{entityID, cursor, 51}, "observations_entity_history_idx",
		},
		{
			"updates first page", queries["ListEntityStateUpdatesFirstPage"],
			[]any{entityID, 51}, "observations_entity_updates_history_idx",
		},
		{
			"updates continuation", queries["ListEntityStateUpdatesAfter"],
			[]any{entityID, cursor, 51}, "observations_entity_updates_history_idx",
		},
		{
			"applied first page", byDispositionFirst, []any{entityID, "applied", 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"applied continuation", byDispositionAfter, []any{entityID, "applied", cursor, 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"unchanged first page", byDispositionFirst, []any{entityID, "unchanged", 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"unchanged continuation", byDispositionAfter, []any{entityID, "unchanged", cursor, 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"rejected first page", byDispositionFirst, []any{entityID, "rejected", 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"rejected continuation", byDispositionAfter, []any{entityID, "rejected", cursor, 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"absent entity first page", byDispositionFirst, []any{absent, "applied", 51},
			"observations_entity_disposition_history_idx",
		},
		{
			"absent entity continuation", byDispositionAfter, []any{absent, "applied", cursor, 51},
			"observations_entity_disposition_history_idx",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertHistoryQueryPlan(t, database, test.query, test.args, test.wantIndex)
		})
	}
}

func TestSQLiteEntityStateHistoryUnchangedHeavyQueryPlans(t *testing.T) {
	t.Parallel()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	target := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	// Small skewed fixtures protect index selection and sparse pagination;
	// they deliberately do not measure scale or latency.
	seedTargetHistory(t, database, target, 10, 200, 5)

	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	applied := requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterApplied, Limit: 50,
	}, 10, false)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterRejected, Limit: 50,
	}, 5, false)
	unchanged := requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterUnchanged, Limit: 50,
	}, 50, true)
	requireHistoryContinuation(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterUnchanged, Limit: 50,
	}, unchanged, 50, true)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterUpdates, Limit: 50,
	}, 50, true)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterAll, Limit: 50,
	}, 50, true)
	oldestApplied := applied[len(applied)-1].ReceiveOrder
	requireEmptyHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterApplied,
		BeforeReceiveOrder: &oldestApplied, Limit: 50,
	})
	requireEmptyHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ff"),
		Filter:   EntityStateHistoryFilterApplied, Limit: 50,
	})
	assertAllHistoryQueryPlans(t, database, string(target), 100)
}

func TestSQLiteEntityStateHistoryRejectedHeavyQueryPlans(t *testing.T) {
	t.Parallel()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	target := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	seedTargetHistory(t, database, target, 10, 10, 200)

	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	applied := requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterApplied, Limit: 50,
	}, 10, false)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterUnchanged, Limit: 50,
	}, 10, false)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterUpdates, Limit: 50,
	}, 20, false)
	rejected := requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterRejected, Limit: 50,
	}, 50, true)
	requireHistoryContinuation(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterRejected, Limit: 50,
	}, rejected, 50, true)
	requireHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterAll, Limit: 50,
	}, 50, true)
	oldestApplied := applied[len(applied)-1].ReceiveOrder
	requireEmptyHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: target, Filter: EntityStateHistoryFilterApplied,
		BeforeReceiveOrder: &oldestApplied, Limit: 50,
	})
	requireEmptyHistoryPage(t, repository, ListEntityStateHistoryParams{
		EntityID: EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ff"),
		Filter:   EntityStateHistoryFilterApplied, Limit: 50,
	})
	assertAllHistoryQueryPlans(t, database, string(target), 100)
}

func assertStrictReceiveOrder(t *testing.T, entries []EntityStateHistoryEntry) {
	t.Helper()
	for index := 1; index < len(entries); index++ {
		if entries[index].ReceiveOrder >= entries[index-1].ReceiveOrder {
			t.Fatal("history page breaks strict receive-order ordering")
		}
	}
}

func requireHistoryPage(
	t *testing.T,
	repository *SQLiteRepository,
	params ListEntityStateHistoryParams,
	wantCount int,
	wantMore bool,
) []EntityStateHistoryEntry {
	t.Helper()
	page, err := repository.ListEntityStateHistory(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != wantCount || page.HasMore != wantMore {
		t.Fatalf(
			"%s page = %d items, hasMore = %v, want %d items, hasMore = %v",
			params.Filter, len(page.Items), page.HasMore, wantCount, wantMore,
		)
	}
	assertStrictReceiveOrder(t, page.Items)
	return page.Items
}

func requireHistoryContinuation(
	t *testing.T,
	repository *SQLiteRepository,
	params ListEntityStateHistoryParams,
	previous []EntityStateHistoryEntry,
	wantCount int,
	wantMore bool,
) {
	t.Helper()
	cursor := previous[len(previous)-1].ReceiveOrder
	params.BeforeReceiveOrder = &cursor
	next := requireHistoryPage(t, repository, params, wantCount, wantMore)
	if next[0].ReceiveOrder >= cursor {
		t.Fatalf("continuation starts at %d above cursor %d", next[0].ReceiveOrder, cursor)
	}
}

func requireEmptyHistoryPage(
	t *testing.T,
	repository *SQLiteRepository,
	params ListEntityStateHistoryParams,
) {
	t.Helper()
	page, err := repository.ListEntityStateHistory(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("%s empty page = %#v", params.Filter, page)
	}
}
