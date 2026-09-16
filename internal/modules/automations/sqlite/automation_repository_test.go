package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// SQLite must persist normalized definitions, increment revisions on replacement,
// and enforce optimistic concurrency for replacement and deletion.
func TestSQLiteRepositoryCreateReplaceDeleteRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)

	definition := validDomainDefinition(t)
	definition.Name = "  Office light  "
	created, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d, want 1", created.Revision)
	}
	if _, err = automations.ParseAutomationID(string(created.ID)); err != nil {
		t.Fatalf("created ID = %q: %v", created.ID, err)
	}
	if created.Definition.Name != "Office light" {
		t.Fatalf("stored name = %q, want trimmed", created.Definition.Name)
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("created timestamps = %v / %v", created.CreatedAt, created.UpdatedAt)
	}
	assertStoredAutomationJSON(t, database, created.ID)

	replaced, err := repository.ReplaceAutomation(ctx, created.ID, 1, definition)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Revision != 2 {
		t.Fatalf("replaced revision = %d, want 2", replaced.Revision)
	}
	if !replaced.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("replacement changed created_at: %v -> %v", created.CreatedAt, replaced.CreatedAt)
	}

	if _, err = repository.ReplaceAutomation(ctx, created.ID, 1, definition); !errors.Is(
		err, automations.ErrRevisionConflict,
	) {
		t.Fatalf("stale replacement error = %v, want ErrRevisionConflict", err)
	}
	missingID, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ReplaceAutomation(ctx, missingID, 1, definition); !errors.Is(
		err, automations.ErrAutomationNotFound,
	) {
		t.Fatalf("unknown replacement error = %v, want ErrAutomationNotFound", err)
	}

	if err = repository.DeleteAutomation(ctx, created.ID, 1); !errors.Is(err, automations.ErrRevisionConflict) {
		t.Fatalf("stale deletion error = %v, want ErrRevisionConflict", err)
	}
	if err = repository.DeleteAutomation(ctx, created.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.GetAutomation(ctx, created.ID); !errors.Is(err, automations.ErrAutomationNotFound) {
		t.Fatalf("read after deletion error = %v, want ErrAutomationNotFound", err)
	}
	if err = repository.DeleteAutomation(ctx, created.ID, 2); !errors.Is(err, automations.ErrAutomationNotFound) {
		t.Fatalf("second deletion error = %v, want ErrAutomationNotFound", err)
	}
}

// Keyset pages must advance without overlap and reproduce the full ID-ordered set.
//
//nolint:gocognit // One ordered pagination sequence proves keyset stability.
func TestSQLiteRepositoryListIsKeysetStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	for index := range 5 {
		definition := validDomainDefinition(t)
		definition.Name = fmt.Sprintf("Automation %d", index)
		if _, err := repository.CreateAutomation(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repository.ListAutomations(ctx, automations.ListAutomationsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 5 || all.HasMore {
		t.Fatalf("unpaged list = %d items, HasMore %v", len(all.Items), all.HasMore)
	}

	var paged []automations.Record
	var after *automations.AutomationID
	pages := 0
	for {
		page, pageErr := repository.ListAutomations(ctx, automations.ListAutomationsParams{
			AfterID: after, Limit: 2,
		})
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if len(page.Items) == 0 {
			break
		}
		pages++
		paged = append(paged, page.Items...)
		last := page.Items[len(page.Items)-1].ID
		after = &last
		if !page.HasMore {
			break
		}
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
	if len(paged) != len(all.Items) {
		t.Fatalf("paged items = %d, want %d", len(paged), len(all.Items))
	}
	for index := range all.Items {
		if paged[index].ID != all.Items[index].ID {
			t.Fatalf("paged[%d] = %s, want %s", index, paged[index].ID, all.Items[index].ID)
		}
	}

	if _, err = repository.ListAutomations(
		ctx,
		automations.ListAutomationsParams{Limit: -1},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("negative limit error = %v, want ErrInvalidAutomation", err)
	}
	if _, err = repository.ListAutomations(
		ctx,
		automations.ListAutomationsParams{Limit: 201},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("oversized limit error = %v, want ErrInvalidAutomation", err)
	}
}

// ListEnabledAutomations must return only enabled current definitions in
// ascending Automation ID order for the Service admission pre-read.
func TestSQLiteRepositoryListEnabledAutomationsFiltersAndOrders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	for index, enabled := range []bool{true, false, true} {
		definition := validDomainDefinition(t)
		definition.Name = fmt.Sprintf("Enabled filter %d", index)
		definition.Enabled = enabled
		if _, err := repository.CreateAutomation(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}

	records, err := repository.ListEnabledAutomations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("enabled records = %d, want 2", len(records))
	}
	for index, record := range records {
		if !record.Definition.Enabled {
			t.Fatalf("record %s is disabled", record.ID)
		}
		if index > 0 && records[index-1].ID >= record.ID {
			t.Fatalf("records are not ascending: %s >= %s", records[index-1].ID, record.ID)
		}
	}
}

// Malformed stored JSON must return a permanent error, not a partially trusted definition.
func TestSQLiteRepositoryRejectsMalformedStoredDefinition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)

	id, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(
		ctx,
		`INSERT INTO automations (id, revision, definition_json, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`,
		string(id), `{"name":"only a name"}`, migrationTimestamp, migrationTimestamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.GetAutomation(ctx, id); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("malformed stored definition error = %v, want ErrInvalidAutomation", err)
	}
}

// Persistence must reject contradictory typed families and non-object parameters
// before encoding can discard invalid input.
func TestSQLiteRepositoryRejectsMalformedTypedDefinitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	missingID, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(definition *automations.Definition)
	}{
		{
			"observation trigger carries event payload",
			func(definition *automations.Definition) {
				definition.Triggers[0].EntityEvent = &automations.EntityEventTrigger{
					EntityID: newEntityID(t), EventName: "single_press",
				}
			},
		},
		{
			"parameters are not an object",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`[]`)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := validDomainDefinition(t)
			test.mutate(&definition)
			if _, createErr := repository.CreateAutomation(ctx, definition); !errors.Is(
				createErr, automations.ErrInvalidAutomation,
			) {
				t.Fatalf("create error = %v, want ErrInvalidAutomation", createErr)
			}
			if _, replaceErr := repository.ReplaceAutomation(
				ctx, missingID, 1, definition,
			); !errors.Is(replaceErr, automations.ErrInvalidAutomation) {
				t.Fatalf("replace error = %v, want ErrInvalidAutomation", replaceErr)
			}
		})
	}
}

// Real SQLite constraints must enforce definition shape, unique active Runs and
// Fact outcomes, Run/Skip exclusivity, and consistent Step evidence.
func TestMigrationEnforcesAutomationStorageInvariants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)

	automationID := newAutomationIDString(t)
	runID := newRunIDString(t)
	secondRunID := newRunIDString(t)
	skipID := newSkipIDString(t)
	factID := newFactIDString(t)
	factEntityID := string(newEntityID(t))
	factObservationID := newObservationIDString(t)

	rejected := []struct {
		name string
		sql  string
		args []any
	}{
		{"definition not an object", `INSERT INTO automations VALUES (?, 1, ?, ?, ?)`,
			[]any{automationID, `[]`, migrationTimestamp, migrationTimestamp}},
		{"definition not json", `INSERT INTO automations VALUES (?, 1, ?, ?, ?)`,
			[]any{automationID, `not json`, migrationTimestamp, migrationTimestamp}},
		{"revision below one", `INSERT INTO automations VALUES (?, 0, ?, ?, ?)`,
			[]any{automationID, `{}`, migrationTimestamp, migrationTimestamp}},
		{"run running with completion", insertHistoryRunSQL,
			[]any{runID, automationID, "running", nil, migrationTimestamp, `[]`}},
		{"run failed without failure code", insertHistoryRunSQL,
			[]any{runID, automationID, "failed", nil, migrationTimestamp, `[]`}},
		{"skip without fact", `INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json)
			VALUES (?, ?, 'Office light', 'skip', 1, ?, ?, 'stale_fact', 'device_fact',
			'{"mode":"not_configured","bypass_requested":false}')`,
			[]any{skipID, automationID, migrationTimestamp, matchedTriggerJSON(t)}},
		{
			"skip without reason",
			`INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
			skip_matched_triggers_json, skip_source, condition_decision_json)
			VALUES (?, ?, 'Office light', 'skip', 1, ?, ?, 'observation', ?, 'applied', ?, 'true', ?,
			?, 'device_fact', '{"mode":"not_configured","bypass_requested":false}')`,
			[]any{
				skipID,
				automationID,
				migrationTimestamp,
				factID,
				factEntityID,
				factObservationID,
				migrationTimestamp,
				matchedTriggerJSON(t),
			},
		},
		{"run carries skip reason", `INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			run_snapshot_json, run_source, run_status, run_started_at,
			run_matched_trigger_ids_json, skip_reason, condition_decision_json)
			VALUES (?, ?, 'Office light', 'run', 1, ?, '{}', 'manual', 'running', ?, '[]', 'stale_fact',
			'{"mode":"not_configured","bypass_requested":false}')`,
			[]any{runID, automationID, migrationTimestamp, migrationTimestamp}},
		{"receipt with unknown kind", `INSERT INTO automation_fact_receipts VALUES (?, ?, 'later', ?)`,
			[]any{factID, automationID, runID}},
		{"receipt with wrong history prefix", `INSERT INTO automation_fact_receipts VALUES (?, ?, 'run', ?)`,
			[]any{factID, automationID, automationID}},
	}
	for _, test := range rejected {
		if _, err := database.ExecContext(ctx, test.sql, test.args...); err == nil {
			t.Fatalf("%s: invalid row was accepted", test.name)
		}
	}

	// A valid manual running Run, its not_attempted Step, a valid Skip carrying
	// Fact evidence, and one receipt all commit.
	mustExec(t, database, insertHistoryRunSQL, runID, automationID, "running", nil, nil, `[]`)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, 0, 'light_on', 'not_attempted')`,
		runID,
	)
	mustExec(t, database, insertHistorySkipSQL,
		skipID, automationID, migrationTimestamp, factID, factEntityID, factObservationID, `true`, migrationTimestamp,
		matchedTriggerJSON(t), `{"mode":"not_configured","bypass_requested":false}`)
	mustExec(t, database, `INSERT INTO automation_fact_receipts VALUES (?, ?, 'skip', ?)`,
		factID, automationID, skipID)

	rejectedAfter := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"step without run parent",
			`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, 0, 'light_on', 'not_attempted')`,
			[]any{newRunIDString(t)},
		},
		{
			"Step satisfied without verified command",
			`INSERT INTO automation_run_steps (run_id, position, step_id, status, reserved_command_id, reserved_correlation_id, started_at, completed_at) VALUES (?, 1, 'light_off', 'satisfied', ?, ?, ?, ?)`,
			[]any{runID, newCommandIDString(t), newCorrelationIDString(t), migrationTimestamp, migrationTimestamp},
		},
		{"second running run for same automation", insertHistoryRunSQL,
			[]any{secondRunID, automationID, "running", nil, nil, `[]`}},
		{"duplicate fact receipt", `INSERT INTO automation_fact_receipts VALUES (?, ?, 'skip', ?)`,
			[]any{factID, automationID, skipID}},
	}
	for _, test := range rejectedAfter {
		if _, err := database.ExecContext(ctx, test.sql, test.args...); err == nil {
			t.Fatalf("%s: invalid row was accepted", test.name)
		}
	}

	var steps int
	if err := database.QueryRowContext(
		ctx, `SELECT count(*) FROM automation_run_steps WHERE run_id = ?`, runID,
	).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != 1 {
		t.Fatalf("steps = %d, want 1", steps)
	}
}
