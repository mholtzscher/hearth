// Package exprvalue evaluates native Expr over owned, bounded JSON values.
package exprvalue

import (
	"encoding/json"
	"errors"

	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/vm"
)

// Program is immutable and can be evaluated concurrently with independent VMs.
type Program struct{ code *vm.Program }

// Value keeps the live numeric type alongside exact, bounded JSON evidence.
type Value struct {
	Data any
	JSON json.RawMessage
}

// Failure contains only safe diagnostics, never source or input values.
//
//nolint:errname // Failure is the expression diagnostic contract.
type Failure struct {
	Code  string
	Start int
	End   int
	// Located distinguishes a zero-width span from an unlocated failure.
	Located bool
}

func (f *Failure) Error() string { return f.Code }
func fault(code string) error    { return &Failure{Code: code} }

// OwnValue validates and deep-copies a live value without losing its numeric type.
func OwnValue(data any) (Value, error) { return makeValue(data) }

// Evaluate copies and bounds the entire supplied JSON environment as one value
// before running a fresh VM. CompileJSON provides dynamic named roots.
func Evaluate(program *Program, env map[string]any) (Value, error) {
	return EvaluateWithOwnedEnvironment(program, func() (any, error) {
		value, err := OwnValue(env)
		return value.Data, err
	})
}

// EvaluateWithOwnedEnvironment runs a fresh VM after the caller constructs and
// validates an owned environment. The caller must enforce all environment bounds
// and provide only owned JSON data. This supports domain-specific envelopes whose
// individual fragments have separate limits.
//
//nolint:nonamedreturns // Panic recovery replaces both results at the VM boundary.
func EvaluateWithOwnedEnvironment(program *Program, own func() (any, error)) (value Value, err error) {
	defer func() {
		if recover() != nil {
			value, err = Value{}, fault(evaluationCode)
		}
	}()
	if program == nil || program.code == nil || own == nil {
		return Value{}, fault(evaluationCode)
	}
	environment, err := own()
	if err != nil {
		return Value{}, err
	}
	machine := vm.VM{MemoryBudget: vmAllocationUnits}
	data, err := machine.Run(program.code, environment)
	if err != nil {
		if failure, ok := errors.AsType[*Failure](err); ok {
			return Value{}, failure
		}
		if located, ok := errors.AsType[*file.Error](err); ok && located.Message == "memory budget exceeded" {
			return Value{}, fault("expression_limit")
		}
		return Value{}, fault(evaluationCode)
	}
	return makeValue(data)
}
