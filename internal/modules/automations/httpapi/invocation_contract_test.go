package httpapi //nolint:testpackage // Exercise the HTTP boundary and its published contracts.

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

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type invocationHTTPService struct {
	valueContractService

	run automations.Run
}

func (s *invocationHTTPService) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.HistoryEntry, error) {
	return s.run, nil
}

func TestInvocationHTTPContracts(t *testing.T) {
	t.Parallel()
	const target = "aut_01980000-0000-7000-8000-000000000002"
	const parent = "aut_01980000-0000-7000-8000-000000000003"
	const parentRun = "arn_01980000-0000-7000-8000-000000000004"
	const childRun = "arn_01980000-0000-7000-8000-000000000005"
	const nestedRun = "arn_01980000-0000-7000-8000-000000000006"
	const definition = `{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"sub_automation","automation_id":"` + target + `"}]}`
	snapshot, err := automations.DecodeDefinition([]byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	run := automations.NewRunSnapshot(automations.Record{ID: target, Revision: 1, Definition: snapshot}, childRun,
		automations.InvocationCause{ParentAutomationID: parent, ParentRunID: parentRun, ParentStepID: "call"}, nil,
		automations.NotConfiguredDecision(), at)
	run.ValueInitialization = &automations.RunValueInitialization{
		RecordedAt: at,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
	}
	run.Executions = []automations.StepExecution{
		{StepID: "call", Sequence: 0, Details: automations.SubAutomationExecutionDetails{
			StartedAt: at, State: automations.SubAutomationRunning{ChildRunID: nestedRun},
		}},
	}
	service := &invocationHTTPService{run: run}
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Invocation", "test"))
	Register(api, service, nil)

	for _, test := range []struct {
		body   string
		status int
	}{
		{definition, http.StatusCreated},
		{strings.Replace(definition, `"triggers":[],`, "", 1), http.StatusBadRequest},
		{strings.Replace(definition, `"triggers":[]`, `"triggers":null`, 1), http.StatusBadRequest},
		{strings.Replace(definition, `"automation_id":"`+target+`"`, `"automation_id":"`+target+`","parent_run_id":"`+parentRun+`"`, 1), http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("create: got %d want %d: %s", response.Code, test.status, response.Body.String())
		}
		if test.status == http.StatusCreated &&
			(!strings.Contains(response.Body.String(), `"triggers":[]`) || !strings.Contains(response.Body.String(), `"kind":"sub_automation"`)) {
			t.Fatalf("definition: %s", response.Body.String())
		}
	}
	if service.writes != 1 {
		t.Fatalf("invalid definitions reached service: %d", service.writes)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/automations/"+target+"/history/"+childRun, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("history: %d %s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"kind":"invocation"`, `"parent_automation_id":"` + parent + `"`, `"parent_run_id":"` + parentRun + `"`, `"parent_step_id":"call"`, `"child_run_id":"` + nestedRun + `"`, `"triggers":[]`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("missing %s: %s", want, response.Body.String())
		}
	}
	raw, err := json.Marshal(automationOutputSchema("historyEntry"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/history", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://hearth.invalid/history")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatalf("published history schema: %v", err)
	}
}
