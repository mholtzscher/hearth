package httpapi //nolint:testpackage // Exercise the independent HTTP ingress and schema boundary.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type valueContractService struct {
	Automations

	writes int
}

func TestHTTPRunDetailPreservesUnifiedValuesAndSchema(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	definition, err := automations.DecodeDefinition(
		json.RawMessage(
			`{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"variables":{"n":9007199254740993},"steps":[{"id":"f","kind":"function","adapter_id":"example","function_key":"run","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","arguments":{}}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := automations.NewRunSnapshot(
		automations.Record{ID: "aut_01980000-0000-7000-8000-000000000001", Revision: 1, Definition: definition},
		"arn_01980000-0000-7000-8000-000000000001",
		automations.ManualCause{},
		nil,
		automations.NotConfiguredDecision(),
		at,
	)
	run.ValueInitialization = &automations.RunValueInitialization{
		RecordedAt: at,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{"n":9007199254740993}`)},
	}
	run.Executions = []automations.StepExecution{
		{
			StepID:   "f",
			Sequence: 0,
			Details: automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: adapters.InvocationID("afi_01900000-0000-7000-8000-000000000001"),
				Arguments:    json.RawMessage(`{}`),
				State: automations.FunctionSucceeded{
					CompletedAt:     at,
					CatalogRevision: adapters.CatalogID("afc_01900000-0000-7000-8000-000000000001"),
				},
				Output: json.RawMessage(`-9223372036854775808`),
			},
		},
	}
	run.State = automations.CompletedRun{CompletedAt: at, Outcome: automations.SucceededRun{}}
	body, err := automationRunBody(run)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{`"data_evidence":"recorded"`, `"n":9007199254740993`, `"output":-9223372036854775808`} {
		if !strings.Contains(string(raw), exact) {
			t.Fatalf("missing %s: %s", exact, raw)
		}
	}
	documentRaw, err := json.Marshal(automationOutputSchema("run"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(documentRaw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/run", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://hearth.invalid/run")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatalf("published response schema: %v", err)
	}
	run.DataEvidence, run.ValueInitialization, run.Executions = automations.RunDataLegacyUnavailable, nil, nil
	body, err = automationRunBody(run)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"data_evidence":"legacy_unavailable"`) ||
		!strings.Contains(string(raw), `"value_initialization":null`) {
		t.Fatalf("legacy markers lost: %s", raw)
	}
}

func TestHTTPAdmissionRejectsNestedExpressionCondition(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Values", "test"))
	service := &valueContractService{}
	Register(api, service)
	const body = `{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"conditions":{"id":"not","kind":"not","child":{"id":"expr","kind":"expression","expr":"true"}},"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.writes != 0 {
		t.Fatalf("admission expression accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestHTTPReplaceEnvelopeSizeBoundary(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Values", "test"))
	service := &valueContractService{}
	Register(api, service)
	const envelope = `{"expected_revision":1,"definition":{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}}`
	for _, size := range []int{automationRequestMaxBytes, automationRequestMaxBytes + 1, automationRequestMaxBytes + 2} {
		request := httptest.NewRequest(http.MethodPut,
			"/automations/aut_01980000-0000-7000-8000-000000000001",
			strings.NewReader(envelope+strings.Repeat(" ", size-len(envelope))))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		want := http.StatusOK
		if size > automationRequestMaxBytes {
			want = http.StatusRequestEntityTooLarge
		}
		if response.Code != want {
			t.Fatalf("size %d: status %d want %d: %s", size, response.Code, want, response.Body.String())
		}
	}
	if service.writes != 1 {
		t.Fatalf("oversized requests reached service: %d writes", service.writes)
	}
}

func TestHTTPPublishedCanonicalReferencesResolve(t *testing.T) {
	t.Parallel()
	bundle := automationOutputSchema("run")
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/run", document); err != nil {
		t.Fatal(err)
	}
	defs := bundle["$defs"].(map[string]any)
	for _, section := range []string{"history", "definition"} {
		items := defs[section].(map[string]any)["$defs"].(map[string]any)
		for name := range items {
			if _, err = compiler.Compile(
				"https://hearth.invalid/run#/$defs/" + section + "/$defs/" + name,
			); err != nil {
				t.Fatalf("%s/%s: %v", section, name, err)
			}
		}
	}
}

func (service *valueContractService) CreateAutomation(
	_ context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	service.writes++
	return automations.Record{
		ID:         "aut_01980000-0000-7000-8000-000000000001",
		Revision:   1,
		Definition: definition,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}, nil
}

func (service *valueContractService) ReplaceAutomation(
	ctx context.Context,
	id automations.AutomationID,
	expectedRevision int64,
	definition automations.Definition,
) (automations.Record, error) {
	record, err := service.CreateAutomation(ctx, definition)
	record.ID = id
	record.Revision = expectedRevision + 1
	return record, err
}

func TestValueBindingsHTTPSafeDiagnosticsAndEnvelopeLimit(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Values", "test"))
	service := &valueContractService{}
	Register(api, service)
	const prefix = `{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[`
	for _, test := range []struct {
		step   string
		status int
		path   string
	}{
		{`{"id":"set","kind":"set_variables","assignments":{"x":9007199254740993}}`, 201, ""},
		{`{"id":"wait","kind":"delay","duration_ms":1}`, 201, ""},
		{`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`, 400, ""},
		{`{"id":"wait","kind":"delay","duration_ms_expr":"1"}`, 400, ""},
		{`{"id":"send","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":"","message_expr":"let private_source = 'private_value'; private_source +"}`, 400, "/steps/0/message_expr"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(prefix+test.step+`]} `))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status %d want %d: %s", response.Code, test.status, response.Body.String())
		}
		if test.path != "" &&
			(!strings.Contains(response.Body.String(), test.path) || strings.Contains(response.Body.String(), "private_source") || strings.Contains(response.Body.String(), "private_value")) {
			t.Fatalf("unsafe or unlocated diagnostics: %s", response.Body.String())
		}
	}
	for _, size := range []int{automationRequestMaxBytes + 1, automationRequestMaxBytes + 2} {
		request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(strings.Repeat(" ", size)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%d bytes: status %d", size, response.Code)
		}
	}
	valid := prefix + `{"id":"wait","kind":"delay","duration_ms":1}]}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/automations",
		strings.NewReader(valid+strings.Repeat(" ", automationRequestMaxBytes-len(valid))),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("exact limit: %d %s", response.Code, response.Body.String())
	}
	if service.writes != 3 {
		t.Fatalf("invalid requests reached service: %d", service.writes)
	}
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "automation-definition:v2") ||
		strings.Contains(string(raw), "automation-definition:v3") ||
		!strings.Contains(string(raw), "automation-response:run:v1") {
		t.Fatal("schema refs do not use v1")
	}
}
