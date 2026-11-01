package httpapi //nolint:testpackage // Registered handlers own envelope-aware problem diagnostics.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type httpDefinitionDiagnosticCase struct {
	name, conditions, steps, path, code, step, condition string
	span                                                 map[string]any
	status                                               int
	location                                             string
}

func TestDefinitionDiagnosticsLocateHTTPRequestsWithoutValues(t *testing.T) {
	t.Parallel()
	const leaf = `{"id":"gate","kind":"entity_state","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value_pointer":"","operator":"gt","operand":true}`
	const delay = `{"id":"wait","kind":"delay","duration_ms":1}`
	function := `{"id":"lookup","kind":"function","adapter_id":"demo","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat(
		"0",
		64,
	) + `","arguments":{"SENSITIVE":true}}`
	for _, test := range []httpDefinitionDiagnosticCase{
		{"root", leaf, delay, "/conditions", "invalid_condition", "", "gate", nil, 400, ""},
		{"nested", `{"id":"all","kind":"all","children":[` + leaf + `]}`, delay, "/conditions/children/0", "invalid_condition", "", "gate", nil, 400, ""},
		{"Unicode syntax", "", `{"id":"branch","kind":"if","conditions":{"id":"syntax","kind":"expression","expr":"'私' + )"},"then":[` + delay + `]}`, "/steps/0/conditions/expr", "expression_type", "branch", "syntax", map[string]any{"start": float64(8), "end": float64(9)}, 400, ""},
		{"escaped nested syntax", "", `{"id":"branch","kind":"if","conditions":{"id":"all","kind":"all","children":[{"id":"not","kind":"not","child":{"id":"syntax","kind":"expression","expr":"'\u79c1' + )"}}]},"then":[` + delay + `]}`, "/steps/0/conditions/children/0/child/expr", "expression_type", "branch", "syntax", map[string]any{"start": float64(8), "end": float64(9)}, 400, ""},
		{"Function", "", function, "/steps/0", "function_not_found", "lookup", "", nil, 422, "body.steps[0]"},
		{"nested Function", "", `{"id":"branch","kind":"if","conditions":{"id":"yes","kind":"expression","expr":"true"},"then":[` + function + `]}`, "/steps/0/then/0", "function_not_found", "lookup", "", nil, 422, "body.steps[0].then[0]"},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(test.name+"/"+method, func(t *testing.T) {
				t.Parallel()
				exerciseHTTPDefinitionDiagnostic(t, method, test)
			})
		}
	}
}

func TestDefinitionDiagnosticPathsDoNotEchoInvalidNames(t *testing.T) {
	t.Parallel()
	for _, fields := range []string{
		`,"variables_expr":{"SENSITIVE/invalid":"true"}`,
		`,"SENSITIVE-property":{"secret":true}`,
		`,"conditions":{"id":"SENSITIVE/invalid","kind":"entity_state","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value_pointer":"","operator":"eq","operand":true}`,
	} {
		t.Run(fields, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Privacy", "test"))
			Register(api, nil, nil)
			body := `{"name":"Valid","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]` + fields + `}`
			request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(body))
			request.Header.Set("Content-Type", jsonContentType)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 400 || strings.Contains(response.Body.String(), "SENSITIVE") {
				t.Fatalf("unsafe schema diagnostic: %d %s", response.Code, response.Body.String())
			}
			var problem map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem["diagnostics"] == nil {
				t.Fatalf("missing safe diagnostic: %s", response.Body.String())
			}
			if err := automationErrorSchema(
				t,
				api,
				"/automations",
				http.MethodPost,
				400,
			).Validate(problem); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDefinitionShapeDiagnosticsListAllowedValues(t *testing.T) {
	t.Parallel()
	const command = `{"id":"on_fan","kind":"command","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":true}`
	for _, test := range []struct {
		name, step string
		want       map[string]any
	}{
		{"unknown Command property", command + `,"when":{"trigger_id":"single_press"}}`, map[string]any{
			"code": "unknown_property", "path": "/steps/0",
			"allowed": []any{"entity_id", "id", "kind", "operation", "parameters", "parameters_expr"},
		}},
		{"unknown Step kind", `{"id":"single_on","kind":"branch","then":[` + command + `}]}`, map[string]any{
			"code": "unknown_kind", "path": "/steps/0/kind",
			"allowed": []any{"command", "if", "choose", "delay", "notification", "function", "set_variables", "read_state"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Diagnostics", "test"))
			Register(api, nil, nil)
			body := `{"name":"Office button","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[` + test.step + `]}`
			request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(body))
			request.Header.Set("Content-Type", jsonContentType)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertDefinitionHTTPDiagnostic(t, response, 400, test.want)
			var problem map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if err := automationErrorSchema(
				t,
				api,
				"/automations",
				http.MethodPost,
				400,
			).Validate(problem); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func exerciseHTTPDefinitionDiagnostic(t *testing.T, method string, test httpDefinitionDiagnosticCase) {
	t.Helper()
	definition := `{"name":"SENSITIVE-name","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[` + test.steps + `]`
	if test.conditions != "" {
		definition += `,"conditions":` + test.conditions
	}
	definition += `}`
	path, schemaPath, prefix, body := "/automations", "/automations", "", definition
	if method == http.MethodPut {
		path += "/aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		schemaPath, prefix, body = "/automations/{automation_id}", "/definition", `{"expected_revision":1,"definition":`+definition+`}`
	}
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Diagnostics", "test"))
	service := automations.NewService(
		nil,
		&diagnosticDefinitionDevices{},
		automations.Dependencies{Functions: &diagnosticDefinitionFunctions{}},
	)
	Register(api, service, nil)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	want := map[string]any{"code": test.code, "path": prefix + test.path}
	if test.step != "" {
		want["step_id"] = test.step
	}
	if test.condition != "" {
		want["condition_id"] = test.condition
	}
	if test.span != nil {
		want["span"] = test.span
	}
	assertDefinitionHTTPDiagnostic(t, response, test.status, want)
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if test.location != "" {
		location := test.location
		if method == http.MethodPut {
			location = strings.Replace(location, "body.", "body.definition.", 1)
		}
		wantErrors := []any{
			map[string]any{"location": location, "message": "step lookup: function_not_found"},
		}
		if !reflect.DeepEqual(problem["errors"], wantErrors) {
			t.Errorf("Function errors=%v want=%v", problem["errors"], wantErrors)
		}
	}
	if err := automationErrorSchema(t, api, schemaPath, method, test.status).Validate(problem); err != nil {
		t.Fatal(err)
	}
}

func assertDefinitionHTTPDiagnostic(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	want map[string]any,
) {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	code := "invalid_automation"
	if status == 422 {
		code = "invalid_function_reference"
	}
	if response.Code != status || problem["code"] != code ||
		response.Header().Get("Content-Type") != problemContentType {
		t.Fatalf("incorrect problem: %d %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(problem["diagnostics"], []any{want}) {
		t.Errorf("diagnostics=%v want=%v; body=%s", problem["diagnostics"], want, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("SENSITIVE")) ||
		bytes.Contains(response.Body.Bytes(), []byte("私")) {
		t.Fatal("source or input value leaked")
	}
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
