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
	for _, test := range []struct {
		name, body, code string
		err              error
		status           int
	}{
		{"invalid", valid, "invalid_automation", automations.ErrInvalidAutomation, 400},
		{"conflict", valid, "revision_conflict", automations.ErrRevisionConflict, 409},
		{"function", valid, "invalid_function_reference", &automations.FunctionReferenceError{StepID: "lookup", ReasonCode: "function_missing"}, 422},
		{"unavailable", valid, "admission_unavailable", automations.ErrAdmissionUnavailable, 503},
		{"internal", valid, "internal_error", errors.New("private database failure"), 500},
		{"framework validation", "{", "", nil, 422},
		{"framework size", strings.Repeat(" ", automationRequestMaxBytes+1), "", nil, 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Errors", "test"))
			Register(api, &errorContractService{err: test.err}, nil)
			request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(test.body))
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
			schema := createAutomationErrorSchema(t, api, test.status)
			if err := schema.Validate(body); err != nil {
				t.Fatalf("response does not match published schema: %v; %s", err, response.Body.String())
			}
		})
	}
}

type errorContractService struct {
	Automations

	err error
}

func createAutomationErrorSchema(t *testing.T, api huma.API, status int) *jsonschema.Schema {
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
		"https://hearth.invalid/openapi#/paths/~1automations/post/responses/%d/content/application~1problem+json/schema",
		status,
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
