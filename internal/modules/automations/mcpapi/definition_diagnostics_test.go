package mcpapi //nolint:testpackage // The registered tools own diagnostic details independently of HTTP.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type mcpDefinitionDiagnosticCase struct {
	name, conditions, steps, path, code, step, condition string
	span                                                 map[string]any
}

func TestDefinitionDiagnosticsLocateMCPArgumentsWithoutValues(t *testing.T) {
	t.Parallel()
	const leaf = `{"id":"gate","kind":"entity_state","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value_pointer":"","operator":"gt","operand":true}`
	const delay = `{"id":"wait","kind":"delay","duration_ms":1}`
	for _, test := range []mcpDefinitionDiagnosticCase{
		{"root", leaf, delay, "/conditions", "invalid_condition", "", "gate", nil},
		{"nested", `{"id":"all","kind":"all","children":[` + leaf + `]}`, delay, "/conditions/children/0", "invalid_condition", "", "gate", nil},
		{"Unicode syntax", "", `{"id":"branch","kind":"if","conditions":{"id":"syntax","kind":"expression","expr":"'私' + )"},"then":[` + delay + `]}`, "/steps/0/conditions/expr", "expression_type", "branch", "syntax", map[string]any{"start": float64(8), "end": float64(9)}},
		{"Function", "", `{"id":"lookup","kind":"function","adapter_id":"demo","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat("0", 64) + `","arguments":{"SENSITIVE":true}}`, "/steps/0", "function_not_found", "lookup", "", nil},
		{"nested Function", "", `{"id":"branch","kind":"if","conditions":{"id":"yes","kind":"expression","expr":"true"},"then":[{"id":"lookup","kind":"function","adapter_id":"demo","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat("0", 64) + `","arguments":{"SENSITIVE":true}}]}`, "/steps/0/then/0", "function_not_found", "lookup", "", nil},
	} {
		for _, tool := range []string{"create_automation", "replace_automation"} {
			t.Run(test.name+"/"+tool, func(t *testing.T) {
				t.Parallel()
				exerciseMCPDefinitionDiagnostic(t, tool, test)
			})
		}
	}
}

func exerciseMCPDefinitionDiagnostic(t *testing.T, tool string, test mcpDefinitionDiagnosticCase) {
	t.Helper()
	definition := `{"name":"SENSITIVE-name","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[` + test.steps + `]`
	if test.conditions != "" {
		definition += `,"conditions":` + test.conditions
	}
	definition += `}`
	arguments := `{"definition":` + definition
	if tool == "replace_automation" {
		arguments += `,"automation_id":"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab","expected_revision":1`
	}
	arguments += `}`
	server := mcptools.New(mcptools.Config{Name: "Diagnostics", Version: "test"})
	service := automations.NewService(
		nil,
		&diagnosticDefinitionDevices{},
		automations.Dependencies{Functions: &diagnosticDefinitionFunctions{}},
	)
	Register(server, service)
	result := definitionDiagnosticCall(t, server, tool, arguments)
	details := result
	want := map[string]any{"code": test.code, "path": "/definition" + test.path}
	if test.step != "" {
		want["step_id"] = test.step
	}
	if test.condition != "" {
		want["condition_id"] = test.condition
	}
	if test.span != nil {
		want["span"] = test.span
	}
	if !reflect.DeepEqual(details["diagnostics"], []any{want}) {
		t.Errorf("details=%v want diagnostics=%v", details, want)
	}
	if test.code != "function_not_found" && details["path"] != test.path {
		t.Errorf("legacy path changed: %v", details)
	}
}

func definitionDiagnosticCall(t *testing.T, server *mcptools.Server, tool, arguments string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+arguments+`}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	for line := range strings.SplitSeq(payload, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			break
		}
	}
	var wire struct {
		Result struct {
			IsError    bool           `json:"isError"`
			Structured map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.Result.IsError || wire.Result.Structured["failure_code"] != "invalid_automation" {
		t.Fatalf("unexpected tool failure: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "SENSITIVE") || strings.Contains(response.Body.String(), "私") {
		t.Fatal("source or input value leaked")
	}
	return wire.Result.Structured
}

type diagnosticDefinitionDevices struct{ automations.AutomationDevices }
type diagnosticDefinitionFunctions struct {
	automations.AutomationFunctions
}

func (*diagnosticDefinitionFunctions) ValidateContractInput(
	context.Context,
	adapters.ContractInput,
) (json.RawMessage, error) {
	return nil, adapters.ErrFunctionNotFound
}
