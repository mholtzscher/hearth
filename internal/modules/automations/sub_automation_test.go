package automations_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestSubAutomationDefinitionStrictCodecAndLeafLimit(t *testing.T) {
	t.Parallel()
	const target = "aut_019a1b2c-3d4e-7000-8000-000000000002"
	step := `{"id":"call","kind":"sub_automation","automation_id":"` + target + `"}`
	document := func(s string) json.RawMessage {
		return json.RawMessage(`{"name":"Caller","enabled":true,"triggers":[],"steps":[` + s + `]}`)
	}
	definition, err := automations.DecodeDefinition(document(step))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definition.Steps[0].Body.(automations.SubAutomationStep); !ok {
		t.Fatalf("step = %#v", definition.Steps[0])
	}
	encoded, err := automations.EncodeDefinition(definition)
	if err != nil || !strings.Contains(string(encoded), `"kind":"sub_automation"`) ||
		!strings.Contains(string(encoded), `"automation_id":"`+target+`"`) {
		t.Fatalf("encoded = %s, %v", encoded, err)
	}
	for _, bad := range []string{
		strings.Replace(step, `"kind":"sub_automation"`, `"kind":"invoke"`, 1),
		strings.Replace(step, target, "not-canonical", 1),
		strings.Replace(step, `}`, `,"arguments":{}}`, 1),
		strings.Replace(step, `}`, `,"output":{}}`, 1),
	} {
		if _, err = automations.DecodeDefinition(document(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	steps := make([]string, 33)
	for i := range steps {
		steps[i] = fmt.Sprintf(`{"id":"s%d","kind":"sub_automation","automation_id":"%s"}`, i, target)
	}
	if _, err = automations.DecodeDefinition(document(strings.Join(steps, ","))); err == nil {
		t.Fatal("accepted 33 leaves")
	}
	first, second := make([]automations.Step, 17), make([]automations.Step, 16)
	for i := range first {
		first[i] = automations.Step{
			ID:   automations.StepID(fmt.Sprintf("a%d", i)),
			Body: automations.SubAutomationStep{AutomationID: target},
		}
	}
	for i := range second {
		second[i] = automations.Step{
			ID:   automations.StepID(fmt.Sprintf("b%d", i)),
			Body: automations.SubAutomationStep{AutomationID: target},
		}
	}
	definition.Steps = []automations.Step{
		{
			ID: "branch",
			Body: automations.IfStep{
				Conditions: automations.Condition{
					ID: "test",
					Body: automations.ExpressionCondition{
						Value: automations.ValueBinding{Body: automations.ExpressionValue{Source: "true"}},
					},
				},
				Then: first,
				Else: second,
			},
		},
	}
	if _, err = automations.NormalizeDefinition(definition); err == nil {
		t.Fatal("accepted 33 SubAutomation leaves across arms")
	}
}

func TestSubAutomationExecutionCodecRejectsContradictoryEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	child := automations.RunID("arn_019a1b2c-3d4e-7000-8000-000000000004")
	for _, state := range []automations.SubAutomationExecutionState{
		automations.SubAutomationRunning{ChildRunID: child},
		automations.SubAutomationSucceeded{ChildRunID: child, CompletedAt: at},
		automations.SubAutomationRejected{CompletedAt: at, ReasonCode: "automation_busy"},
		automations.SubAutomationChildFailed{ChildRunID: child, CompletedAt: at, Outcome: automations.FailedRun{FailureCode: "invalid_command"}},
		automations.SubAutomationInterrupted{ChildRunID: child, CompletedAt: at, ReasonCode: automations.FailureCoreRestarted},
	} {
		env, err := automations.EncodeStepExecutionDetails(
			automations.SubAutomationExecutionDetails{StartedAt: at, State: state},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = automations.DecodeStepExecutionDetails("call", env); err != nil {
			t.Fatalf("%T: %v", state, err)
		}
		env.EvidenceJSON = json.RawMessage(`{"child_run_id":"` + string(child) + `","output":true}`)
		if _, err = automations.DecodeStepExecutionDetails("call", env); err == nil {
			t.Fatalf("%T accepted output", state)
		}
	}
	if _, err := automations.EncodeStepExecutionDetails(
		automations.SubAutomationExecutionDetails{
			StartedAt: at,
			State:     automations.SubAutomationRejected{CompletedAt: at, ReasonCode: "conditions_false"},
		},
	); err == nil {
		t.Fatal("accepted rejection without Skip")
	}
}
