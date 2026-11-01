//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
)

type invocationHistoryService struct {
	automationsapi.Automations

	run automations.Run
}

func (s *invocationHistoryService) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.HistoryEntry, error) {
	return s.run, nil
}

func TestInvocationMCPWireHistory(t *testing.T) {
	t.Parallel()
	const target = "aut_01980000-0000-7000-8000-000000000002"
	const parent = "aut_01980000-0000-7000-8000-000000000003"
	const parentRun = "arn_01980000-0000-7000-8000-000000000004"
	const childRun = "arn_01980000-0000-7000-8000-000000000005"
	const nestedRun = "arn_01980000-0000-7000-8000-000000000006"
	snapshot, err := automations.DecodeDefinition(
		[]byte(
			`{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"sub_automation","automation_id":"` + target + `"}]}`,
		),
	)
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
	session := connectAutomationMCP(t, &invocationHistoryService{run: run})
	result := callAutomationTool(
		t,
		session,
		"get_automation_history_entry",
		map[string]any{"automation_id": target, "entry_id": childRun},
	)
	if result.IsError {
		t.Fatalf("tool failed: %#v", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"invocation"`, `"parent_automation_id":"` + parent + `"`, `"parent_run_id":"` + parentRun + `"`, `"parent_step_id":"call"`, `"child_run_id":"` + nestedRun + `"`, `"triggers":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s: %s", want, raw)
		}
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected text: %#v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content: %#v", result.Content[0])
	}
	var textValue any
	if err = json.Unmarshal([]byte(text.Text), &textValue); err != nil {
		t.Fatal(err)
	}
	textRaw, err := json.Marshal(textValue)
	if err != nil {
		t.Fatal(err)
	}
	if string(textRaw) != string(raw) {
		t.Fatalf("text and structured results differ: %s / %s", textRaw, raw)
	}
}
