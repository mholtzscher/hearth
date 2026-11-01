//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

func TestAutomationMCPServerPreconditionsAreInternal(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"devices", "notifications", "functions"} {
		for _, tool := range []string{"create_automation", "replace_automation"} {
			t.Run(missing+"/"+tool, func(t *testing.T) {
				t.Parallel()
				assertMissingValidationMCP(t, missing, tool)
			})
		}
	}
}

func assertMissingValidationMCP(t *testing.T, missing, tool string) {
	t.Helper()
	var validation automations.AutomationDevices = newAPIDevices()
	step := `{"id":"wait","kind":"delay","duration_ms":1}`
	switch missing {
	case "devices":
		validation = nil
	case "notifications":
		step = `{"id":"private-notify","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":"","message":"private message"}`
	case "functions":
		step = `{"id":"private-function","kind":"function","adapter_id":"test-functions","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat(
			"a",
			64,
		) + `","arguments":{}}`
	}
	definition := definitionArguments(
		t,
		`{"name":"Private fixture","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[`+step+`]}`,
	)
	session := connectAutomationMCP(
		t,
		automations.NewService(&preconditionWriteTrap{test: t}, validation, automations.Dependencies{}),
	)
	arguments := map[string]any{"definition": definition}
	if tool == "replace_automation" {
		arguments["automation_id"] = "aut_01980000-0000-7000-8000-000000000001"
		arguments["expected_revision"] = 1
	}
	assertServerMCPFailure(t, callAutomationTool(t, session, tool, arguments))
}

type preconditionWriteTrap struct {
	automations.Repository

	test *testing.T
}

func (repo *preconditionWriteTrap) CreateAutomation(
	context.Context,
	automations.Definition,
) (automations.Record, error) {
	repo.test.Error("server precondition failure reached a definition write")
	return automations.Record{}, errors.New("unexpected definition write")
}

func (repo *preconditionWriteTrap) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.Definition,
) (automations.Record, error) {
	repo.test.Error("server precondition failure reached a definition write")
	return automations.Record{}, errors.New("unexpected definition write")
}

func TestAutomationMCPZeroCoreAdmissionTimeIsInternal(t *testing.T) {
	t.Parallel()
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
	service := automations.NewService(
		repository,
		newAPIDevices(),
		automations.Dependencies{Now: func() time.Time { return time.Time{} }},
	)
	session := connectAutomationMCP(t, service)
	assertServerMCPFailure(
		t,
		callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": string(record.ID)}),
	)
	var count int
	if err = database.QueryRowContext(t.Context(), "SELECT count(*) FROM automation_history").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed precondition wrote %d history entries", count)
	}
}

func assertServerMCPFailure(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	content := decodeStructuredInto[map[string]any](t, result)
	want := map[string]any{"failure_code": "internal_error", "message": "internal error"}
	if !result.IsError || toolErrorText(t, result) != `{"failure_code":"internal_error","message":"internal error"}` ||
		!reflect.DeepEqual(content, want) {
		raw, _ := json.Marshal(result)
		t.Errorf("server precondition result=%s, want safe internal_error", raw)
	}
}
