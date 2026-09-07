// Package entitytypetest provides the shared handwritten runner for generated
// Entity-type contract conformance tests. It replays human-authored example
// expectations without evaluating the manifest DSL or importing production
// contract, SDK, or catalog code.
package entitytypetest

import (
	"encoding/json"
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
// against mutable current support.
type OperationProbe struct {
	ValidateParameters func(support, parameters json.RawMessage) error
	Satisfies          func(parameters, state json.RawMessage) (bool, error)
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
		if operation.ValidateParameters == nil || operation.Satisfies == nil {
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
	if len(operation.Outcomes) == 0 {
		t.Fatalf("operation %q has no outcome examples", name)
	}
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
