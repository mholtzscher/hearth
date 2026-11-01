//go:build integration

package mcpapi_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestAutomationMCPStoredCorruptionIsInternal(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		statement string
		tools     []string
	}{
		{"definition", `UPDATE automation_definition_revisions SET definition_json = '{"private_stored_field":"private_stored_value"}'`, []string{"list_automations", "get_automation", "get_automation_history_entry"}},
		{"lifecycle", `UPDATE automation_history SET run_status = 'private_stored_value'`, []string{"list_automation_history", "get_automation_history_entry"}},
		{"fact", `UPDATE automation_history SET run_source = 'device_fact', fact_id = 'private_stored_value', fact_family = 'observation', fact_entity_id = 'private_stored_value', fact_variant = 'applied', fact_causation_id = 'private_stored_value', fact_emitted_at = recorded_at, fact_value_json = 'true'`, []string{"list_automation_history", "get_automation_history_entry"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertStoredMCPReads(t, test.statement, test.tools)
		})
	}
}

func assertStoredMCPReads(t *testing.T, corruption string, tools []string) {
	t.Helper()
	database := openAutomationTestDatabase(t)
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	definition, err := automations.DecodeDefinition(json.RawMessage(definitionDocument(t, 1)))
	if err != nil {
		t.Fatal(err)
	}
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := entry.(automations.Run)
	var logs bytes.Buffer
	session := connectAutomationMCPWithLogs(
		t,
		automations.NewService(repository, newAPIDevices(), automations.Dependencies{}),
		&logs,
	)
	// Only this disposable fixture bypasses write guards to model a damaged store.
	for _, statement := range []string{"PRAGMA ignore_check_constraints = ON", "DROP TRIGGER automation_definition_revision_immutable", corruption} {
		if _, err = database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	arguments := map[string]map[string]any{
		"list_automations":             {},
		"get_automation":               {"automation_id": string(record.ID)},
		"list_automation_history":      {"automation_id": string(record.ID)},
		"get_automation_history_entry": {"automation_id": string(record.ID), "entry_id": string(run.ID)},
	}
	for _, tool := range tools {
		result := callAutomationTool(t, session, tool, arguments[tool])
		assertStoredMCPFailure(t, tool, result)
	}
	if strings.Contains(logs.String(), "private_stored") {
		t.Errorf("unsafe diagnostics: %s", logs.String())
	}
	if count := strings.Count(logs.String(), `"event":"mcp.tool_internal_failure"`); count != len(tools) {
		t.Errorf("internal diagnostic count = %d, want %d: %s", count, len(tools), logs.String())
	}
}

func assertStoredMCPFailure(t *testing.T, tool string, result *mcp.CallToolResult) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private_stored") {
		t.Errorf("stored cause leaked into result: %s", raw)
	}
	content := decodeStructuredInto[map[string]any](t, result)
	want := map[string]any{"failure_code": "internal_error", "message": "internal error"}
	if !result.IsError || toolErrorText(t, result) != `{"failure_code":"internal_error","message":"internal error"}` ||
		!reflect.DeepEqual(content, want) {
		t.Errorf("%s result = %s, want safe internal_error", tool, raw)
	}
}
