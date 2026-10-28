package mcpapi //nolint:testpackage // Test MCP argument and result boundaries independently.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestValueBindingsMCPV3OnlyAndEnvelopeLimit(t *testing.T) {
	t.Parallel()
	const prefix = `{"definition":{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[`
	for _, test := range []struct {
		step  string
		valid bool
	}{
		{`{"id":"set","kind":"set_variables","assignments":{"x":9007199254740993}}`, true},
		{`{"id":"wait","kind":"delay","duration_ms":1}`, true},
		{`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`, false},
		{`{"id":"wait","kind":"delay","duration_ms_expr":"1"}`, false},
		{`{"id":"set","kind":"set_variables","assignments_expr":{"x":"let private_source = 'private_value'; private_source +"}}`, false},
	} {
		_, err := definitionArgument(json.RawMessage(prefix + test.step + `]}}`))
		if (err == nil) != test.valid {
			t.Fatalf("valid=%t error=%v", test.valid, err)
		}
		if err != nil &&
			(strings.Contains(err.Error(), "private_source") || strings.Contains(err.Error(), "private_value")) {
			t.Fatal("expression source leaked")
		}
	}
	if _, err := definitionArgument(json.RawMessage(strings.Repeat(" ", automationRequestMaxBytes+1))); err == nil {
		t.Fatal("oversized argument envelope accepted")
	}
	valid := prefix + `{"id":"wait","kind":"delay","duration_ms":1}]}}`
	if _, err := definitionArgument(
		json.RawMessage(valid + strings.Repeat(" ", automationRequestMaxBytes-len(valid))),
	); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
}

func TestMCPAdmissionRejectsNestedExpressionCondition(t *testing.T) {
	t.Parallel()
	const body = `{"definition":{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"conditions":{"id":"not","kind":"not","child":{"id":"expr","kind":"expression","expr":"true"}},"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}}`
	if _, err := definitionArgument(json.RawMessage(body)); err == nil {
		t.Fatal("admission expression accepted")
	}
}

//nolint:gocognit // Provider success, value-flow failure and pre-reservation failure share the exact public-detail/schema oracle.
func TestRunDetailMCPMapsUnifiedValueOutcomes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		details automations.StepExecutionDetails
		want    string
	}{
		{"scalar", automations.FunctionExecutionDetails{StartedAt: at, InvocationID: adapters.InvocationID("afi_01900000-0000-7000-8000-000000000001"), Arguments: json.RawMessage(`{}`), State: automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: adapters.CatalogID("afc_01900000-0000-7000-8000-000000000001")}, Output: json.RawMessage(`9007199254740993`)}, `"output":9007199254740993`},
		{"provider success value failure", automations.FunctionExecutionDetails{StartedAt: at, InvocationID: adapters.InvocationID("afi_01900000-0000-7000-8000-000000000001"), Arguments: json.RawMessage(`{}`), State: automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: adapters.CatalogID("afc_01900000-0000-7000-8000-000000000001")}, ValueFailure: &automations.ValueFailure{FailureCode: "expression_limit"}}, `"invocation_outcome":"succeeded"`},
		{"pre-reservation", automations.ValueFailureExecutionDetails{StepKind: automations.StepKindNotification, At: at, Failure: automations.ValueFailure{Field: "title", FailureCode: "expression_evaluation"}}, `"value_failure"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			step := `{"id":"f","kind":"function","adapter_id":"example","function_key":"run","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","arguments":{}}`
			if test.name == "pre-reservation" {
				step = `{"id":"f","kind":"notification","destination_id":"ndst_01900000-0000-7000-8000-000000000001","title_expr":"vars.missing.nested","message":"hello"}`
			}
			snapshot, err := automations.DecodeDefinition(
				json.RawMessage(
					`{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"variables":{"n":9007199254740993},"steps":[` + step + `]}`,
				),
			)
			if err != nil {
				t.Fatal(err)
			}
			run := automations.NewRunSnapshot(
				automations.Record{ID: "aut_01980000-0000-7000-8000-000000000001", Revision: 1, Definition: snapshot},
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
			run.Executions = []automations.StepExecution{{StepID: "f", Sequence: 0, Details: test.details}}
			if test.name == "scalar" {
				run.State = automations.CompletedRun{CompletedAt: at, Outcome: automations.SucceededRun{}}
			} else {
				code := "expression_limit"
				if test.name == "pre-reservation" {
					code = "expression_evaluation"
				}
				run.State = automations.CompletedRun{CompletedAt: at, Outcome: automations.FailedRun{FailureCode: code}}
			}
			detail, err := runDetail(run)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), test.want) || !strings.Contains(string(raw), `"n":9007199254740993`) ||
				!strings.Contains(string(raw), `"data_evidence":"recorded"`) {
				t.Fatalf("missing exact evidence: %s", raw)
			}
			if test.name == "pre-reservation" && strings.Contains(string(raw), "attempt_id") {
				t.Fatalf("invented reservation: %s", raw)
			}
			var document any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err = decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}
			if err = compileResultContract(t, "historyDetail", "").Validate(document); err != nil {
				t.Fatalf("published schema: %v", err)
			}
		})
	}
}
