package automations

import "encoding/json"

// These conversions consume schema-validated, disjoint wire alternatives.
// Literal JSON is never inspected for expression-shaped vendor properties.
func decodeValueFields(literal json.RawMessage, source *string) ValueBinding {
	if source != nil {
		return ValueBinding{Body: ExpressionValue{Source: *source}}
	}
	return ValueBinding{Body: LiteralValue{Value: literal}}
}

func encodeValueFields(binding ValueBinding) (json.RawMessage, *string) {
	switch body := binding.Body.(type) {
	case LiteralValue:
		return body.Value, nil
	case ExpressionValue:
		return nil, &body.Source
	default:
		panic("invalid prepared value binding")
	}
}

func decodeVariableFields(
	literals map[VariableName]json.RawMessage,
	expressions map[VariableName]string,
) map[VariableName]ValueBinding {
	if literals == nil && expressions == nil {
		return nil
	}
	bindings := make(map[VariableName]ValueBinding, len(literals)+len(expressions))
	for name, literal := range literals {
		bindings[name] = ValueBinding{Body: LiteralValue{Value: literal}}
	}
	for name, source := range expressions {
		bindings[name] = ValueBinding{Body: ExpressionValue{Source: source}}
	}
	return bindings
}

func encodeVariableFields(
	bindings map[VariableName]ValueBinding,
) (map[VariableName]json.RawMessage, map[VariableName]string) {
	literals := map[VariableName]json.RawMessage{}
	expressions := map[VariableName]string{}
	for name, binding := range bindings {
		switch body := binding.Body.(type) {
		case LiteralValue:
			literals[name] = body.Value
		case ExpressionValue:
			expressions[name] = body.Source
		default:
			panic("invalid prepared variable binding")
		}
	}
	return literals, expressions
}

// JSON Schema validates each map; the codec additionally owns disjoint names.
func validateVariableFieldNames(object map[string]any, field, path string) error {
	literals, _ := object[field].(map[string]any)
	expressions, _ := object[field+"_expr"].(map[string]any)
	for name := range literals {
		if _, overlap := expressions[name]; overlap {
			return definitionIssue(path+"/"+field+"_expr", "literal and expression names must not overlap")
		}
	}
	return nil
}
