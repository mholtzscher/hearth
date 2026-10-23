//go:build integration

package automations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestFunctionDefinitionValidationChecksAllArmsAndPreservesOperationalErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		err     error
		invalid bool
	}{
		{"missing Adapter", adapters.ErrAdapterNotFound, true},
		{"missing Function", adapters.ErrFunctionNotFound, true},
		{"changed contract", adapters.ErrFunctionContractChanged, true},
		{"arguments", adapters.ErrInvalidFunctionInput, true},
		{"persistence", adapters.ErrFunctionPersistence, false},
		{"canceled", context.Canceled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &scriptedFunctions{}
			deps := runtimeTestDependencies()
			deps.Functions = stub
			service, _ := newRuntimeService(t, newScriptedDevices(), deps)
			definition := branchingFixture(t)
			definition.Steps = []automations.Step{{ID: "branch", Body: automations.IfStep{
				Conditions: triggerPredicate(
					"a",
				),
				Then: []automations.Step{{ID: "wait", Body: automations.DelayStep{DurationMS: 1}}},
				Else: []automations.Step{functionNode(t, "unselected")},
			}}}
			created := createRuntimeAutomation(t, service, definition)
			stub.validationErr = test.err
			_, createErr := service.CreateAutomation(t.Context(), definition)
			_, replaceErr := service.ReplaceAutomation(t.Context(), created.ID, created.Revision, definition)
			for _, err := range []error{createErr, replaceErr} {
				if err == nil || errors.Is(err, automations.ErrInvalidAutomation) != test.invalid {
					t.Fatalf("lookup classification: %v, want invalid=%v", err, test.invalid)
				}
				if !test.invalid && !errors.Is(err, test.err) {
					t.Fatalf("operational error lost: %v", err)
				}
			}
			retained, err := service.GetAutomation(t.Context(), created.ID)
			if err != nil || retained.Revision != created.Revision {
				t.Fatalf("failed replacement changed revision: %#v %v", retained, err)
			}
		})
	}
}
