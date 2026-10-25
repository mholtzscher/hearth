//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// BenchmarkHistoryListing measures the complete repository read, including the
// transaction, limit+1 fetch, summary validation, and domain mapping. The synthetic
// retained Runs have valid ~60 KiB snapshots; setup and payload accounting are untimed.
func BenchmarkHistoryListing(b *testing.B) {
	database, parent := historyBenchmarkDatabase(b)
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	for _, limit := range []int{50, 200} {
		for _, continuation := range []bool{false, true} {
			b.Run(fmt.Sprintf("limit=%d/after=%t", limit, continuation), func(b *testing.B) {
				params, wantID := historyBenchmarkPage(parent, limit, continuation)
				payload := historyListingPayload(b, database, params)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					page, listErr := repository.ListHistory(b.Context(), params)
					if listErr != nil || len(page.Items) != limit || !page.HasMore {
						b.Fatalf("listing = %d items, more=%t, %v", len(page.Items), page.HasMore, listErr)
					}
					if page.Items[0].ID != wantID || page.Items[0].AutomationName != "Large retained snapshot" {
						b.Fatalf("unexpected first summary: %#v", page.Items[0])
					}
				}
				b.ReportMetric(float64(payload), "db_payload_B/op")
			})
		}
	}
}

func historyBenchmarkPage(
	parent automations.AutomationID,
	limit int,
	continuation bool,
) (automations.ListHistoryParams, string) {
	params := automations.ListHistoryParams{AutomationID: parent, Limit: limit}
	if !continuation {
		return params, "arn_00000000-0000-7000-8000-000000001000"
	}
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	cursor := "arn_00000000-0000-7000-8000-000000000501"
	params.BeforeRecordedAt, params.BeforeID = &at, &cursor
	return params, "arn_00000000-0000-7000-8000-000000000500"
}

func historyBenchmarkDatabase(b *testing.B) (*sql.DB, automations.AutomationID) {
	b.Helper()
	database, err := platformdb.Open(b.Context(), filepath.Join(b.TempDir(), "history.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(b.Context(), database); err != nil {
		b.Fatal(err)
	}
	parent := automations.AutomationID("aut_00000000-0000-7000-8000-000000000001")
	entity := devices.EntityID("ent_00000000-0000-7000-8000-000000000001")
	snapshot, err := automations.EncodeDefinition(automations.Definition{
		Name:    "Large retained snapshot",
		Enabled: true,
		Triggers: []automations.Trigger{
			{ID: "event", Body: automations.EntityEventTrigger{EntityID: entity, EventName: "pressed"}},
		},
		Steps: []automations.Step{{ID: "command", Body: automations.CommandStep{
			EntityID: entity, OperationName: devices.OperationNameSet,
			Parameters: devices.CommandParameters(`{"value":"` + strings.Repeat("x", 60*1024) + `"}`),
		}}},
	})
	if err != nil {
		b.Fatal(err)
	}
	_, err = database.ExecContext(b.Context(), `WITH RECURSIVE entries(n) AS (
		VALUES(1) UNION ALL SELECT n + 1 FROM entries WHERE n < 1000
	) INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at, run_completed_at,
		run_matched_trigger_ids_json, condition_decision_json
	) SELECT printf('arn_00000000-0000-7000-8000-%012d', n), ?, 'Large retained snapshot',
		'run', 7, ?, ?, 'manual', 'succeeded', ?, ?, '[]',
		'{"mode":"not_configured","bypass_requested":false}' FROM entries`,
		string(parent), migrationTimestamp, string(snapshot), migrationTimestamp, migrationTimestamp)
	if err != nil {
		b.Fatal(err)
	}
	return database, parent
}

// historyQueryCapture observes the actual generated query without maintaining a
// second projection in the benchmark or its payload calculation.
type historyQueryCapture struct {
	*sql.DB

	query string
	args  []any
}

func (capture *historyQueryCapture) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	capture.query, capture.args = query, args
	return capture.DB.QueryContext(ctx, query, args...)
}

func historyListingPayload(b *testing.B, database *sql.DB, params automations.ListHistoryParams) int {
	b.Helper()
	capture := &historyQueryCapture{DB: database}
	queries := dbsqlc.New(capture)
	if params.BeforeID == nil {
		if _, err := queries.ListHistoryFirstPage(b.Context(), dbsqlc.ListHistoryFirstPageParams{
			AutomationID: string(params.AutomationID), Limit: int64(params.Limit + 1),
		}); err != nil {
			b.Fatal(err)
		}
	} else if _, err := queries.ListHistoryAfter(b.Context(), dbsqlc.ListHistoryAfterParams{
		AutomationID: string(params.AutomationID), RecordedAt: migrationTimestamp,
		ID: *params.BeforeID, PageLimit: int64(params.Limit + 1),
	}); err != nil {
		b.Fatal(err)
	}
	rows, err := database.QueryContext(b.Context(), capture.query, capture.args...)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		b.Fatal(err)
	}
	values, targets := make([]any, len(columns)), make([]any, len(columns))
	for i := range targets {
		targets[i] = &values[i]
	}
	var payload, count int
	for rows.Next() {
		if err = rows.Scan(targets...); err != nil {
			b.Fatal(err)
		}
		count++
		for _, value := range values {
			switch value := value.(type) {
			case string:
				payload += len(value)
			case int64:
				payload += 8
			case nil:
			default:
				b.Fatalf("unexpected SQLite value %T", value)
			}
		}
	}
	if err = rows.Err(); err != nil || count != params.Limit+1 {
		b.Fatalf("payload read: rows=%d, %v", count, err)
	}
	return payload
}
