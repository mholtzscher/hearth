//go:build integration

package db //nolint:testpackage // Exercises the embedded version 24 migration boundary.

import (
	"database/sql"
	"strings"
	"testing"
)

const valueContractDocument = `{"name":"test","variables_expr":{"x":"1"},"steps":[{"id":"outer","kind":"if","conditions":{"id":"same","kind":"expression","expr":"false"},"then":[{"id":"choose","kind":"choose","branches":[{"id":"first","conditions":{"id":"same","kind":"expression","expr":"false"},"steps":[]},{"id":"second","conditions":{"id":"root","kind":"all","children":[{"id":"same","kind":"expression","expr":"x"}]},"steps":[]}]}]},{"id":"command","kind":"command","parameters_expr":"x"},{"id":"state","kind":"read_state"},{"id":"function","kind":"function","arguments":{}}]}`

func valueContractDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := expressionUpgradeDatabase(t)
	if _, err := newGooseProvider(t, database).UpTo(t.Context(), 23); err != nil {
		t.Fatal(err)
	}
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_definition_revisions VALUES (?,1,?),(?,2,?)`,
		expressionUpgradeID,
		valueContractDocument,
		expressionUpgradeID,
		`{"steps":[]}`,
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
		`INSERT INTO automation_history (id,automation_id,automation_name,kind,revision,recorded_at,definition_revision,run_source,run_status,run_started_at,run_matched_trigger_ids_json,condition_decision_json,data_evidence,value_initialization_json,value_evidence_bytes,value_evidence_records) VALUES (?,?,'test','run',1,?,1,'manual','running',?,'[]','{"mode":"not_configured","bypass_requested":false}','recorded',?,42,3)`,
		expressionUpgradeRun,
		expressionUpgradeID,
		expressionUpgradeStamp,
		expressionUpgradeStamp,
		`{"failure":{"path":"/variables_expr/x","failure_code":"expression_type","source_start":1,"source_end":2},"input":9007199254740993,"text":"a\\b\"c"}`,
	)
	return database
}

func valueContractStep(t *testing.T, database *sql.DB, step, kind, evidence string, sequence int) {
	t.Helper()
	expressionUpgradeExec(
		t,
		database,
		`INSERT INTO automation_step_executions VALUES (?,?,?,?,'failed',?,?,?)`,
		expressionUpgradeRun,
		step,
		sequence,
		kind,
		expressionUpgradeStamp,
		expressionUpgradeStamp,
		evidence,
	)
}

func TestValueContractMigrationPreservesEvidence(t *testing.T) {
	t.Parallel()
	database := valueContractDatabase(t)
	valueContractStep(
		t,
		database,
		"outer",
		"if",
		`{"outcome":"error","evaluations":[],"failure_code":"expression_type","value_failure":{"path":"/steps/0/conditions/expr","failure_code":"expression_type","source_start":0,"source_end":1}}`,
		0,
	)
	valueContractStep(
		t,
		database,
		"choose",
		"choose",
		`{"outcome":"error","evaluations":[{"branch_id":"first","evaluation":{"value":9007199254740993}}],"failure_code":"expression_type","value_failure":{"path":"/steps/0/then/0/branches/1/conditions/children/0/expr","failure_code":"expression_type"}}`,
		1,
	)
	valueContractStep(
		t,
		database,
		"command",
		"command",
		`{"input":{"number":9007199254740993,"text":"a\\b\"c"},"value_failure":{"path":"/steps/1/parameters_expr","failure_code":"expression_type"}}`,
		2,
	)
	valueContractStep(
		t,
		database,
		"state",
		"read_state",
		`{"value_failure":{"path":"/steps/2","failure_code":"state_read_failed","state":{"number":9007199254740993}}}`,
		3,
	)
	valueContractStep(
		t,
		database,
		"function",
		"function",
		`{"value_failure":{"path":"/steps/3","failure_code":"run_data_limit"}}`,
		4,
	)
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		query string
		want  []string
	}{
		{
			`SELECT value_initialization_json FROM automation_history WHERE id='` + expressionUpgradeRun + `'`,
			[]string{`"field":"variables.x"`, `9007199254740993`, `a\\b\"c`},
		},
		{
			`SELECT evidence_json FROM automation_step_executions WHERE step_id='outer'`,
			[]string{`"condition_id":"same"`},
		},
		{
			`SELECT evidence_json FROM automation_step_executions WHERE step_id='choose'`,
			[]string{`"condition_id":"same"`, `9007199254740993`, `"branch_id":"first"`},
		},
		{
			`SELECT evidence_json FROM automation_step_executions WHERE step_id='command'`,
			[]string{`"field":"parameters"`, `9007199254740993`, `a\\b\"c`},
		},
		{
			`SELECT evidence_json FROM automation_step_executions WHERE step_id='state'`,
			[]string{`"state":{"number":9007199254740993}`},
		},
		{
			`SELECT evidence_json FROM automation_step_executions WHERE step_id='function'`,
			[]string{`"failure_code":"run_data_limit"`},
		},
	}
	for _, check := range checks {
		var got string
		if err := database.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		for _, want := range check.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %s", got, want)
			}
		}
		if strings.Contains(got, `"path"`) || strings.Contains(got, `"source_start"`) ||
			strings.Contains(got, `"source_end"`) {
			t.Errorf("old diagnostic retained: %s", got)
		}
	}
	var columns int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('automation_history') WHERE name IN ('value_evidence_bytes','value_evidence_records')`).
		Scan(&columns); err != nil ||
		columns != 0 {
		t.Fatalf("columns=%d err=%v", columns, err)
	}
	var revision string
	if err := database.QueryRowContext(t.Context(), `SELECT definition_json FROM automation_definition_revisions WHERE revision=1`).
		Scan(&revision); err != nil ||
		revision != valueContractDocument {
		t.Fatalf("revision changed: %v", err)
	}
	var violations int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).
		Scan(&violations); err != nil ||
		violations != 0 {
		t.Fatalf("foreign keys: %d %v", violations, err)
	}
	if _, err := newGooseProvider(
		t,
		database,
	).Down(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "pre-upgrade backup") {
		t.Fatalf("down: %v", err)
	}
}

func TestValueContractMigrationRejectsInvalidPathAtomically(t *testing.T) {
	t.Parallel()
	database := valueContractDatabase(t)
	valueContractStep(
		t,
		database,
		"command",
		"command",
		`{"value_failure":{"path":"/steps/1/unknown","failure_code":"expression_type"}}`,
		0,
	)
	if err := Migrate(t.Context(), database); err == nil {
		t.Fatal("expected migration failure")
	}
	var initialization string
	if err := database.QueryRowContext(t.Context(), `SELECT value_initialization_json FROM automation_history`).
		Scan(&initialization); err != nil ||
		!strings.Contains(initialization, `"path":"/variables_expr/x"`) {
		t.Fatalf("initialization changed: %s %v", initialization, err)
	}
	var columns int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('automation_history') WHERE name IN ('value_evidence_bytes','value_evidence_records')`).
		Scan(&columns); err != nil ||
		columns != 2 {
		t.Fatalf("columns=%d err=%v", columns, err)
	}
}

func TestValueContractHistoricalPathForms(t *testing.T) {
	t.Parallel()
	definition := `{"variables":{"x":1},"variables_expr":{"y":"1"},"steps":[{"id":"set","kind":"set_variables","assignments":{"x":1},"assignments_expr":{"y":"1"}},{"id":"branch","kind":"if","conditions":{"id":"c","kind":"expression","expr":"true"},"then":[]}]}`
	for _, tc := range []struct{ step, kind, path, code, field string }{
		{"", "", "/trigger", "expression_type", "trigger"},
		{"", "", "/variables", "run_data_limit", "variables"},
		{"", "", "/variables/x", "expression_type", "variables.x"},
		{"", "", "/variables_expr/y", "expression_type", "variables.y"},
		{"set", "set_variables", "/steps/0/assignments", "run_data_limit", "assignments"},
		{"set", "set_variables", "/steps/0/assignments/x", "expression_type", "assignments.x"},
		{"set", "set_variables", "/steps/0/assignments_expr/y", "expression_type", "assignments.y"},
		{"branch", "if", "/steps/1", "run_data_limit", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			c := valueContractCandidate{
				step:       tc.step,
				kind:       tc.kind,
				definition: definition,
				evidence:   `{"evaluations":[]}`,
				failure:    `{"path":"` + tc.path + `","failure_code":"` + tc.code + `"}`,
			}
			got, err := convertValueContractFailure(c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.field == "" {
				if strings.Contains(got, `"field"`) || strings.Contains(got, `"condition_id"`) {
					t.Fatal(got)
				}
			} else if !strings.Contains(got, `"field":"`+tc.field+`"`) {
				t.Fatal(got)
			}
		})
	}
}
