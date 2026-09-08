// Package entitytypetest provides the shared handwritten runner for generated
// Entity-type contract conformance tests. It replays human-authored example
// expectations without evaluating the manifest DSL or importing production
// contract, SDK, or catalog code.
package entitytypetest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

// ContractProbe exposes observable contract behavior to example tests.
// Callbacks decode and validate inputs and return actual errors or outcomes;
// only the runner compares those results against human-authored expectations.
type ContractProbe struct {
	ValidateSupport func(support json.RawMessage) error
	ValidateState   func(support, state json.RawMessage) error
	Operations      map[string]OperationProbe
}

// OperationProbe tests one operation without deriving expected outcomes.
// Satisfies schema-decodes its recorded inputs but never revalidates them
// against mutable current support. Dispatched operations declare no outcome
// predicate: Dispatched is true, Satisfies is nil, and no outcome examples
// are replayed. Observed operations keep Satisfies and mandatory
// satisfied+unsatisfied outcome coverage.
type OperationProbe struct {
	ValidateParameters func(support, parameters json.RawMessage) error
	Satisfies          func(parameters, state json.RawMessage) (bool, error)
	Dispatched         bool
}

// contractExamples mirrors the existing examples.json authoring shape. The
// runner parses that shape without changing it.
type contractExamples struct {
	Cases []contractCase `json:"cases"`
}

type contractCase struct {
	Name       string                       `json:"name"`
	Support    json.RawMessage              `json:"support"`
	States     []contractValidity           `json:"states"`
	Operations map[string]contractOperation `json:"operations"`
}

type contractValidity struct {
	Value json.RawMessage `json:"value"`
	Valid bool            `json:"valid"`
}

type contractOperation struct {
	Parameters []contractValidity `json:"parameters"`
	Outcomes   []contractOutcome  `json:"outcomes"`
}

type contractOutcome struct {
	Parameters json.RawMessage `json:"parameters"`
	State      json.RawMessage `json:"state"`
	Satisfied  bool            `json:"satisfied"`
}

// UnmarshalJSON rejects authored examples with missing value/valid payloads
// instead of defaulting them to false/nil.
func (validity *contractValidity) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Value *json.RawMessage `json:"value"`
		Valid *bool            `json:"valid"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Value == nil || len(*decoded.Value) == 0 {
		return fmt.Errorf("validity example is missing \"value\"")
	}
	if decoded.Valid == nil {
		return fmt.Errorf("validity example is missing \"valid\"")
	}
	validity.Value = *decoded.Value
	validity.Valid = *decoded.Valid
	return nil
}

// UnmarshalJSON rejects authored examples with missing parameters/state
// payloads or satisfied flags instead of defaulting them to false/nil.
func (outcome *contractOutcome) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Parameters *json.RawMessage `json:"parameters"`
		State      *json.RawMessage `json:"state"`
		Satisfied  *bool            `json:"satisfied"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Parameters == nil || len(*decoded.Parameters) == 0 {
		return fmt.Errorf("outcome example is missing \"parameters\"")
	}
	if decoded.State == nil || len(*decoded.State) == 0 {
		return fmt.Errorf("outcome example is missing \"state\"")
	}
	if decoded.Satisfied == nil {
		return fmt.Errorf("outcome example is missing \"satisfied\"")
	}
	outcome.Parameters = *decoded.Parameters
	outcome.State = *decoded.State
	outcome.Satisfied = *decoded.Satisfied
	return nil
}

// UnmarshalJSON rejects authored examples with missing parameter/outcome
// categories instead of defaulting them to empty.
func (operation *contractOperation) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Parameters *[]contractValidity `json:"parameters"`
		Outcomes   *[]contractOutcome  `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Parameters == nil {
		return fmt.Errorf("operation examples are missing \"parameters\"")
	}
	if decoded.Outcomes == nil {
		return fmt.Errorf("operation examples are missing \"outcomes\"")
	}
	operation.Parameters = *decoded.Parameters
	operation.Outcomes = *decoded.Outcomes
	return nil
}

// UnmarshalJSON rejects authored cases with missing support/state/operation
// payloads instead of defaulting them to empty.
func (example *contractCase) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Name       *string                       `json:"name"`
		Support    *json.RawMessage              `json:"support"`
		States     *[]contractValidity           `json:"states"`
		Operations *map[string]contractOperation `json:"operations"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Name == nil || *decoded.Name == "" {
		return fmt.Errorf("contract case is missing \"name\"")
	}
	if decoded.Support == nil || len(*decoded.Support) == 0 {
		return fmt.Errorf("contract case %q is missing \"support\"", *decoded.Name)
	}
	if decoded.States == nil {
		return fmt.Errorf("contract case %q is missing \"states\"", *decoded.Name)
	}
	if decoded.Operations == nil {
		return fmt.Errorf("contract case %q is missing \"operations\"", *decoded.Name)
	}
	example.Name = *decoded.Name
	example.Support = *decoded.Support
	example.States = *decoded.States
	example.Operations = *decoded.Operations
	return nil
}

// contractSupportedOperations parses the authored support's operation
// inventory without evaluating DSL semantics. Cases omit optional operations
// by leaving them out of support; the runner requires examples to match that
// inventory exactly.
func contractSupportedOperations(support json.RawMessage) (map[string]struct{}, error) {
	var decoded struct {
		Operations *map[string]json.RawMessage `json:"operations"`
	}
	if err := json.Unmarshal(support, &decoded); err != nil {
		return nil, err
	}
	if decoded.Operations == nil {
		return nil, fmt.Errorf("support has no operations object")
	}
	supported := make(map[string]struct{}, len(*decoded.Operations))
	for name := range *decoded.Operations {
		supported[name] = struct{}{}
	}
	return supported, nil
}

// checkContractCaseInventory requires each case's examples to match its own
// supported operations exactly, so a case that omits a supported operation
// fails even when another case covers it. Absence is correct only for
// operations the case does not support.
func checkContractCaseInventory(t *testing.T, example contractCase) {
	t.Helper()
	supported, err := contractSupportedOperations(example.Support)
	if err != nil {
		t.Fatalf("contract case %q support is malformed: %v", example.Name, err)
	}
	for name := range supported {
		if _, ok := example.Operations[name]; !ok {
			t.Fatalf("contract case %q is missing examples for supported operation %q", example.Name, name)
		}
	}
	for name := range example.Operations {
		if _, ok := supported[name]; !ok {
			t.Fatalf("contract case %q has examples for unsupported operation %q", example.Name, name)
		}
	}
}

// RunContractExamples replays every authored case with its original expected
// result through the supplied probe. Type and package context comes from the
// caller via named subtests for case, operation, category, and example index.
// Structural defects (malformed input, empty case sets, missing callbacks,
// unknown operations, empty example categories) fail the test instead of
// silently skipping coverage.
func RunContractExamples(t *testing.T, examplesJSON []byte, probe ContractProbe) {
	t.Helper()
	var examples contractExamples
	if err := json.Unmarshal(examplesJSON, &examples); err != nil {
		t.Fatalf("contract examples are malformed: %v", err)
	}
	if len(examples.Cases) == 0 {
		t.Fatal("contract examples contain no cases")
	}
	if probe.ValidateSupport == nil || probe.ValidateState == nil {
		t.Fatal("contract probe is missing ValidateSupport or ValidateState")
	}
	covered := make(map[string]struct{})
	for _, example := range examples.Cases {
		for name := range example.Operations {
			covered[name] = struct{}{}
		}
	}
	for name, operation := range probe.Operations {
		if _, ok := covered[name]; !ok {
			t.Fatalf("contract probe has unknown operation %q", name)
		}
		if operation.ValidateParameters == nil {
			t.Fatalf("contract probe is missing callbacks for operation %q", name)
		}
		if operation.Dispatched {
			if operation.Satisfies != nil {
				t.Fatalf("contract probe must not define Satisfies for dispatched operation %q", name)
			}
			continue
		}
		if operation.Satisfies == nil {
			t.Fatalf("contract probe is missing callbacks for operation %q", name)
		}
	}
	for _, example := range examples.Cases {
		t.Run(example.Name, func(t *testing.T) {
			t.Helper()
			runContractCase(t, example, probe)
		})
	}
}

func runContractCase(t *testing.T, example contractCase, probe ContractProbe) {
	t.Helper()
	if len(example.Support) == 0 {
		t.Fatal("contract case has no support")
	}
	checkContractCaseInventory(t, example)
	if err := probe.ValidateSupport(example.Support); err != nil {
		t.Fatalf("contract case support is invalid: %v", err)
	}
	if len(example.States) == 0 {
		t.Fatal("contract case has no State examples")
	}
	t.Run("state", func(t *testing.T) {
		t.Helper()
		t.Run("states", func(t *testing.T) {
			t.Helper()
			for index, state := range example.States {
				t.Run(strconv.Itoa(index+1), func(t *testing.T) {
					t.Helper()
					if err := probe.ValidateState(
						example.Support,
						state.Value,
					); (err == nil) != state.Valid {
						t.Errorf("State example valid = %v, want %v (error = %v)", err == nil, state.Valid, err)
					}
				})
			}
		})
	})
	for name, operation := range example.Operations {
		t.Run(name, func(t *testing.T) {
			t.Helper()
			runContractOperation(t, example.Support, name, operation, probe)
		})
	}
}

// checkContractOutcomeCoverage enforces outcome example policy without
// deriving expectations: dispatched operations declare no outcome
// predicate and carry no outcomes, while observed operations keep
// mandatory satisfied+unsatisfied coverage.
func checkContractOutcomeCoverage(
	t *testing.T,
	name string,
	operation contractOperation,
	dispatched bool,
) {
	t.Helper()
	switch {
	case dispatched:
		if len(operation.Outcomes) != 0 {
			t.Fatalf("operation %q is dispatched and must declare no outcomes", name)
		}
	case len(operation.Outcomes) == 0:
		t.Fatalf("operation %q has no outcome examples", name)
	default:
		var satisfied, unsatisfied bool
		for _, outcome := range operation.Outcomes {
			if outcome.Satisfied {
				satisfied = true
			} else {
				unsatisfied = true
			}
		}
		if !satisfied || !unsatisfied {
			t.Fatalf(
				"operation %q requires at least one satisfied and one unsatisfied outcome",
				name,
			)
		}
	}
}

func runContractOperation(
	t *testing.T,
	support json.RawMessage,
	name string,
	operation contractOperation,
	probe ContractProbe,
) {
	t.Helper()
	operationProbe, ok := probe.Operations[name]
	if !ok {
		t.Fatalf("contract probe is missing operation %q", name)
	}
	if len(operation.Parameters) == 0 {
		t.Fatalf("operation %q has no parameter examples", name)
	}
	checkContractOutcomeCoverage(t, name, operation, operationProbe.Dispatched)
	t.Run("parameters", func(t *testing.T) {
		t.Helper()
		for index, parameters := range operation.Parameters {
			t.Run(strconv.Itoa(index+1), func(t *testing.T) {
				t.Helper()
				if err := operationProbe.ValidateParameters(
					support,
					parameters.Value,
				); (err == nil) != parameters.Valid {
					t.Errorf(
						"parameter example valid = %v, want %v (error = %v)",
						err == nil,
						parameters.Valid,
						err,
					)
				}
			})
		}
	})
	t.Run("outcomes", func(t *testing.T) {
		t.Helper()
		// Dispatched operations declare no outcome predicate and carry
		// no outcome examples (enforced above); the empty replay
		// preserves the parameters/outcomes subtest shape without
		// calling a matcher.
		if operationProbe.Dispatched {
			return
		}
		for index, outcome := range operation.Outcomes {
			t.Run(strconv.Itoa(index+1), func(t *testing.T) {
				t.Helper()
				satisfied, err := operationProbe.Satisfies(outcome.Parameters, outcome.State)
				if err != nil {
					t.Errorf("outcome error = %v", err)
					return
				}
				if satisfied != outcome.Satisfied {
					t.Errorf("outcome satisfied = %v, want %v", satisfied, outcome.Satisfied)
				}
			})
		}
	})
}
