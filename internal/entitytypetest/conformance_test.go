// Independent checks for the shared contract conformance runner. These tests
// use fixed human-reviewed fixtures and hand-written probes rather than the
// generator, so a runner omission cannot be masked by a matching generator
// omission. Rejection scenarios run in a subprocess because the runner fails
// structurally broken input with FailNow.
package entitytypetest_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/entitytypetest"
)

const runnerScenarioEnv = "HEARTH_ENTITYTYPETEST_SCENARIO"

// fixedContractExamples is a human-reviewed fixture with one valid and one
// invalid State, one valid and one invalid parameter set, and one satisfied
// and one unsatisfied outcome.
const fixedContractExamples = `{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
	`"states": [{"value": "ok", "valid": true}, {"value": "bad", "valid": false}], ` +
	`"operations": {"set": {"parameters": [{"value": "go", "valid": true}, ` +
	`{"value": "bad", "valid": false}], "outcomes": [{"parameters": "go", ` +
	`"state": "ok", "satisfied": true}, {"parameters": "go", "state": "other", ` +
	`"satisfied": false}]}}}]}`

// fixedContractProbe implements the fixed fixture's contract directly: only
// "ok" states, "go" parameters, and "go"+"ok" outcomes are accepted. It
// derives nothing from production code.
func fixedContractProbe() entitytypetest.ContractProbe {
	return entitytypetest.ContractProbe{
		ValidateSupport: func(support json.RawMessage) error {
			if !supportHasLevel(support) {
				return errBadSupport
			}
			return nil
		},
		ValidateState: func(support, state json.RawMessage) error {
			if !supportHasLevel(support) {
				return errBadSupport
			}
			if value, ok := jsonString(state); !ok || value != "ok" {
				return errBadState
			}
			return nil
		},
		Operations: map[string]entitytypetest.OperationProbe{
			"set": {
				ValidateParameters: func(support, parameters json.RawMessage) error {
					if !supportHasLevel(support) {
						return errBadSupport
					}
					if value, ok := jsonString(parameters); !ok || value != "go" {
						return errBadParameters
					}
					return nil
				},
				Satisfies: func(parameters, state json.RawMessage) (bool, error) {
					values, ok := jsonString(parameters)
					if !ok {
						return false, errBadParameters
					}
					current, ok := jsonString(state)
					if !ok {
						return false, errBadState
					}
					return values == "go" && current == "ok", nil
				},
			},
		},
	}
}

// supportHasLevel reports whether raw support carries the fixed level key.
func supportHasLevel(raw json.RawMessage) bool {
	var support map[string]any
	if err := json.Unmarshal(raw, &support); err != nil {
		return false
	}
	_, ok := support["level"]
	return ok
}

// jsonString decodes a raw JSON string value.
func jsonString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

type runnerError string

func (err runnerError) Error() string { return string(err) }

const (
	errBadSupport    runnerError = "bad support"
	errBadState      runnerError = "bad State"
	errBadParameters runnerError = "bad parameters"
)

// This test protects full-category replay and fails if the runner skips a
// valid example category while the probe agrees with the fixture.
func TestContractRunnerAcceptsFixedExamples(t *testing.T) {
	t.Parallel()
	entitytypetest.RunContractExamples(t, []byte(fixedContractExamples), fixedContractProbe())
}

// This test protects optional operations: a case that does not support an
// operation correctly omits its examples while another case covers it.
func TestContractRunnerAcceptsOptionalAbsent(t *testing.T) {
	t.Parallel()
	examples := `{"cases": [` +
		`{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
		`"states": [{"value": "ok", "valid": true}, {"value": "bad", "valid": false}], ` +
		`"operations": {"set": {"parameters": [{"value": "go", "valid": true}, ` +
		`{"value": "bad", "valid": false}], "outcomes": [{"parameters": "go", ` +
		`"state": "ok", "satisfied": true}, {"parameters": "go", "state": "other", ` +
		`"satisfied": false}]}}}, ` +
		`{"name": "disabled", "support": {"level": 1, "operations": {}}, ` +
		`"states": [{"value": "ok", "valid": true}, {"value": "bad", "valid": false}], ` +
		`"operations": {}}]}`
	entitytypetest.RunContractExamples(t, []byte(examples), fixedContractProbe())
}

type runnerScenario struct {
	name string
	// want is a fragment of the child failure output proving the intended
	// rejection fired rather than an incidental failure.
	want   string
	mutate func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe)
}

func runnerScenarios() []runnerScenario {
	setProbe := func(
		probe entitytypetest.ContractProbe,
		operation entitytypetest.OperationProbe,
	) entitytypetest.ContractProbe {
		probe.Operations = map[string]entitytypetest.OperationProbe{"set": operation}
		return probe
	}
	validOperation := fixedContractProbe().Operations["set"]
	replace := func(examples string) func([]byte, entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
		return func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
			return []byte(examples), probe
		}
	}
	return []runnerScenario{
		{
			name:   "malformed-examples",
			want:   "contract examples are malformed",
			mutate: replace(`{"cases": [`),
		},
		{
			name:   "empty-cases",
			want:   "contract examples contain no cases",
			mutate: replace(`{"cases": []}`),
		},
		{
			name: "missing-support-callback",
			want: "contract probe is missing ValidateSupport or ValidateState",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.ValidateSupport = nil
				return examples, probe
			},
		},
		{
			name: "missing-state-callback",
			want: "contract probe is missing ValidateSupport or ValidateState",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.ValidateState = nil
				return examples, probe
			},
		},
		{
			name: "missing-operation-probe",
			want: `contract probe is missing operation "set"`,
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.Operations = nil
				return examples, probe
			},
		},
		{
			name: "unknown-operation-probe",
			want: `contract probe has unknown operation "bogus"`,
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.Operations["bogus"] = validOperation
				return examples, probe
			},
		},
		{
			name: "missing-operation-callbacks",
			want: `contract probe is missing callbacks for operation "set"`,
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				return examples, setProbe(probe, entitytypetest.OperationProbe{})
			},
		},
		{
			name: "empty-states",
			want: "contract case has no State examples",
			mutate: func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.Operations = nil
				return []byte(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {}}, "states": [], ` +
					`"operations": {}}]}`), probe
			},
		},
		{
			name: "empty-parameters",
			want: `operation "set" has no parameter examples`,
			mutate: func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				return []byte(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
					`"states": [{"value": "ok", "valid": true}], ` +
					`"operations": {"set": {"parameters": [], "outcomes": [{"parameters": "go", ` +
					`"state": "ok", "satisfied": true}]}}}]}`), probe
			},
		},
		{
			name: "empty-outcomes",
			want: `operation "set" has no outcome examples`,
			mutate: func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				return []byte(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
					`"states": [{"value": "ok", "valid": true}], ` +
					`"operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
					`"outcomes": []}}}]}`), probe
			},
		},
		{
			name: "wrong-state-result",
			want: "State example valid = true, want false",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				probe.ValidateState = func(_, _ json.RawMessage) error { return nil }
				return examples, probe
			},
		},
		{
			name: "wrong-parameter-result",
			want: "parameter example valid = true, want false",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				operation := validOperation
				operation.ValidateParameters = func(_, _ json.RawMessage) error { return nil }
				return examples, setProbe(probe, operation)
			},
		},
		{
			name: "inverted-outcome",
			want: "outcome satisfied =",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				operation := validOperation
				operation.Satisfies = func(parameters, state json.RawMessage) (bool, error) {
					satisfied, err := validOperation.Satisfies(parameters, state)
					return !satisfied, err
				}
				return examples, setProbe(probe, operation)
			},
		},
		{
			name: "outcome-error",
			want: "outcome error =",
			mutate: func(examples []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				operation := validOperation
				operation.Satisfies = func(_, _ json.RawMessage) (bool, error) {
					return false, errBadState
				}
				return examples, setProbe(probe, operation)
			},
		},
		{
			name: "missing-state-value",
			want: "missing \"value\"",
			mutate: replace(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
				`"states": [{"valid": true}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
				`"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}]}`),
		},
		{
			name: "missing-state-valid",
			want: "missing \"valid\"",
			mutate: replace(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
				`"states": [{"value": "ok"}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
				`"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}]}`),
		},
		{
			name: "missing-outcome-satisfied",
			want: "missing \"satisfied\"",
			mutate: replace(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
				`"states": [{"value": "ok", "valid": true}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
				`"outcomes": [{"parameters": "go", "state": "ok"}]}}}]}`),
		},
		{
			name: "missing-operation-parameters",
			want: "missing \"parameters\"",
			mutate: replace(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
				`"states": [{"value": "ok", "valid": true}], ` +
				`"operations": {"set": {"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}]}`),
		},
		{
			name: "support-missing-operations",
			want: "has no operations object",
			mutate: replace(`{"cases": [{"name": "demo", "support": {"level": 1}, ` +
				`"states": [{"value": "ok", "valid": true}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
				`"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}]}`),
		},
		{
			name: "case-omits-supported-operation",
			want: "missing examples for supported operation",
			mutate: func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				return []byte(`{"cases": [` +
					`{"name": "demo", "support": {"level": 1, "operations": {"set": {}}}, ` +
					`"states": [{"value": "ok", "valid": true}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
					`"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}, ` +
					`{"name": "narrow", "support": {"level": 1, "operations": {"set": {}}}, ` +
					`"states": [{"value": "ok", "valid": true}], "operations": {}}]}`), probe
			},
		},
		{
			name: "case-covers-unsupported-operation",
			want: "has examples for unsupported operation",
			mutate: func(_ []byte, probe entitytypetest.ContractProbe) ([]byte, entitytypetest.ContractProbe) {
				return []byte(`{"cases": [{"name": "demo", "support": {"level": 1, "operations": {}}, ` +
					`"states": [{"value": "ok", "valid": true}], "operations": {"set": {"parameters": [{"value": "go", "valid": true}], ` +
					`"outcomes": [{"parameters": "go", "state": "ok", "satisfied": true}]}}}]}`), probe
			},
		},
	}
}

// This test protects strict runner rejection and fails if a malformed,
// incomplete, or deliberately wrong probe input passes silently.
func TestContractRunnerRejections(t *testing.T) {
	t.Parallel()
	if scenario := os.Getenv(runnerScenarioEnv); scenario != "" {
		runRunnerScenario(t, scenario)
		return
	}
	for _, scenario := range runnerScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			command := exec.Command(os.Args[0], "-test.run", "^TestContractRunnerRejections$", "-test.count=1")
			command.Env = append(os.Environ(), runnerScenarioEnv+"="+scenario.name)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("scenario %s passed, want failure:\n%s", scenario.name, output)
			}
			if !strings.Contains(string(output), scenario.want) {
				t.Fatalf("scenario %s output does not contain %q:\n%s", scenario.name, scenario.want, output)
			}
		})
	}
}

func runRunnerScenario(t *testing.T, name string) {
	t.Helper()
	for _, scenario := range runnerScenarios() {
		if scenario.name != name {
			continue
		}
		examples, probe := scenario.mutate([]byte(fixedContractExamples), fixedContractProbe())
		entitytypetest.RunContractExamples(t, examples, probe)
		t.Fatalf("scenario %s unexpectedly passed", name)
		return
	}
	t.Fatalf("unknown runner scenario %q", name)
}
