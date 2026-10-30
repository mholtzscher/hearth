package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationshttp "github.com/mholtzscher/hearth/internal/modules/automations/httpapi"
	automationsmcp "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestFunctionSaveOperationalFailuresStayInternalOnWire(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cause error
	}{
		{"lookup storage", errors.New("SENSITIVE-storage")},
		{"runtime seam", errors.Join(adapters.ErrAdapterUnavailable, errors.New("SENSITIVE-runtime"))},
		{"persistence and invalid", errors.Join(adapters.ErrFunctionPersistence, adapters.ErrInvalidFunctionInput)},
		{"canceled and missing", errors.Join(context.Canceled, adapters.ErrFunctionNotFound)},
	} {
		for _, arguments := range []string{`"arguments":{"SENSITIVE":9007199254740993}`, `"arguments_expr":"{value: vars.target}"`} {
			for _, replace := range []bool{false, true} {
				t.Run(
					test.name+"/"+arguments+"/"+map[bool]string{false: "create", true: "replace"}[replace],
					func(t *testing.T) {
						t.Parallel()
						definition := `{"name":"Private","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"lookup","kind":"function","adapter_id":"demo","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat(
							"0",
							64,
						) + `",` + arguments + `}]}`
						service := automations.NewService(
							nil,
							&saveProtocolDevices{},
							automations.Dependencies{Functions: &saveProtocolFunctions{cause: test.cause}},
						)
						assertFunctionSaveHTTP(t, service, definition, replace)
						assertFunctionSaveMCP(t, service, definition, replace)
					},
				)
			}
		}
	}
}

func assertFunctionSaveHTTP(t *testing.T, service *automations.Service, definition string, replace bool) {
	t.Helper()
	var logs bytes.Buffer
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Save", "test"))
	automationshttp.Register(api, service, slog.New(slog.NewJSONHandler(&logs, nil)))
	method, path, body := http.MethodPost, "/automations", definition
	if replace {
		method, path, body = http.MethodPut, "/automations/aut_01890f47-7a6b-7c4d-8e9f-0123456789ab", `{"expected_revision":1,"definition":`+definition+`}`
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if response.Code != 500 || problem["code"] != "internal_error" || problem["detail"] != "internal error" ||
		len(problem) != 5 {
		t.Fatalf("not a safe internal problem: %d %s", response.Code, response.Body.String())
	}
	assertFunctionSavePrivacy(t, response.Body.String(), logs.Bytes(), "automation.http_internal_failure")
}

func assertFunctionSaveMCP(t *testing.T, service *automations.Service, definition string, replace bool) {
	t.Helper()
	var logs bytes.Buffer
	server := mcptools.New(
		mcptools.Config{Name: "Save", Version: "test", Logger: slog.New(slog.NewJSONHandler(&logs, nil))},
	)
	automationsmcp.Register(server, service)
	tool, arguments := "create_automation", `{"definition":`+definition+`}`
	if replace {
		tool, arguments = "replace_automation", `{"automation_id":"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab","expected_revision":1,"definition":`+definition+`}`
	}
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
	if !wire.Result.IsError || wire.Result.Structured["failure_code"] != "internal_error" ||
		wire.Result.Structured["message"] != "internal error" ||
		len(wire.Result.Structured) != 2 {
		t.Fatalf("not a safe internal MCP failure: %s", payload)
	}
	assertFunctionSavePrivacy(t, response.Body.String(), logs.Bytes(), "mcp.tool_internal_failure")
}

func assertFunctionSavePrivacy(t *testing.T, body string, logs []byte, event string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(logs, &record); err != nil {
		t.Fatalf("want one diagnostic: %v", err)
	}
	if record["event"] != event || record["error_code"] != "internal_error" {
		t.Fatalf("wrong diagnostic: %v", record)
	}
	for _, private := range []string{"SENSITIVE", "9007199254740993", "vars.target", "step_id", "reason_code"} {
		if strings.Contains(body+string(logs), private) {
			t.Fatalf("private data leaked: %s", private)
		}
	}
}

type saveProtocolDevices struct{ automations.AutomationDevices }
type saveProtocolFunctions struct {
	automations.AutomationFunctions

	cause error
}

func (functions *saveProtocolFunctions) ValidateContractReference(context.Context, adapters.ContractReference) error {
	return functions.cause
}

func (functions *saveProtocolFunctions) ValidateContractInput(
	context.Context,
	adapters.ContractInput,
) (json.RawMessage, error) {
	return nil, functions.cause
}
