package automations_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const (
	kindDiagnosticButton = "ent_01890f47-7a6b-7c4d-8e9f-0123456789aa"
	kindDiagnosticLamp   = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func allowedStepKinds() []string {
	return []string{"command", "if", "choose", "delay", "notification", "function", "set_variables", "read_state"}
}

func allowedBranchConditionKinds() []string {
	return []string{"expression", "entity_state", "trigger", "all", "any", "not"}
}

func commandStepProperties() []string {
	return []string{"entity_id", "id", "kind", "operation", "parameters", "parameters_expr"}
}

// TestDefinitionKindDiagnosticsExplainRejectedSteps replays the two step shapes
// the household agent submitted and asserts that the decoder names the rejected
// field instead of every alternative of the Step union.
func TestDefinitionKindDiagnosticsExplainRejectedSteps(t *testing.T) {
	t.Parallel()
	command := func(id string, extra string) string {
		return `{"id":"` + id + `","kind":"command","entity_id":"` + kindDiagnosticLamp +
			`","operation":"set","parameters":{"value":true}` + extra + `}`
	}
	when := `,"when":{"trigger_id":"single_press"}`
	for _, test := range []struct {
		name, steps string
		want        []automations.DefinitionIssue
	}{
		{
			name:  "unknown property on Commands",
			steps: command("on_fan", when) + "," + command("off_fan", when),
			want: []automations.DefinitionIssue{
				{Path: "/steps/0", Code: "unknown_property", Allowed: commandStepProperties()},
				{Path: "/steps/1", Code: "unknown_property", Allowed: commandStepProperties()},
			},
		},
		{
			name: "unknown Step kind",
			steps: `{"id":"single_on","kind":"branch","condition":{"trigger_id":"single_press"},"then":[` +
				command("on_fan", "") + `]}`,
			want: []automations.DefinitionIssue{
				{Path: "/steps/0/kind", Code: "unknown_kind", Allowed: allowedStepKinds()},
			},
		},
		{
			name:  "missing Step kind",
			steps: `{"id":"on_fan","entity_id":"` + kindDiagnosticLamp + `","operation":"set","parameters":{}}`,
			want: []automations.DefinitionIssue{
				{Path: "/steps/0/kind", Code: "missing_property", Allowed: allowedStepKinds()},
			},
		},
		{
			name:  "missing property of the selected kind",
			steps: `{"id":"wait","kind":"delay"}`,
			want: []automations.DefinitionIssue{
				{Path: "/steps/0/duration_ms", Code: "missing_property"},
			},
		},
		{
			name: "nested Step and branch Condition",
			steps: `{"id":"route","kind":"if","conditions":{"id":"pressed","kind":"pressed"},"then":[` +
				command("on_fan", when) + `]}`,
			want: []automations.DefinitionIssue{
				{Path: "/steps/0/conditions/kind", Code: "unknown_kind", Allowed: allowedBranchConditionKinds()},
				{Path: "/steps/0/then/0", Code: "unknown_property", Allowed: commandStepProperties()},
			},
		},
		{
			name:  "invalid value inside the selected kind",
			steps: `{"id":"wait","kind":"delay","duration_ms":0}`,
			want: []automations.DefinitionIssue{
				{Path: "/steps/0/duration_ms", Code: "invalid_definition"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertDefinitionIssues(t, kindDiagnosticDocument(eventTrigger, test.steps), test.want)
		})
	}
}

func TestDefinitionKindDiagnosticsExplainRejectedTriggers(t *testing.T) {
	t.Parallel()
	assertDefinitionIssues(
		t,
		kindDiagnosticDocument(`{"id":"single_press","kind":"button","entity_id":"`+kindDiagnosticButton+`"}`,
			`{"id":"wait","kind":"delay","duration_ms":1}`),
		[]automations.DefinitionIssue{
			{
				Path:    "/triggers/0/kind",
				Code:    "unknown_kind",
				Allowed: []string{"observation", "entity_event", "held_state", "cron"},
			},
		},
	)
}

func TestDefinitionKindDiagnosticsDoNotEchoValues(t *testing.T) {
	t.Parallel()
	for _, step := range []string{
		`{"id":"wait","kind":"SENSITIVE-kind","duration_ms":1}`,
		`{"id":"wait","kind":"delay","duration_ms":1,"SENSITIVE-property":true}`,
	} {
		_, err := automations.DecodeDefinition(json.RawMessage(kindDiagnosticDocument(eventTrigger, step)))
		failure, ok := errors.AsType[*automations.DefinitionError](err)
		if !ok || len(failure.Issues) == 0 {
			t.Fatalf("DecodeDefinition error = %v, want DefinitionError", err)
		}
		if rendered := fmt.Sprintf("%+v", failure.Issues); strings.Contains(rendered, "SENSITIVE") {
			t.Fatalf("diagnostics echo the rejected input: %s", rendered)
		}
	}
}

const eventTrigger = `{"id":"single_press","kind":"entity_event","entity_id":"` + kindDiagnosticButton +
	`","event_name":"single"}`

func kindDiagnosticDocument(trigger, steps string) string {
	return `{"name":"Office button","enabled":true,"triggers":[` + trigger + `],"steps":[` + steps + `]}`
}

func assertDefinitionIssues(t *testing.T, document string, want []automations.DefinitionIssue) {
	t.Helper()
	_, err := automations.DecodeDefinition(json.RawMessage(document))
	failure, ok := errors.AsType[*automations.DefinitionError](err)
	if !ok {
		t.Fatalf("DecodeDefinition error = %v, want DefinitionError", err)
	}
	got := make([]automations.DefinitionIssue, 0, len(failure.Issues))
	for _, issue := range failure.Issues {
		got = append(got, automations.DefinitionIssue{Path: issue.Path, Code: issue.Code, Allowed: issue.Allowed})
	}
	// Diagnostic order is not part of the contract; retain exact content checks.
	want = slices.Clone(want)
	byPath := func(left, right automations.DefinitionIssue) int { return strings.Compare(left.Path, right.Path) }
	slices.SortFunc(got, byPath)
	slices.SortFunc(want, byPath)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %+v\nwant     %+v", got, want)
	}
}
