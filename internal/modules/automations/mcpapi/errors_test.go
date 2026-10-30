package mcpapi //nolint:testpackage // Exercise the domain classifier independently of SDK argument errors.

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestDomainFailureClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		cause         error
		missing       failureCode
		code, message string
	}{
		{automations.ErrAutomationNotFound, failureAutomationNotFound, "automation_not_found", "automation resource not found"},
		{automations.ErrHistoryNotFound, failureHistoryEntryNotFound, "automation_history_entry_not_found", "automation resource not found"},
		{automations.ErrRevisionConflict, failureNone, "revision_conflict", "automation revision conflict"},
		{automations.ErrAutomationBusy, failureNone, "automation_busy", "automation already has a running run"},
		{automations.ErrAutomationConditionsBlocked, failureNone, "conditions_blocked", "automation conditions prevented manual admission"},
		{automations.ErrConditionSnapshotRequired, failureNone, "condition_snapshot_unavailable", "automation condition state is unavailable"},
		{automations.ErrAdmissionUnavailable, failureNone, "admission_unavailable", "automation admission is unavailable"},
		{automations.ErrInvalidAutomation, failureNone, "invalid_automation", "invalid automation request"},
		{errors.New("private dependency failure"), failureNone, "internal_error", "internal error"},
	} {
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			failure := automationFailure(fmt.Errorf("private detail: %w", test.cause), test.missing)
			if failure.Code != test.code || failure.Message != test.message || failure.Details != nil {
				t.Fatalf("classified failure = %+v", failure)
			}
		})
	}
}

func TestFunctionReferenceFailurePreservesDetails(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		"adapter_not_found", "function_not_found", "function_contract_changed", "invalid_function_arguments",
	} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			err := fmt.Errorf("private wrapper: %w", &automations.FunctionReferenceError{
				StepID: "nested-function", ReasonCode: reason,
			})
			failure := automationFailure(err, failureNone)
			want := map[string]any{
				"step_id":     "nested-function",
				"reason_code": reason,
				"diagnostics": []definitionDiagnostic{
					{Code: reason, Path: "/definition/steps", StepID: "nested-function"},
				},
			}
			if failure.Code != "invalid_automation" || failure.Message != "invalid automation Function reference" ||
				!reflect.DeepEqual(failure.Details, want) {
				t.Fatalf("Function reference failure = %+v, want details %v", failure, want)
			}
		})
	}
}
