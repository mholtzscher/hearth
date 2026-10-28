// Package exprvalue evaluates native Expr over owned, bounded JSON context.
package exprvalue

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/vm"
)

// Environment contains only owned JSON data; Evaluate copies and validates it.
type Environment struct {
	Run     map[string]any `expr:"run"`
	Trigger map[string]any `expr:"trigger"`
	Vars    map[string]any `expr:"vars"`
	Steps   map[string]any `expr:"steps"`
}

type initialEnvironment struct {
	Run     map[string]any `expr:"run"`
	Trigger map[string]any `expr:"trigger"`
}

// Program is immutable and can be evaluated concurrently with independent VMs.
type Program struct {
	code    *vm.Program
	offsets []int
	initial bool
}

// Value keeps the live numeric type alongside exact, bounded JSON evidence.
type Value struct {
	Data any
	JSON json.RawMessage
}

// OwnValue validates and deep-copies a live value without losing its numeric type.
func OwnValue(data any) (Value, error) { return makeValue(data) }

// ValidateEnvironment enforces the complete live context bounds without evaluation.
func ValidateEnvironment(env Environment) error {
	_, err := ownEnvironment(env)
	return err
}

// Failure is safe for logs: it never contains source or input values.
//
//nolint:errname // The approved concrete bridge API names this diagnostic Failure.
type Failure struct {
	Code  string
	Start int
	End   int
}

func (f *Failure) Error() string { return f.Code }

func fault(code string) error { return &Failure{Code: code} }

// Evaluate runs a fresh VM, never sharing mutable execution state with Program.
//
//nolint:nonamedreturns // Panic recovery must replace both result and error at this boundary.
func Evaluate(program *Program, env Environment) (value Value, err error) {
	defer func() {
		if recover() != nil {
			value = Value{}
			err = fault(evaluationCode)
		}
	}()
	if program == nil || program.code == nil {
		return Value{}, fault(evaluationCode)
	}
	owned, err := ownEnvironment(env)
	if err != nil {
		return Value{}, err
	}
	machine := vm.VM{MemoryBudget: vmAllocationUnits}
	var environment any = owned
	if program.initial {
		environment = initialEnvironment{Run: owned.Run, Trigger: owned.Trigger}
	}
	data, err := machine.Run(program.code, environment)
	if err != nil {
		if failure, ok := errors.AsType[*Failure](err); ok {
			return Value{}, failure
		}
		if located, ok := errors.AsType[*file.Error](err); ok {
			start, end := byteSpan(program.offsets, located.Location)
			code := evaluationCode
			// Pinned upstream VM raises this fixed sentinel string (not a typed
			// error); do not inspect or return arbitrary provider error text.
			if located.Message == "memory budget exceeded" {
				code = "expression_limit"
			}
			return Value{}, &Failure{Code: code, Start: start, End: end}
		}
		return Value{}, fault(evaluationCode)
	}
	return makeValue(data)
}

func ownEnvironment(env Environment) (Environment, error) {
	if len(env.Vars) > variableCount {
		return Environment{}, fault("expression_limit")
	}
	owned := Environment{}
	roots := []struct {
		input     map[string]any
		target    *map[string]any
		normalize func(any) (Value, error)
	}{
		{env.Run, &owned.Run, makeValue}, {env.Trigger, &owned.Trigger, makeValue},
		{env.Vars, &owned.Vars, makeValue}, {env.Steps, &owned.Steps, ownStepValue},
	}
	total := len(`{"run":,"trigger":,"vars":,"steps":}`)
	for _, root := range roots {
		data, size, err := ownRoot(root.input, root.normalize)
		if err != nil {
			return Environment{}, err
		}
		*root.target = data
		total += size
		if total > environmentBytes {
			return Environment{}, fault("expression_limit")
		}
	}
	return owned, nil
}

// Context roots aggregate bounded fragments; their envelopes do not consume an
// individual value's depth, node or encoded-byte allowance.
func ownRoot(root map[string]any, normalize func(any) (Value, error)) (map[string]any, int, error) {
	if len(root) > containerSize {
		return nil, 0, fault("expression_limit")
	}
	owned := make(map[string]any, len(root))
	total := 0
	for _, key := range objectKeys(root) {
		if len(key) > environmentBytes-total {
			return nil, 0, fault("expression_limit")
		}
		if !utf8.ValidString(key) {
			return nil, 0, fault("expression_value_invalid")
		}
		value, err := normalize(root[key])
		if err != nil {
			return nil, 0, err
		}
		owned[key] = value.Data
		total += len(key) + len(value.JSON)
		if total > environmentBytes {
			return nil, 0, fault("expression_limit")
		}
	}
	raw, err := json.Marshal(owned)
	if err != nil {
		return nil, 0, fault("expression_value_invalid")
	}
	return owned, len(raw), nil
}

func ownStepValue(fragment any) (Value, error) {
	entry, ok := fragment.(map[string]any)
	if !ok || len(entry) != 1 {
		return makeValue(fragment)
	}
	output, exists := entry["output"]
	if !exists {
		return makeValue(fragment)
	}
	value, err := makeValue(output)
	if err != nil {
		return Value{}, err
	}
	encoded := make([]byte, 0, len(`{"output":}`)+len(value.JSON))
	encoded = append(encoded, `{"output":`...)
	encoded = append(encoded, value.JSON...)
	encoded = append(encoded, '}')
	return Value{Data: map[string]any{"output": value.Data}, JSON: encoded}, nil
}
