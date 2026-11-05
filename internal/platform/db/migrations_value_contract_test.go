package db //nolint:testpackage // Exercises pure historical failure-path conversion.

import (
	"strings"
	"testing"
)

func TestValueContractHistoricalPathForms(t *testing.T) {
	t.Parallel()
	definition := `{"variables":{"x":1},"variables_expr":{"y":"1"},"steps":[{"id":"set","kind":"set_variables","assignments":{"x":1},"assignments_expr":{"y":"1"}},{"id":"branch","kind":"if","conditions":{"id":"c","kind":"expression","expr":"true"},"then":[]}]}`
	for _, tc := range []struct{ step, kind, path, code, field string }{
		{"", "", "/trigger", "expression_type", "trigger"},
		{"", "", "/variables", "run_data_limit", "variables"},
		{"", "", "/variables/x", "expression_type", "variables.x"},
		{"", "", "/variables_expr/y", "expression_type", "variables.y"},
		{"set", "set_variables", "/steps/0/assignments", "run_data_limit", "assignments"},
		{"set", "set_variables", "/steps/0/assignments/x", "expression_type", "assignments.x"},
		{"set", "set_variables", "/steps/0/assignments_expr/y", "expression_type", "assignments.y"},
		{"branch", "if", "/steps/1", "run_data_limit", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			c := valueContractCandidate{
				step:       tc.step,
				kind:       tc.kind,
				definition: definition,
				evidence:   `{"evaluations":[]}`,
				failure:    `{"path":"` + tc.path + `","failure_code":"` + tc.code + `"}`,
			}
			got, err := convertValueContractFailure(c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.field == "" {
				if strings.Contains(got, `"field"`) || strings.Contains(got, `"condition_id"`) {
					t.Fatal(got)
				}
			} else if !strings.Contains(got, `"field":"`+tc.field+`"`) {
				t.Fatal(got)
			}
		})
	}
}
