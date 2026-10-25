//go:build integration

package db //nolint:testpackage // Exercises the private embedded migration set at the v2 boundary.

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const expressionMigrationAutomation = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
const expressionMigrationRun = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
const expressionMigrationStamp = "2026-10-25T00:00:00.000000000Z"

//nolint:gocognit // One upgrade journey checks exact values, every child, failed downgrade and retention together.
func TestExpressionMigrationPreservesExactValuesAndEveryChild(t *testing.T) {
	t.Parallel()
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "expressions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 19); err != nil {
		t.Fatal(err)
	}
	// Provider lookalikes are data, including numbers beyond both JS and int64.
	const payload = `{"kind":"if","steps":[{"kind":"delay","duration_ms":12}],"expr":"not code","literal":9007199254740993,"huge":9223372036854775808,"fraction":1.2300e-4}`
	document := `{"name":"migration","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"0 18 * * *"}],"steps":[` +
		`{"id":"if","kind":"if","conditions":{"id":"t","kind":"trigger","trigger_ids":["clock"]},"then":[{"id":"command","kind":"command","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":` + payload + `}],"else":[{"id":"function","kind":"function","adapter_id":"weather","function_key":"forecast","contract_fingerprint":"sha256-v1:` + strings.Repeat(
		"0",
		64,
	) + `","arguments":` + payload + `}]},` +
		`{"id":"choose","kind":"choose","branches":[{"id":"one","conditions":{"id":"t","kind":"trigger","trigger_ids":["clock"]},"steps":[{"id":"notify","kind":"notification","destination_id":"ndst_01890f47-7a6b-7c4d-8e9f-0123456789ab","title":"quote\" and \\ newline\n","message":"expr literal"}]}],"default":[{"id":"delay","kind":"delay","duration_ms":1000}]}]}`
	insertExpressionLegacyParents(t, database, document)
	if _, err = database.ExecContext(
		t.Context(),
		`INSERT INTO automation_holds VALUES (?,7,'held',42,'pending',?,?)`,
		expressionMigrationAutomation,
		expressionMigrationStamp,
		"2026-10-25T00:00:10.000000000Z",
	); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO automation_run_steps(run_id,position,step_id,status) VALUES (?,0,'command','not_attempted')`,
		`INSERT INTO automation_run_branch_decisions(run_id,step_id,position,decision_json) VALUES (?,'if',0,'{}')`,
		`INSERT INTO automation_run_delays(run_id,step_id,position,status,started_at) VALUES (?,'delay',0,'running','stamp')`,
		`INSERT INTO automation_run_notifications(run_id,step_id,position,attempt_id,status,started_at) VALUES (?,'notify',0,'attempt','running','stamp')`,
		`INSERT INTO automation_run_functions(run_id,step_id,position,invocation_id,status,started_at) VALUES (?,'function',0,'invocation','running','stamp')`,
	} {
		if _, err = database.ExecContext(t.Context(), statement, expressionMigrationRun); err != nil {
			t.Fatal(err)
		}
	}
	before := expressionChildRows(t, database)
	if _, err = provider.UpTo(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	if after := expressionChildRows(t, database); after != before {
		t.Fatalf("children changed: %s -> %s", before, after)
	}
	assertNoForeignKeyViolations(t.Context(), t, database)
	var upgraded, snapshot, provenance, created, updated string
	var revision int
	if err = database.QueryRowContext(t.Context(), `SELECT definition_json,revision,created_at,updated_at FROM automations`).
		Scan(&upgraded, &revision, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(t.Context(), `SELECT run_snapshot_json,data_evidence FROM automation_history`).
		Scan(&snapshot, &provenance); err != nil {
		t.Fatal(err)
	}
	if snapshot != upgraded || revision != 7 || created != expressionMigrationStamp ||
		updated != expressionMigrationStamp ||
		provenance != "legacy_unavailable" {
		t.Fatal("identity/revision/time/provenance changed")
	}
	for _, path := range []string{"$.steps[0].then[0].parameters.literal", "$.steps[0].else[0].arguments.literal"} {
		var value string
		if err = database.QueryRowContext(t.Context(), `SELECT definition_json -> ? FROM automations`, path).
			Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != payload {
			t.Fatalf("opaque tokens changed: %s", value)
		}
	}
	for path, expected := range map[string]string{
		"$.steps[1].branches[0].steps[0].title.literal":   `"quote\" and \\ newline\n"`,
		"$.steps[1].branches[0].steps[0].message.literal": `"expr literal"`,
		"$.steps[1].default[0].duration_ms.literal":       `1000`,
	} {
		var value string
		if err = database.QueryRowContext(t.Context(), `SELECT definition_json -> ? FROM automations`, path).
			Scan(&value); err != nil ||
			value != expected {
			t.Fatalf("%s = %s (%v)", path, value, err)
		}
	}
	if _, err = provider.DownTo(t.Context(), 19); err == nil {
		t.Fatal("lossy downgrade accepted")
	}
	var retained string
	if err = database.QueryRowContext(t.Context(), `SELECT definition_json FROM automations`).
		Scan(&retained); err != nil ||
		retained != upgraded {
		t.Fatal("failed downgrade changed data")
	}
	if _, err = database.ExecContext(
		t.Context(),
		`INSERT INTO automation_run_data VALUES (?,0,NULL,'variables_initialized',?,'{"values":{}}')`,
		expressionMigrationRun,
		expressionMigrationStamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(
		t.Context(),
		`DELETE FROM automation_history WHERE id=?`,
		expressionMigrationRun,
	); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_run_data`).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatal("journal did not cascade")
	}
}

func insertExpressionLegacyParents(t *testing.T, database *sql.DB, document string) {
	t.Helper()
	if _, err := database.ExecContext(
		t.Context(),
		`INSERT INTO automations VALUES (?,7,?,?,?)`,
		expressionMigrationAutomation,
		document,
		expressionMigrationStamp,
		expressionMigrationStamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(
		t.Context(),
		`INSERT INTO automation_history(id,automation_id,automation_name,kind,revision,recorded_at,run_snapshot_json,run_source,run_status,run_started_at,run_matched_trigger_ids_json,condition_decision_json) VALUES (?,?,'migration','run',7,?,?,'manual','running',?,'[]','{"mode":"not_configured","bypass_requested":false}')`,
		expressionMigrationRun,
		expressionMigrationAutomation,
		expressionMigrationStamp,
		document,
		expressionMigrationStamp,
	); err != nil {
		t.Fatal(err)
	}
}

func expressionChildRows(t *testing.T, database *sql.DB) string {
	t.Helper()
	var all strings.Builder
	for _, table := range []string{"automation_run_steps", "automation_run_branch_decisions", "automation_run_delays", "automation_run_notifications", "automation_run_functions", "automation_holds"} {
		all.WriteString(expressionChildTableRows(t, database, table))
	}
	return all.String()
}

func expressionChildTableRows(t *testing.T, database *sql.DB, table string) string {
	t.Helper()
	var all strings.Builder
	//nolint:unqueryvet // Snapshot every persisted child column, including evidence added by later migrations.
	rows, err := database.QueryContext(t.Context(), "SELECT * FROM "+table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range targets {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		raw, marshalErr := json.Marshal(values)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		all.WriteString(table)
		all.Write(raw)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all.String()
}

//nolint:gocognit // At-limit success and corrupt-input rollback share the exact same migration boundary.
func TestExpressionMigrationNearLimitAndTransactionalFailure(t *testing.T) {
	t.Parallel()
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "near_limit", true: "corrupt_rolls_back"}[invalid], func(t *testing.T) {
			t.Parallel()
			database, err := Open(t.Context(), filepath.Join(t.TempDir(), "expressions.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			provider := newGooseProvider(t, database)
			if _, err = provider.UpTo(t.Context(), 19); err != nil {
				t.Fatal(err)
			}
			prefix := `{"name":"large","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"call","kind":"command","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":"`
			suffix := `"}}]}`
			document := prefix + strings.Repeat("x", 66080-len(prefix)-len(suffix)) + suffix
			if invalid {
				document = `{"steps":[{"kind":"command","parameters":false}]}`
			}
			insertExpressionLegacyParents(t, database, document)
			_, err = provider.UpTo(t.Context(), 20)
			if invalid && err == nil {
				t.Fatal("corrupt input accepted")
			}
			if !invalid && err != nil {
				t.Fatal(err)
			}
			var stored string
			if err = database.QueryRowContext(t.Context(), `SELECT definition_json FROM automations`).
				Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if invalid {
				if stored != document {
					t.Fatal("partial conversion")
				}
				assertColumnAbsent(t.Context(), t, database, "automation_history", "data_evidence")
			} else if len(stored) != 66092 || !strings.Contains(stored, `"parameters":{"literal":{"value":`) {
				t.Fatal("wrapper expansion changed old maximum document")
			}
			assertNoForeignKeyViolations(t.Context(), t, database)
		})
	}
}
