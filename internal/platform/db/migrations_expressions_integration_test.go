//go:build integration

package db //nolint:testpackage // Tests the embedded migration at the current pre-expression schema boundary.

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const expressionUpgradeID = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
const expressionUpgradeRun = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
const expressionUpgradeStamp = "2026-10-27T12:00:00.000000000Z"

const expressionUpgradePayload = `{"kind":"if","steps":[{"kind":"delay","duration_ms":12}],"expr":"data","literal":9007199254740993,"huge":9223372036854775808,"exponent":1e1000000000,"fraction":1.2300e-4}`

func expressionUpgradeDocument() string {
	return `{"name":"preserve","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[` +
		`{"id":"if","kind":"if","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"then":[{"id":"command","kind":"command","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":` + expressionUpgradePayload + `}],"else":[{"id":"function","kind":"function","adapter_id":"weather","function_key":"forecast","contract_fingerprint":"sha256-v1:` + strings.Repeat(
		"0",
		64,
	) + `","arguments":` + expressionUpgradePayload + `}]},` +
		`{"id":"choose","kind":"choose","branches":[{"id":"one","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"steps":[{"id":"notify","kind":"notification","destination_id":"ndst_01890f47-7a6b-7c4d-8e9f-0123456789ab","title":"quote\" and \\ newline\n","message":"expr literal"}]}],"default":[{"id":"delay","kind":"delay","duration_ms":1000}]}]}`
}

func expressionUpgradeDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "expressions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err = newGooseProvider(t, database).UpTo(t.Context(), 22); err != nil {
		t.Fatal(err)
	}
	return database
}

func expressionUpgradeExec(t *testing.T, database *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func seedExpressionUpgrade(t *testing.T, database *sql.DB, document string) {
	t.Helper()
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_definition_revisions VALUES (?,1,?)`,
		expressionUpgradeID,
		document,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_definition_revisions VALUES (?,2,?)`,
		expressionUpgradeID,
		document,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automations VALUES (?,2,?,?)`,
		expressionUpgradeID,
		expressionUpgradeStamp,
		expressionUpgradeStamp,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_history (id,automation_id,automation_name,kind,revision,recorded_at,definition_revision,run_source,run_status,run_started_at,run_matched_trigger_ids_json,condition_decision_json) VALUES (?,?,'preserve','run',1,?,1,'manual','running',?,'[]','{"mode":"not_configured","bypass_requested":false}')`,
		expressionUpgradeRun,
		expressionUpgradeID,
		expressionUpgradeStamp,
		expressionUpgradeStamp,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_history (id,automation_id,automation_name,kind,revision,recorded_at,skip_matched_triggers_json,skip_reason,skip_source,condition_decision_json) VALUES ('ask_01890f47-7a6b-7c4d-8e9f-0123456789ab',?,'skip','skip',2,?,'[]','conditions_false','manual','{}')`,
		expressionUpgradeID,
		expressionUpgradeStamp,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_holds VALUES (?,2,'held',42,'pending',?,'2026-10-27T12:00:10.000000000Z')`,
		expressionUpgradeID,
		expressionUpgradeStamp,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_fact_receipts VALUES ('fct_01890f47-7a6b-7c4d-8e9f-0123456789ab',?,'run',?)`,
		expressionUpgradeID,
		expressionUpgradeRun,
	)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_schedule_watermarks VALUES ('global',?)`,
		expressionUpgradeStamp,
	)
	for sequence, row := range []struct{ step, kind, phase, evidence string }{
		{"if", "if", "succeeded", `{"outcome":"then"}`},
		{"command", "command", "succeeded", `{"command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","verified_command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","command_outcome":"satisfied"}`},
		{"choose", "choose", "succeeded", `{"outcome":"branch","selected_branch_id":"one"}`},
		{"notify", "notification", "succeeded", `{"attempt_id":"ntf_01890f47-7a6b-7c4d-8e9f-0123456789ab","outcome":"accepted"}`},
		{"delay", "delay", "succeeded", `{}`},
		{"function", "function", "running", `{"invocation_id":"afi_01890f47-7a6b-7c4d-8e9f-0123456789ab"}`},
	} {
		var completed any = expressionUpgradeStamp
		if row.phase == "running" {
			completed = nil
		}
		expressionUpgradeExec(
			t,
			database,
			`INSERT INTO automation_step_executions VALUES (?,?,?,?,?,?,?,?)`,
			expressionUpgradeRun,
			row.step,
			sequence,
			row.kind,
			row.phase,
			expressionUpgradeStamp,
			completed,
			row.evidence,
		)
	}
}

func expressionUpgradeRows(t *testing.T, database *sql.DB, query string) string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range targets {
			targets[index] = &values[index]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		raw, encodeErr := json.Marshal(values)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		result.Write(raw)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result.String()
}

//nolint:gocognit // One upgrade checks preservation, exact conversion, immutability, refused rollback and cascading retention.
func TestExpressionMigrationPreservesSharedRevisionsAndUnifiedHistory(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	seedExpressionUpgrade(t, database, expressionUpgradeDocument())
	// Migration 22 had no evidence-size CHECK: keep even oversized stored rows.
	expressionUpgradeExec(t, database, `UPDATE automation_step_executions SET evidence_json=? WHERE step_id='delay'`,
		`{"note":"`+strings.Repeat("x", 262144)+`"}`)
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO devices (id,kind,name,created_at,updated_at) VALUES ('dev_other','sensor','unrelated',?,?)`,
		expressionUpgradeStamp,
		expressionUpgradeStamp,
	)
	queries := []string{
		`SELECT automation_id,revision,definition_json FROM automation_definition_revisions ORDER BY automation_id,revision`,
		`SELECT id,revision,created_at,updated_at FROM automations`,
		`SELECT id,automation_id,automation_name,kind,revision,recorded_at,definition_revision,run_status,run_started_at,run_completed_at,condition_decision_json FROM automation_history ORDER BY id`,
		`SELECT run_id,step_id,sequence,kind,phase,started_at,completed_at,evidence_json FROM automation_step_executions ORDER BY sequence`,
		`SELECT automation_id,revision,trigger_id,last_receive_order,phase,started_at,due_at FROM automation_holds`,
		`SELECT fact_id,automation_id,outcome_kind,history_id FROM automation_fact_receipts`,
		`SELECT id,highwater_at FROM automation_schedule_watermarks`,
		`SELECT * FROM devices`,
	}
	before := make([]string, len(queries))
	for i, query := range queries {
		before[i] = expressionUpgradeRows(t, database, query)
	}
	provider := newGooseProvider(t, database)
	if _, err := provider.UpTo(t.Context(), 23); err != nil {
		t.Fatal(err)
	}
	for i, query := range queries {
		if after := expressionUpgradeRows(t, database, query); after != before[i] {
			t.Fatalf("upgrade changed retained rows: %s\nbefore=%s\nafter=%s", query, before[i], after)
		}
	}
	for _, path := range []string{"$.steps[0].then[0].parameters", "$.steps[0].else[0].arguments"} {
		for _, revision := range []int{1, 2} {
			var payload string
			if err := database.QueryRowContext(t.Context(), `SELECT definition_json -> ? FROM automation_definition_revisions WHERE automation_id=? AND revision=?`, path, expressionUpgradeID, revision).
				Scan(&payload); err != nil {
				t.Fatal(err)
			}
			if payload != expressionUpgradePayload {
				t.Fatalf("revision %d rounded/reinterpreted payload: %s", revision, payload)
			}
		}
	}
	for path, want := range map[string]string{
		"$.steps[1].branches[0].steps[0].title":   `"quote\" and \\ newline\n"`,
		"$.steps[1].branches[0].steps[0].message": `"expr literal"`,
		"$.steps[1].default[0].duration_ms":       `1000`,
	} {
		var got string
		if err := database.QueryRowContext(t.Context(), `SELECT definition_json -> ? FROM automation_definitions`, path).
			Scan(&got); err != nil ||
			got != want {
			t.Fatalf("%s=%s (%v)", path, got, err)
		}
	}
	var provenance string
	var initialized sql.NullString
	var bytes, records int
	if err := database.QueryRowContext(t.Context(), `SELECT data_evidence,value_initialization_json,value_evidence_bytes,value_evidence_records FROM automation_history WHERE id=?`, expressionUpgradeRun).
		Scan(&provenance, &initialized, &bytes, &records); err != nil ||
		provenance != "legacy_unavailable" ||
		initialized.Valid ||
		bytes != 0 ||
		records != 0 {
		t.Fatal("legacy values were fabricated")
	}
	if _, err := database.ExecContext(
		t.Context(),
		`UPDATE automation_definition_revisions SET definition_json='{}' WHERE revision=1`,
	); err == nil {
		t.Fatal("revision immutability lost")
	}
	if _, err := provider.DownTo(t.Context(), 22); err == nil {
		t.Fatal("lossy downgrade accepted")
	}
	for i, query := range queries {
		if expressionUpgradeRows(t, database, query) != before[i] {
			t.Fatal("failed downgrade changed retained rows")
		}
	}
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	var foreignKeys int
	if err := database.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).
		Scan(&foreignKeys); err != nil ||
		foreignKeys != 1 {
		t.Fatal("foreign keys disabled")
	}
	assertNoForeignKeyViolations(t.Context(), t, database)
	expressionUpgradeExec(t, database, `DELETE FROM automations WHERE id=?`, expressionUpgradeID)
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_step_executions`).
		Scan(&count); err != nil ||
		count != 6 {
		t.Fatal("head deletion removed history")
	}
	expressionUpgradeExec(t, database, `DELETE FROM automation_history WHERE id=?`, expressionUpgradeRun)
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_step_executions`).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatal("Run retention did not cascade")
	}
}

func TestExpressionMigrationMalformedArmsRollBackAllChanges(t *testing.T) {
	t.Parallel()
	for name, steps := range map[string]string{
		"duplicate Step ID":       `[{"id":"same","kind":"delay","duration_ms":1},{"id":"same","kind":"delay","duration_ms":2}]`,
		"empty then":              `[{"id":"i","kind":"if","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"then":[]}]`,
		"missing branch sequence": `[{"id":"c","kind":"choose","branches":[{}]}]`,
		"null else":               `[{"id":"i","kind":"if","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"then":[{"id":"d","kind":"delay","duration_ms":1}],"else":null}]`,
		"object default":          `[{"id":"c","kind":"choose","branches":[{"id":"b","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"steps":[{"id":"d","kind":"delay","duration_ms":1}]}],"default":{}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database := expressionUpgradeDatabase(t)
			document := `{"name":"corrupt","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":` + steps + `}`
			seedExpressionUpgrade(t, database, document)
			before := expressionUpgradeRows(
				t,
				database,
				`SELECT run_id,step_id,sequence,kind,phase,started_at,completed_at,evidence_json FROM automation_step_executions ORDER BY sequence`,
			)
			if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err == nil {
				t.Fatal("malformed nested arm migrated")
			}
			var stored string
			if err := database.QueryRowContext(t.Context(), `SELECT definition_json FROM automation_definition_revisions WHERE revision=1`).
				Scan(&stored); err != nil ||
				stored != document {
				t.Fatal("failed migration changed revision")
			}
			if expressionUpgradeRows(
				t,
				database,
				`SELECT run_id,step_id,sequence,kind,phase,started_at,completed_at,evidence_json FROM automation_step_executions ORDER BY sequence`,
			) != before {
				t.Fatal("failed migration changed trace")
			}
			assertColumnAbsent(t.Context(), t, database, "automation_history", "data_evidence")
			if _, err := database.ExecContext(
				t.Context(),
				`UPDATE automation_definition_revisions SET definition_json='{}' WHERE revision=1`,
			); err == nil {
				t.Fatal("rollback failed to restore immutability")
			}
			assertNoForeignKeyViolations(t.Context(), t, database)
		})
	}
}

func TestExpressionMigrationPreservesOldMaximumRevisionVerbatim(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	prefix := `{"name":"large","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"command","kind":"command","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":"`
	suffix := `"}}]}`
	document := prefix + strings.Repeat("x", 66080-len(prefix)-len(suffix)) + suffix
	seedExpressionUpgrade(t, database, document)
	if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err != nil {
		t.Fatal(err)
	}
	var upgraded string
	if err := database.QueryRowContext(t.Context(), `SELECT definition_json FROM automation_definition_revisions WHERE revision=1`).
		Scan(&upgraded); err != nil ||
		upgraded != document {
		t.Fatal("old maximum revision was rewritten")
	}
}

func TestExpressionMigrationRejectsMissingRequiredDefinitionFields(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	expressionUpgradeExec(t, database, `INSERT INTO automation_definition_revisions VALUES (?,1,?)`,
		expressionUpgradeID, `{"steps":[{"kind":"delay","duration_ms":1}]}`)
	if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err == nil {
		t.Fatal("malformed definition migrated")
	}
}

func TestExpressionMigrationRollsBackAfterRebuildStarts(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	step := `{"id":"leaf","kind":"delay","duration_ms":1}`
	for range 8 {
		step = `{"id":"branch","kind":"if","conditions":{"id":"yes","kind":"trigger","trigger_ids":["clock"]},"then":[` + step + `]}`
	}
	document := `{"name":"deep","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[` + step + `]}`
	seedExpressionUpgrade(t, database, document)
	const query = `SELECT run_id,step_id,sequence,kind,phase,started_at,completed_at,evidence_json FROM automation_step_executions ORDER BY sequence`
	before := expressionUpgradeRows(t, database, query)
	if _, err := newGooseProvider(
		t,
		database,
	).UpTo(t.Context(), 23); err == nil ||
		!strings.Contains(err.Error(), "CHECK constraint failed: valid = 1") {
		t.Fatalf("expected structural SQL check after rebuild began, got %v", err)
	}
	if expressionUpgradeRows(t, database, query) != before {
		t.Fatal("failed rebuild changed trace")
	}
	var retained string
	if err := database.QueryRowContext(t.Context(), `SELECT definition_json FROM automation_definition_revisions WHERE revision=1`).
		Scan(&retained); err != nil ||
		retained != document {
		t.Fatal("failed rebuild changed revision")
	}
	assertColumnAbsent(t.Context(), t, database, "automation_history", "data_evidence")
	assertNoForeignKeyViolations(t.Context(), t, database)
}

func TestExpressionMigrationPreservesHistoryAfterHeadDeletion(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	seedExpressionUpgrade(t, database, expressionUpgradeDocument())
	expressionUpgradeExec(t, database, `DELETE FROM automations WHERE id=?`, expressionUpgradeID)
	const traceQuery = `SELECT run_id,step_id,sequence,kind,phase,started_at,completed_at,evidence_json FROM automation_step_executions ORDER BY sequence`
	before := expressionUpgradeRows(t, database, traceQuery)
	if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err != nil {
		t.Fatal(err)
	}
	if after := expressionUpgradeRows(t, database, traceQuery); after != before {
		t.Fatal("headless Run trace changed")
	}
	var payload string
	if err := database.QueryRowContext(t.Context(), `SELECT definition_json -> '$.steps[0].then[0].parameters' FROM automation_definition_revisions WHERE automation_id=? AND revision=1`, expressionUpgradeID).
		Scan(&payload); err != nil ||
		payload != expressionUpgradePayload {
		t.Fatal("headless Run revision not preserved")
	}
	assertNoForeignKeyViolations(t.Context(), t, database)
}

//nolint:paralleltest,tparallel // Cases reuse one Run and insert the same sequence serially.
func TestExpressionMigrationExecutionFailureConstraints(t *testing.T) {
	t.Parallel()
	database := expressionUpgradeDatabase(t)
	seedExpressionUpgrade(t, database, expressionUpgradeDocument())
	if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, kind, phase, evidence string
		valid                       bool
	}{
		{"command missing IDs", "command", "failed", `{"reason_code":"failed"}`, false},
		{"command null failure", "command", "failed", `{"value_failure":null}`, false},
		{"command value failure", "command", "failed", `{"value_failure":{"failure_code":"invalid"}}`, true},
		{"notification null ID", "notification", "failed", `{"attempt_id":null,"reason_code":"failed"}`, false},
		{"notification value failure", "notification", "failed", `{"value_failure":{"failure_code":"invalid"}}`, true},
		{"function missing ID", "function", "failed", `{"reason_code":"failed"}`, false},
		{"function value failure", "function", "failed", `{"value_failure":{"failure_code":"invalid"}}`, true},
		{"read state value failure", "read_state", "failed", `{"value_failure":{"failure_code":"invalid"}}`, true},
		{"set variables success", "set_variables", "succeeded", `{}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := database.ExecContext(
				t.Context(),
				`INSERT INTO automation_step_executions VALUES (?,?,?,?,?,?,?,?)`,
				expressionUpgradeRun,
				"new",
				6,
				test.kind,
				test.phase,
				expressionUpgradeStamp,
				expressionUpgradeStamp,
				test.evidence,
			)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
			if err == nil {
				expressionUpgradeExec(
					t,
					database,
					`DELETE FROM automation_step_executions WHERE run_id=? AND step_id='new'`,
					expressionUpgradeRun,
				)
			}
		})
	}
}
