//nolint:goconst // Binding diagnostics use the strict bridge's fixed failure-code protocol.
package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
	automationVariableMaxCount     = 64
	automationValueMaxBytes        = 65536
	automationValueMaxDepth        = 16
	resolvedNotificationFieldCount = 2
)

type ExpressionCondition struct{ Value ValueBinding }

func (ExpressionCondition) isConditionBody() {}

var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func validVariableName(name VariableName) bool {
	return variableNamePattern.MatchString(string(name)) && !strings.HasPrefix(string(name), "__hearth_")
}

func (binding ValueBinding) MarshalJSON() ([]byte, error) {
	switch body := binding.Body.(type) {
	case LiteralValue:
		return json.Marshal(struct {
			Literal json.RawMessage `json:"literal"`
		}{body.Value})
	case ExpressionValue:
		return json.Marshal(struct {
			Expr string `json:"expr"`
		}{body.Source})
	default:
		return nil, invalid("unsupported value binding")
	}
}

func (binding *ValueBinding) UnmarshalJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return invalid("binding must contain valid UTF-8 JSON")
	}
	// Token decoding also rejects duplicate envelope keys without interpreting
	// keys within an opaque literal as binding syntax.
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid("binding must be an object")
	}
	if !d.More() {
		return invalid("binding requires exactly one variant")
	}
	token, err := d.Token()
	if err != nil {
		return invalid("invalid binding")
	}
	var fragment json.RawMessage
	if err = d.Decode(&fragment); err != nil || d.More() {
		return invalid("binding requires exactly one variant")
	}
	if _, err = d.Token(); err != nil {
		return invalid("invalid binding")
	}
	if _, err = d.Token(); err != io.EOF {
		return invalid("invalid binding")
	}
	switch token {
	case "literal":
		binding.Body = LiteralValue{Value: bytes.Clone(fragment)}
	case "expr":
		var source string
		if json.Unmarshal(fragment, &source) != nil || strings.TrimSpace(source) == "" {
			return invalid("expression must be a nonempty string")
		}
		binding.Body = ExpressionValue{Source: source}
	default:
		return invalid("unsupported binding member")
	}
	return nil
}

func normalizeValueBinding(binding ValueBinding) (ValueBinding, error) {
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
		program, err := exprvalue.Compile(body.Source)
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
		value, err := normalizeValueBinding(binding)
		if err != nil {
			return nil, bindingAt(err, string(name))
		}
		if body, ok := value.Body.(ExpressionValue); ok && initial {
			for _, root := range body.program.Roots() {
				if root == "vars" || root == "steps" {
					return nil, &bindingError{path: string(name), cause: &exprvalue.Failure{Code: "expression_type"}}
				}
			}
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

//nolint:gocognit // Exhaustive definition traversal distinguishes output producers from all binding consumers.
func validateStaticOutputReferences(steps []Step, variables map[VariableName]ValueBinding) error {
	outputs := map[string]bool{}
	var bindings []ValueBinding
	for _, binding := range variables {
		bindings = append(bindings, binding)
	}
	var condition func(Condition)
	condition = func(root Condition) {
		switch body := root.Body.(type) {
		case ExpressionCondition:
			bindings = append(bindings, body.Value)
		case AllCondition:
			for _, child := range body.Children {
				condition(child)
			}
		case AnyCondition:
			for _, child := range body.Children {
				condition(child)
			}
		case NotCondition:
			condition(body.Child)
		case EntityStateCondition, TriggerCondition:
		}
	}
	var visit func([]Step)
	visit = func(sequence []Step) {
		for _, step := range sequence {
			switch body := step.Body.(type) {
			case CommandStep:
				bindings = append(bindings, body.Parameters)
			case FunctionStep:
				outputs[string(step.ID)] = true
				bindings = append(bindings, body.Arguments)
			case NotificationStep:
				bindings = append(bindings, body.Title, body.Message)
			case ReadStateStep:
				outputs[string(step.ID)] = true
			case SetVariablesStep:
				for _, binding := range body.Assignments {
					bindings = append(bindings, binding)
				}
			case IfStep:
				condition(body.Conditions)
			case ChooseStep:
				for _, branch := range body.Branches {
					condition(branch.Conditions)
				}
			case DelayStep:
			}
			for _, children := range snapshotChildSequences(step) {
				visit(children)
			}
		}
	}
	visit(steps)
	for _, binding := range bindings {
		if body, ok := binding.Body.(ExpressionValue); ok {
			for _, id := range body.program.StepReferences() {
				if !outputs[id] {
					return invalid("expression references an unknown or non-output Step")
				}
			}
		}
	}
	return nil
}
