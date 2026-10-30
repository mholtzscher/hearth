package automations //nolint:testpackage // Pins metadata retained at canonical definition preparation.

import (
	"errors"
	"reflect"
	"testing"
)

func TestDefinitionExpressionDiagnosticsKeepByteSpans(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source, code string
		span               *DefinitionSourceSpan
	}{
		{"Unicode token", "'私' + )", "expression_type", &DefinitionSourceSpan{Start: 8, End: 9}},
		{"Unicode trailing operator", "'私' +", "expression_type", &DefinitionSourceSpan{Start: 6, End: 7}},
		{"unlocated blank", "   ", "expression_value_invalid", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeDefinition(Definition{
				Name:     "Private",
				Triggers: []Trigger{{ID: "tick", Body: CronTrigger{Expression: "* * * * *"}}},
				Steps: []Step{
					{
						ID: "assign",
						Body: SetVariablesStep{
							Assignments: map[VariableName]ValueBinding{
								"safe": {Body: ExpressionValue{Source: test.source}},
							},
						},
					},
				},
			})
			failure, ok := errors.AsType[*DefinitionError](err)
			if !ok || len(failure.Issues) != 1 {
				t.Fatalf("definition issue missing: %v", err)
			}
			issue := failure.Issues[0]
			if issue.Code != test.code || issue.Path != "/steps/0/assignments_expr/safe" || issue.StepID != "assign" ||
				issue.ConditionID != "" ||
				!reflect.DeepEqual(issue.Span, test.span) {
				t.Fatalf("lost expression metadata: %+v", issue)
			}
		})
	}
}
