package exprvalue

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/types"
)

// CompileJSON compiles against the named dynamic JSON roots. Other root names
// are rejected; each supplied root may contain any supported JSON value.
func CompileJSON(source string, roots ...string) (*Program, error) {
	schema := make(types.Map, len(roots))
	for _, root := range roots {
		schema[root] = types.Any
	}
	return Compile(source, schema)
}

// Compile prepares native Expr against the caller's environment schema.
//
//nolint:nonamedreturns // Panic recovery replaces both results at the compiler boundary.
func Compile(source string, environment any) (program *Program, err error) {
	defer func() {
		if recover() != nil {
			program, err = nil, fault(evaluationCode)
		}
	}()
	if len(source) > sourceBytes {
		return nil, fault("expression_limit")
	}
	if !utf8.ValidString(source) || strings.TrimSpace(source) == "" {
		return nil, fault("expression_value_invalid")
	}
	// Disable constant folding so native construction is charged to the fresh VM,
	// not performed by the compiler. No helpers or AST patches alter Expr semantics.
	// Timezone/date can load host zoneinfo; none of these host-time capabilities
	// belongs to Hearth's JSON-only execution context.
	code, err := expr.Compile(source, expr.Env(environment), expr.MaxNodes(sourceNodes),
		expr.Optimize(false), expr.DisableBuiltin("now"), expr.DisableBuiltin("date"), expr.DisableBuiltin("timezone"))
	if err != nil {
		if located, ok := errors.AsType[*file.Error](err); ok {
			start, end := byteSpan(source, located.Location)
			code := "expression_type"
			if located.Message == "compilation failed: expression exceeds maximum allowed nodes" {
				code = "expression_limit"
			}
			return nil, &Failure{Code: code, Start: start, End: end, Located: true}
		}
		return nil, fault("expression_type")
	}
	return &Program{code: code}, nil
}

func byteSpan(source string, loc file.Location) (int, int) {
	offsets := make([]int, 0, len(source)+1)
	for index := range source {
		offsets = append(offsets, index)
	}
	offsets = append(offsets, len(source))
	start := max(0, min(loc.From, len(offsets)-1))
	end := max(start, min(loc.To, len(offsets)-1))
	return offsets[start], offsets[end]
}
