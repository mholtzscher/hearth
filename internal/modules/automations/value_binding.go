//nolint:goconst // Binding diagnostics use the bridge's fixed failure-code protocol.
package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// ValueBinding is either opaque JSON or a prepared pure expression.
type ValueBinding struct{ Body ValueBindingBody }

//sumtype:decl
type ValueBindingBody interface{ isValueBindingBody() }
type LiteralValue struct{ Value json.RawMessage }
type ExpressionValue struct {
	Source  string
	program *exprvalue.Program
}

func (LiteralValue) isValueBindingBody()    {}
func (ExpressionValue) isValueBindingBody() {}

type VariableName string

type bindingError struct {
	path  string
	cause error
}

func (failure *bindingError) Error() string { return "automation value resolution failed" }
func (failure *bindingError) Unwrap() error { return failure.cause }
func (*bindingError) Is(target error) bool  { return target == ErrInvalidAutomation }

func dataFailureCode(err error) string {
	if failure, ok := errors.AsType[*exprvalue.Failure](err); ok {
		return failure.Code
	}
	return "resolved_input_invalid"
}

const (
	automationVariableMaxCount = 64
	automationValueMaxBytes    = 65536
	automationValueMaxDepth    = 16
)

type ExpressionCondition struct{ Value ValueBinding }

func (ExpressionCondition) isConditionBody() {}

var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func validVariableName(name VariableName) bool {
	return variableNamePattern.MatchString(string(name)) && !strings.HasPrefix(string(name), "__hearth_")
}

func normalizeValueBinding(binding ValueBinding) (ValueBinding, error) {
	return prepareValueBinding(binding, false)
}

func prepareValueBinding(binding ValueBinding, initial bool) (ValueBinding, error) {
	switch body := binding.Body.(type) {
	case LiteralValue:
		if len(body.Value) == 0 || !utf8.Valid(body.Value) {
			return ValueBinding{}, invalid("literal value is required")
		}
		if _, err := decodeJSONValue(body.Value); err != nil {
			return ValueBinding{}, invalid("invalid literal JSON")
		}
		return ValueBinding{Body: LiteralValue{Value: bytes.Clone(body.Value)}}, nil
	case ExpressionValue:
		compile := exprvalue.Compile
		if initial {
			compile = exprvalue.CompileInitial
		}
		program, err := compile(body.Source)
		if err != nil {
			return ValueBinding{}, &bindingError{cause: err}
		}
		return ValueBinding{Body: ExpressionValue{Source: body.Source, program: program}}, nil
	default:
		return ValueBinding{}, invalid("unsupported value binding")
	}
}

//nolint:nilnil // A nil map preserves omission of optional initial variables.
func normalizeVariables(input map[VariableName]ValueBinding, initial bool) (map[VariableName]ValueBinding, error) {
	if len(input) > automationVariableMaxCount {
		return nil, invalid("variable count exceeds bound")
	}
	if input == nil {
		return nil, nil
	}
	output := make(map[VariableName]ValueBinding, len(input))
	for name, binding := range input {
		if !validVariableName(name) {
			return nil, invalid("invalid variable name")
		}
		value, err := prepareValueBinding(binding, initial)
		if err != nil {
			field := "assignments"
			if initial {
				field = "variables"
			}
			return nil, bindingAt(err, bindingField(binding, field)+"/"+string(name))
		}
		output[name] = value
	}
	return output, nil
}

func bindingAt(err error, path string) error {
	if err == nil {
		return nil
	}
	if nested, ok := errors.AsType[*bindingError](err); ok && nested.path != "" {
		path += "/" + nested.path
	}
	return &bindingError{path: path, cause: err}
}

func literalBindingJSON(binding ValueBinding) json.RawMessage {
	if literal, ok := binding.Body.(LiteralValue); ok {
		return literal.Value
	}
	return nil
}

// bindingField locates the actual wire field without changing the domain sum type.
func bindingField(binding ValueBinding, field string) string {
	if _, expression := binding.Body.(ExpressionValue); expression {
		return field + "_expr"
	}
	return field
}

// hasExpressionCondition resolves a diagnostic within one prepared Condition root.
func hasExpressionCondition(root Condition, id ConditionID) bool {
	var children []Condition
	switch body := root.Body.(type) {
	case ExpressionCondition:
		return root.ID == id
	case AllCondition:
		children = body.Children
	case AnyCondition:
		children = body.Children
	case NotCondition:
		return hasExpressionCondition(body.Child, id)
	case EntityStateCondition, TriggerCondition:
	}
	for _, child := range children {
		if hasExpressionCondition(child, id) {
			return true
		}
	}
	return false
}
