package automations //nolint:testpackage // The save boundary must not reuse runtime-admission classifications.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type saveFailureCase struct {
	name   string
	cause  error
	reason string
}

func TestFunctionSaveFailureContract(t *testing.T) {
	t.Parallel()
	for _, test := range []saveFailureCase{
		{"missing Adapter", adapters.ErrAdapterNotFound, "adapter_not_found"},
		{"missing Function", adapters.ErrFunctionNotFound, "function_not_found"},
		{"changed contract", adapters.ErrFunctionContractChanged, "function_contract_changed"},
		{"invalid arguments", adapters.ErrInvalidFunctionInput, "invalid_function_arguments"},
		{"storage", errors.New("SENSITIVE-storage"), ""},
		{"persistence", adapters.ErrFunctionPersistence, ""},
		{"canceled", context.Canceled, ""},
		{"deadline", context.DeadlineExceeded, ""},
		// These are synthetic seam errors, not current retained-lookup producers.
		{"runtime unavailable", adapters.ErrAdapterUnavailable, ""},
		{"runtime admission closed", adapters.ErrFunctionAdmissionClosed, ""},
		{"runtime catalog changed", adapters.ErrCatalogChanged, ""},
		{"persistence and invalid", errors.Join(adapters.ErrFunctionPersistence, adapters.ErrInvalidFunctionInput), ""},
		{"canceled and missing", errors.Join(context.Canceled, adapters.ErrFunctionNotFound), ""},
	} {
		for _, binding := range []string{"literal", "expression"} {
			for _, operation := range []string{"create", "replace"} {
				t.Run(test.name+"/"+binding+"/"+operation, func(t *testing.T) {
					t.Parallel()
					exerciseFunctionSaveFailure(t, test, binding, operation)
				})
			}
		}
	}
}

func exerciseFunctionSaveFailure(t *testing.T, test saveFailureCase, binding, operation string) {
	t.Helper()
	functions := &saveFailureFunctions{cause: fmt.Errorf("SENSITIVE-lookup: %w", test.cause)}
	repo := &saveFailureRepository{}
	arguments := flowLiteral(`{"opaque":9007199254740993}`)
	if binding == "expression" {
		arguments = flowExpression(`{opaque: vars.number}`)
	}
	definition := flowDefinition(
		t,
		nil,
		[]Step{
			{
				ID: "lookup",
				Body: FunctionStep{
					AdapterID:   "demo",
					FunctionKey: "inspect",
					ContractFingerprint: adapters.ContractFingerprint(
						"sha256-v1:" + strings.Repeat("0", 64),
					),
					Arguments: arguments,
				},
			},
		},
	)
	service := NewService(repo, &flowReferenceDevices{}, Dependencies{Functions: functions})
	var err error
	if operation == "create" {
		_, err = service.CreateAutomation(t.Context(), definition)
	} else {
		_, err = service.ReplaceAutomation(
			t.Context(),
			"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			1,
			definition,
		)
	}
	assertSaveFailure(t, err, test.cause, test.reason)
	if repo.writes != 0 || functions.calls != 1 {
		t.Fatalf("writes=%d lookups=%d", repo.writes, functions.calls)
	}
}

func assertSaveFailure(t *testing.T, err, cause error, reason string) {
	t.Helper()
	reference, invalid := errors.AsType[*FunctionReferenceError](err)
	if reason != "" {
		if !invalid || reference.ReasonCode != reason || reference.StepID != "lookup" || reference.Path != "/steps/0" ||
			!errors.Is(err, ErrInvalidAutomation) {
			t.Fatalf("lost safe invalid reference: %#v / %v", reference, err)
		}
		return
	}
	if invalid || errors.Is(err, ErrInvalidAutomation) || !errors.Is(err, cause) {
		t.Fatalf("operational failure converted or lost: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), `step "lookup" Function`) {
		t.Fatalf("missing private Step context: %v", err)
	}
}

type saveFailureFunctions struct {
	AutomationFunctions

	cause error
	calls int
}

func (functions *saveFailureFunctions) ValidateContractReference(context.Context, adapters.ContractReference) error {
	functions.calls++
	return functions.cause
}

func (functions *saveFailureFunctions) ValidateContractInput(
	context.Context,
	adapters.ContractInput,
) (json.RawMessage, error) {
	functions.calls++
	return nil, functions.cause
}

type saveFailureRepository struct {
	Repository

	writes int
}

func (repo *saveFailureRepository) CreateAutomation(context.Context, Definition) (Record, error) {
	repo.writes++
	return Record{}, errors.New("unexpected create")
}
func (repo *saveFailureRepository) ReplaceAutomation(context.Context, AutomationID, int64, Definition) (Record, error) {
	repo.writes++
	return Record{}, errors.New("unexpected replace")
}
