package httpapi //nolint:testpackage // Pin the transport owner's error translation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestDomainErrorProblems(t *testing.T) {
	t.Parallel()
	diagnostics := map[string][]*huma.ErrorDetail{
		"function": {{Location: "body.steps", Message: "step lookup: function_missing"}},
	}
	for _, test := range []struct {
		name, code, detail string
		err                error
		status             int
	}{
		{"missing automation", "not_found", "automation resource not found", automations.ErrAutomationNotFound, 404},
		{"missing history", "not_found", "automation resource not found", automations.ErrHistoryNotFound, 404},
		{"revision", "revision_conflict", "automation revision conflict", automations.ErrRevisionConflict, 409},
		{"busy", "automation_busy", "automation already has a running run", automations.ErrAutomationBusy, 409},
		{"blocked sentinel", "conditions_blocked", "automation conditions prevented manual admission", automations.ErrAutomationConditionsBlocked, 409},
		{"snapshot", "condition_snapshot_unavailable", "automation condition state is unavailable", automations.ErrConditionSnapshotRequired, 503},
		{"admission", "admission_unavailable", "automation admission is unavailable", automations.ErrAdmissionUnavailable, 503},
		{"invalid", "invalid_automation", "invalid automation request", automations.ErrInvalidAutomation, 400},
		{"definition path", "invalid_automation", "invalid automation request at /steps/0", &automations.DefinitionError{Issues: []automations.DefinitionIssue{{Path: "/steps/0", Message: "private definition"}}}, 400},
		{"definition without path", "invalid_automation", "invalid automation request", &automations.DefinitionError{}, 400},
		{"function", "invalid_function_reference", "invalid automation Function reference", &automations.FunctionReferenceError{StepID: "lookup", ReasonCode: "function_missing"}, 422},
		{"unknown", "internal_error", "internal error", errors.New("private database failure"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			problem := mapDomainError(fmt.Errorf("private wrapper: %w", test.err))
			status, ok := errors.AsType[huma.StatusError](problem)
			if !ok || status.GetStatus() != test.status {
				t.Fatalf("unexpected problem: %#v", problem)
			}
			raw, err := json.Marshal(problem)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Type   string              `json:"type"`
				Title  string              `json:"title"`
				Detail string              `json:"detail"`
				Code   string              `json:"code"`
				Status int                 `json:"status"`
				Errors []*huma.ErrorDetail `json:"errors"`
			}
			if err = json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if body.Type != "about:blank" || body.Status != test.status || body.Title != http.StatusText(test.status) ||
				body.Detail != test.detail ||
				body.Code != test.code {
				t.Fatalf("unexpected body: %s", raw)
			}
			if !reflect.DeepEqual(body.Errors, diagnostics[test.name]) {
				t.Fatalf("unexpected diagnostics: %s", raw)
			}
		})
	}
}

func TestHTTPErrorResponsesMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	const valid = `{"name":"Errors","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`
	const automationPath = "/automations/aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	for _, test := range []struct {
		name, body, code         string
		err                      error
		status                   int
		method, path, schemaPath string
	}{
		{"invalid", valid, "invalid_automation", automations.ErrInvalidAutomation, 400, "POST", "/automations", "/automations"},
		{"function", valid, "invalid_function_reference", &automations.FunctionReferenceError{StepID: "lookup", ReasonCode: "function_missing"}, 422, "POST", "/automations", "/automations"},
		{"internal", valid, "internal_error", errors.New("private database failure"), 500, "POST", "/automations", "/automations"},
		{"framework validation", "{", "", nil, 422, "POST", "/automations", "/automations"},
		{"framework size", strings.Repeat(" ", automationRequestMaxBytes+1), "", nil, 413, "POST", "/automations", "/automations"},
		{"replace conflict", `{"expected_revision":1,"definition":` + valid + `}`, "revision_conflict", automations.ErrRevisionConflict, 409, "PUT", automationPath, "/automations/{automation_id}"},
		{"delete conflict", "", "revision_conflict", automations.ErrRevisionConflict, 409, "DELETE", automationPath + "?expected_revision=1", "/automations/{automation_id}"},
		{"missing definition", "", "not_found", automations.ErrAutomationNotFound, 404, "GET", automationPath, "/automations/{automation_id}"},
		{"missing history entry", "", "not_found", automations.ErrHistoryNotFound, 404, "GET", automationPath + "/history/ask_01890f47-7a6b-7c4d-8e9f-0123456789ab", "/automations/{automation_id}/history/{entry_id}"},
		{"manual busy", "", "automation_busy", automations.ErrAutomationBusy, 409, "POST", automationPath + "/runs", "/automations/{automation_id}/runs"},
		{"manual unavailable", "", "admission_unavailable", automations.ErrAdmissionUnavailable, 503, "POST", automationPath + "/runs", "/automations/{automation_id}/runs"},
		{"manual snapshot", "", "condition_snapshot_unavailable", automations.ErrConditionSnapshotRequired, 503, "POST", automationPath + "/runs", "/automations/{automation_id}/runs"},
		{"manual linked conflict", "", "conditions_false", &automations.ConditionsBlockedError{
			AutomationID: "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab", SkipID: "ask_01890f47-7a6b-7c4d-8e9f-0123456789ab", Reason: automations.SkipConditionsFalse,
		}, 409, "POST", automationPath + "/runs", "/automations/{automation_id}/runs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Errors", "test"))
			Register(api, &errorContractService{err: test.err}, nil)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", jsonContentType)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != problemContentType {
				t.Fatalf("unexpected response: %d %v %s", response.Code, response.Header(), response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if code, _ := body["code"].(string); code != test.code ||
				strings.Contains(response.Body.String(), "private") {
				t.Fatalf("incorrect code or leaked details: %s", response.Body.String())
			}
			assertAutomationProblemHistory(t, body, test.err)
			schema := automationErrorSchema(t, api, test.schemaPath, test.method, test.status)
			if err := schema.Validate(body); err != nil {
				t.Fatalf("response does not match published schema: %v; %s", err, response.Body.String())
			}
			body["undocumented"] = true
			if err := schema.Validate(body); err == nil {
				t.Fatal("problem schema permits undocumented fields")
			}
		})
	}
}

func assertAutomationProblemHistory(t *testing.T, body map[string]any, cause error) {
	t.Helper()
	var id, url any
	if blocked, ok := errors.AsType[*automations.ConditionsBlockedError](cause); ok {
		id = string(blocked.SkipID)
		url = "/v1/automations/" + string(blocked.AutomationID) + "/history/" + string(blocked.SkipID)
	}
	if body["history_id"] != id || body["history_url"] != url {
		t.Fatalf("incorrect history links: %v", body)
	}
}

type errorContractService struct {
	Automations

	err error
}

func automationErrorSchema(t *testing.T, api huma.API, path, method string, status int) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/openapi", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(fmt.Sprintf(
		"https://hearth.invalid/openapi#/paths/%s/%s/responses/%d/content/application~1problem+json/schema",
		strings.ReplaceAll(path, "/", "~1"), strings.ToLower(method), status,
	))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func (service *errorContractService) CreateAutomation(
	context.Context,
	automations.Definition,
) (automations.Record, error) {
	return automations.Record{}, service.err
}

func (service *errorContractService) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.Definition,
) (automations.Record, error) {
	return automations.Record{}, service.err
}

func (service *errorContractService) DeleteAutomation(context.Context, automations.AutomationID, int64) error {
	return service.err
}

func (service *errorContractService) GetAutomation(
	context.Context,
	automations.AutomationID,
) (automations.Record, error) {
	return automations.Record{}, service.err
}

func (service *errorContractService) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.HistoryEntry, error) {
	return nil, service.err
}

func (service *errorContractService) StartManualRun(
	context.Context,
	automations.ManualRunInput,
) (automations.Run, error) {
	return automations.Run{}, service.err
}

func TestAutomationFrameworkProblemsMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	const path = "/automations/aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	for _, test := range []struct {
		name, method, path, schemaPath, media, body string
		status                                      int
	}{
		{"list query", "GET", "/automations?limit=0", "/automations", "", "", 422},
		{"history query", "GET", path + "/history?limit=0", "/automations/{automation_id}/history", "", "", 422},
		{"missing revision", "DELETE", path, "/automations/{automation_id}", "", "", 422},
		{"manual null", "POST", path + "/runs", "/automations/{automation_id}/runs", jsonContentType, "null", 422},
		{"manual boolean", "POST", path + "/runs", "/automations/{automation_id}/runs", jsonContentType, `{"bypass_conditions":1}`, 422},
		{"manual JSON", "POST", path + "/runs", "/automations/{automation_id}/runs", jsonContentType, "{", 400},
		{"manual media", "POST", path + "/runs", "/automations/{automation_id}/runs", "text/plain", "{}", 415},
		{"manual size", "POST", path + "/runs", "/automations/{automation_id}/runs", jsonContentType, strings.Repeat(" ", manualRunMaximumBodyBytes+1), 413},
		{"create media", "POST", "/automations", "/automations", "text/plain", "{}", 422},
		{"replace media", "PUT", path, "/automations/{automation_id}", "text/plain", "{}", 422},
		{"replace size", "PUT", path, "/automations/{automation_id}", jsonContentType, strings.Repeat(" ", automationRequestMaxBytes+1), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Errors", "test"))
			// No service calls are allowed for a framework rejection.
			Register(api, nil, nil)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.media)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != problemContentType {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["status"] != float64(test.status) || body["code"] != nil {
				t.Fatalf("not a native framework problem: %s", response.Body.String())
			}
			if err := automationErrorSchema(
				t,
				api,
				test.schemaPath,
				test.method,
				test.status,
			).Validate(body); err != nil {
				t.Fatalf("response violates publication: %v", err)
			}
		})
	}
}

func TestAutomationOperationErrorCatalogs(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Errors", "test"))
	Register(api, nil, nil)
	paths := api.OpenAPI().Paths
	for _, test := range []struct {
		operation *huma.Operation
		statuses  []int
	}{
		{paths["/automations"].Post, []int{400, 413, 422, 500}},
		{paths["/automations"].Get, []int{400, 422, 500}},
		{paths["/automations/{automation_id}"].Get, []int{400, 404, 500}},
		{paths["/automations/{automation_id}"].Put, []int{400, 404, 409, 413, 422, 500}},
		{paths["/automations/{automation_id}"].Delete, []int{400, 404, 409, 422, 500}},
		{paths["/automations/{automation_id}/history"].Get, []int{400, 422, 500}},
		{paths["/automations/{automation_id}/history/{entry_id}"].Get, []int{400, 404, 500}},
		{paths["/automations/{automation_id}/runs"].Post, []int{400, 404, 409, 413, 415, 422, 500, 503}},
	} {
		var statuses []int
		for code := range test.operation.Responses {
			status, err := strconv.Atoi(code)
			if err != nil {
				t.Fatal(err)
			}
			if status >= 400 {
				statuses = append(statuses, status)
			}
		}
		slices.Sort(statuses)
		if !slices.Equal(statuses, test.statuses) {
			t.Errorf("%s errors=%v, want=%v", test.operation.OperationID, statuses, test.statuses)
		}
	}
}

func TestReferenceValidationHTTPFailures(t *testing.T) {
	t.Parallel()
	const definition = `{"name":"Reference failures","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"set","kind":"command","entity_id":"ent_01980000-0000-7000-8000-000000000001","operation":"set","parameters":{"value":true}}]}`
	for _, test := range []struct {
		name, code string
		cause      error
		status     int
	}{
		{"operational", "internal_error", errors.New("private dependency failure"), http.StatusInternalServerError},
		{"missing", "invalid_automation", devices.ErrEntityNotFound, http.StatusBadRequest},
		{"unsupported", "invalid_automation", devices.ErrInvalidCommand, http.StatusBadRequest},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(test.name+"/"+method, func(t *testing.T) {
				t.Parallel()
				service := automations.NewService(
					nil,
					&referenceFailureDevices{cause: test.cause},
					automations.Dependencies{},
				)
				router := echo.New()
				api := humaecho.New(router, huma.DefaultConfig("References", "test"))
				Register(api, service, nil)
				path, body := "/automations", definition
				if method == http.MethodPut {
					path += "/aut_01980000-0000-7000-8000-000000000001"
					body = `{"expected_revision":1,"definition":` + definition + `}`
				}
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				request.Header.Set("Content-Type", jsonContentType)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != test.status {
					t.Fatalf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
				}
				var problem struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if problem.Code != test.code || strings.Contains(response.Body.String(), "private") {
					t.Fatalf("incorrect code or leaked cause: %s", response.Body.String())
				}
			})
		}
	}
}

type referenceFailureDevices struct {
	automations.AutomationDevices

	cause error
}

func (reference *referenceFailureDevices) ValidateCommand(
	context.Context,
	devices.CommandInput,
) (devices.CommandParameters, error) {
	return nil, fmt.Errorf("read Entity: %w", reference.cause)
}
