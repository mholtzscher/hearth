package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func observationTransitionDefinition(t *testing.T, previousThreshold int) string {
	t.Helper()
	document := definitionDocument(t, 1)
	return strings.Replace(document, `"comparisons":[`, fmt.Sprintf(
		`"previous_comparisons":[{"value_pointer":"/temperature","operator":"lte","operand":%d}],"comparisons":[`,
		previousThreshold,
	), 1)
}

func assertPreviousComparisonOperand(
	t *testing.T,
	comparisons []AutomationComparisonBody,
	want string,
) {
	t.Helper()
	if len(comparisons) != 1 {
		t.Fatalf("previous comparisons = %#v, want one", comparisons)
	}
	comparison := comparisons[0]
	if comparison.Pointer != "/temperature" || comparison.Operator != "lte" || string(comparison.Operand) != want {
		t.Fatalf("previous comparison = %#v, want pointer /temperature, lte, operand %s", comparison, want)
	}
}
