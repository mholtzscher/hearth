//go:build integration

package sqlite_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// seedHistoricalRun uses historical SQL deliberately: current repository queries
// cannot run against a partially migrated database. Its documents are v2, not v3
// bindings smuggled into a pre-expression schema.
func seedHistoricalRun(
	t *testing.T,
	database *sql.DB,
	definition automations.Definition,
	at time.Time,
) (automations.Record, automations.Run) {
	t.Helper()
	raw := historicalDefinition(t, definition)
	record := automations.Record{
		ID:         automations.AutomationID(newAutomationIDString(t)),
		Revision:   1,
		Definition: definition,
	}
	run := automations.Run{
		ID:           automations.RunID(newRunIDString(t)),
		AutomationID: record.ID,
		Snapshot:     definition,
		StartedAt:    at,
	}
	mustExec(
		t,
		database,
		`INSERT INTO automations (id, revision, definition_json, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`,
		string(record.ID),
		string(raw),
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(t, database, insertHistoryRunSQL, string(run.ID), string(record.ID), "running", nil, nil, "[]")
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`,
		string(raw),
		string(run.ID),
	)
	for position, step := range automations.CommandLeaves(definition.Steps) {
		mustExec(
			t,
			database,
			`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, ?, ?, 'not_attempted')`,
			string(run.ID),
			position,
			string(step.ID),
		)
	}
	return record, run
}

//nolint:gocognit // Historical fixture conversion unwraps only binding fields and leaves opaque literal contents untouched.
func historicalDefinition(t *testing.T, definition automations.Definition) json.RawMessage {
	t.Helper()
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	var unwrap func(any)
	unwrap = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "parameters" || key == "arguments" || key == "title" || key == "message" ||
					key == "duration_ms" {
					if binding, ok := child.(map[string]any); ok {
						if literal, exists := binding["literal"]; exists {
							node[key] = literal
							continue
						}
					}
				}
				unwrap(child)
			}
		case []any:
			for _, child := range node {
				unwrap(child)
			}
		}
	}
	unwrap(document)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
