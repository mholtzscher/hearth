package exprvalue

import platform "github.com/mholtzscher/hearth/internal/platform/exprvalue"

// Compile prepares native Expr over the execution context.
func Compile(source string) (*Program, error) { return compile(source, false) }

// CompileInitial exposes only immutable Run and Trigger data, including via $env.
func CompileInitial(source string) (*Program, error) { return compile(source, true) }

func compile(source string, initial bool) (*Program, error) {
	var schema any = Environment{}
	if initial {
		schema = initialEnvironment{}
	}
	code, err := platform.Compile(source, schema)
	if err != nil {
		return nil, err
	}
	return &Program{code: code, initial: initial}, nil
}
