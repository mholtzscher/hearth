package mcpapi //nolint:testpackage // Verify the MCP result projection against its published schema.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestInvocationMCPDefinitions(t *testing.T) {
	t.Parallel()
	const target = "aut_01980000-0000-7000-8000-000000000002"
	const parentRun = "arn_01980000-0000-7000-8000-000000000004"
	const definition = `{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"invoke","automation_id":"` + target + `"}]}`
	var argument definitionValue
	if err := argument.UnmarshalJSON([]byte(definition)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(definition, `"triggers":[],`, "", 1),
		strings.Replace(definition, `"triggers":[]`, `"triggers":null`, 1),
		strings.Replace(definition, `"automation_id":"`+target+`"`, `"automation_id":"`+target+`","parent_run_id":"`+parentRun+`"`, 1),
		strings.Replace(definition, `"automation_id":"`+target+`"`, `"automation_id_expr":"vars.target"`, 1),
	} {
		if err := argument.UnmarshalJSON([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid definition: %s", invalid)
		}
	}
}

//nolint:gocognit // Each lifecycle projection checks its public fields and schema together.
func TestInvocationMCPHistoryResultsMatchContracts(t *testing.T) {
	t.Parallel()
	const target = "aut_01980000-0000-7000-8000-000000000002"
	const parent = "aut_01980000-0000-7000-8000-000000000003"
	const parentRun = "arn_01980000-0000-7000-8000-000000000004"
	const childRun = "arn_01980000-0000-7000-8000-000000000005"
	const nestedRun = "arn_01980000-0000-7000-8000-000000000006"
	const definition = `{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"invoke","automation_id":"` + target + `"}]}`
	snapshot, decodeErr := automations.DecodeDefinition([]byte(definition))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cause := automations.InvocationCause{ParentAutomationID: parent, ParentRunID: parentRun, ParentStepID: "call"}
	for _, test := range []struct {
		name  string
		state automations.InvokeExecutionState
		want  string
	}{
		{"running", automations.InvokeRunning{ChildRunID: nestedRun}, `"phase":"running"`},
		{"succeeded", automations.InvokeSucceeded{ChildRunID: nestedRun, CompletedAt: at}, `"phase":"succeeded"`},
		{"rejected", automations.InvokeRejected{CompletedAt: at, ReasonCode: "automation_busy"}, `"reason_code":"automation_busy"`},
		{"child failed", automations.InvokeChildFailed{ChildRunID: nestedRun, CompletedAt: at, Outcome: automations.FailedRun{FailureCode: "executor_fault"}}, `"child_failure_code":"executor_fault"`},
		{"interrupted", automations.InvokeInterrupted{ChildRunID: nestedRun, CompletedAt: at, ReasonCode: "core_stopping"}, `"phase":"interrupted"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := automations.NewRunSnapshot(
				automations.Record{ID: target, Revision: 1, Definition: snapshot},
				childRun,
				cause,
				nil,
				automations.NotConfiguredDecision(),
				at,
			)
			run.ValueInitialization = &automations.RunValueInitialization{
				RecordedAt: at,
				Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
			}
			run.Executions = []automations.StepExecution{
				{
					StepID:   "call",
					Sequence: 0,
					Details:  automations.InvokeExecutionDetails{StartedAt: at, State: test.state},
				},
			}
			detail, err := runDetail(run)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{test.want, `"kind":"invocation"`, `"parent_automation_id":"` + parent + `"`, `"parent_run_id":"` + parentRun + `"`, `"parent_step_id":"call"`, `"triggers":[]`} {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("missing %s: %s", want, raw)
				}
			}
			var doc any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err = decoder.Decode(&doc); err != nil {
				t.Fatal(err)
			}
			if err = compileResultContract(t, "historyDetail", "").Validate(doc); err != nil {
				t.Fatalf("published contract: %v", err)
			}
		})
	}
}

func TestInvocationMCPSummaryMatchesContract(t *testing.T) {
	t.Parallel()
	const target = "aut_01980000-0000-7000-8000-000000000002"
	const childRun = "arn_01980000-0000-7000-8000-000000000005"
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cause := automations.InvocationCause{
		ParentAutomationID: "aut_01980000-0000-7000-8000-000000000003",
		ParentRunID:        "arn_01980000-0000-7000-8000-000000000004", ParentStepID: "call",
	}
	if got := causeSource(cause); got != "invocation" {
		t.Fatalf("summary source = %s", got)
	}
	page := HistoryPage{
		Items: []HistorySummary{
			{
				EntryID:      childRun,
				AutomationID: target,
				Kind:         "run",
				Revision:     1,
				RecordedAt:   at,
				Source:       causeSource(cause),
				Status:       "running",
			},
		},
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if err = compileResultContract(t, "historySummaries", "").Validate(doc); err != nil {
		t.Fatalf("summary schema: %v", err)
	}
}
