package main

import (
	"errors"
	"fmt"
	"strings"
)

type valueKind string

const (
	kindBoolean valueKind = "boolean"
	kindString  valueKind = "string"
	kindInteger valueKind = "integer"
	kindNumber  valueKind = "number"
)

type ruleModel struct {
	Op    string
	Left  referenceModel
	Right referenceModel
}

type referenceModel struct {
	Root         string
	Path         string
	Kind         valueKind
	GoExpression string
}

type referenceRoot struct {
	Schema       schemaNode
	GoExpression string
}

func compileRules(rules []ruleManifest, roots map[string]referenceRoot, context string) ([]ruleModel, error) {
	compiled := make([]ruleModel, 0, len(rules))
	for index, rule := range rules {
		value, err := compileRule(rule, roots)
		if err != nil {
			return nil, fmt.Errorf("%s rule %d: %w", context, index+1, err)
		}
		compiled = append(compiled, value)
	}
	return compiled, nil
}

func compileRule(rule ruleManifest, roots map[string]referenceRoot) (ruleModel, error) {
	left, err := compileReference(rule.Left, roots)
	if err != nil {
		return ruleModel{}, fmt.Errorf("left reference: %w", err)
	}
	right, err := compileReference(rule.Right, roots)
	if err != nil {
		return ruleModel{}, fmt.Errorf("right reference: %w", err)
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	switch rule.Op {
	case "eq":
	case "lte":
		if left.Kind != kindInteger && left.Kind != kindNumber {
			return ruleModel{}, fmt.Errorf("operator %q requires numeric operands, got %s", rule.Op, left.Kind)
		}
	case "multiple_of":
		if left.Kind != kindInteger {
			return ruleModel{}, fmt.Errorf("operator %q requires integer operands, got %s", rule.Op, left.Kind)
		}
	default:
		return ruleModel{}, fmt.Errorf("unsupported operator %q", rule.Op)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right}, nil
}

func compileReference(reference referenceManifest, roots map[string]referenceRoot) (referenceModel, error) {
	root, exists := roots[reference.Root]
	if !exists {
		return referenceModel{}, fmt.Errorf("root %q is not available in this context", reference.Root)
	}
	schema := root.Schema
	expression := root.GoExpression
	if reference.Path != "" {
		segments, err := parseJSONPointer(reference.Path)
		if err != nil {
			return referenceModel{}, err
		}
		var expressionSb88 strings.Builder
		for _, segment := range segments {
			if schema.Type != "object" {
				return referenceModel{}, fmt.Errorf("path %q traverses non-object type %q", reference.Path, schema.Type)
			}
			property, exists := schema.Properties[segment]
			if !exists {
				return referenceModel{}, fmt.Errorf("path %q selects unknown property %q", reference.Path, segment)
			}
			if !required(schema, segment) {
				return referenceModel{}, fmt.Errorf("path %q traverses optional property %q", reference.Path, segment)
			}
			field, err := exportedName(segment)
			if err != nil {
				return referenceModel{}, err
			}
			expressionSb88.WriteString("." + field)
			schema = property
		}
		expression += expressionSb88.String()
	}
	kind, err := scalarKind(schema)
	if err != nil {
		return referenceModel{}, fmt.Errorf("path %q: %w", reference.Path, err)
	}
	return referenceModel{Root: reference.Root, Path: reference.Path, Kind: kind, GoExpression: expression}, nil
}

func parseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("path %q is not an RFC 6901 JSON Pointer", pointer)
	}
	encoded := strings.Split(pointer[1:], "/")
	segments := make([]string, len(encoded))
	for index, segment := range encoded {
		var decoded strings.Builder
		for offset := 0; offset < len(segment); offset++ {
			if segment[offset] != '~' {
				decoded.WriteByte(segment[offset])
				continue
			}
			if offset+1 >= len(segment) {
				return nil, fmt.Errorf("path %q contains an invalid escape", pointer)
			}
			offset++
			switch segment[offset] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, fmt.Errorf("path %q contains an invalid escape", pointer)
			}
		}
		segments[index] = decoded.String()
	}
	return segments, nil
}

func scalarKind(schema schemaNode) (valueKind, error) {
	switch schema.Type {
	case string(kindBoolean):
		return kindBoolean, nil
	case string(kindString):
		return kindString, nil
	case string(kindInteger):
		return kindInteger, nil
	case string(kindNumber):
		return kindNumber, nil
	case "object", "array":
		return "", errors.New("behavior references must select scalar values")
	default:
		return "", fmt.Errorf("unsupported scalar type %q", schema.Type)
	}
}

func ruleCondition(rule ruleModel) string {
	left := ruleOperand(rule.Left)
	right := ruleOperand(rule.Right)
	switch rule.Op {
	case "eq":
		return left + " == " + right
	case "lte":
		return left + " <= " + right
	case "multiple_of":
		return right + " != 0 && " + left + "%" + right + " == 0"
	default:
		panic("render unsupported rule operator " + rule.Op)
	}
}

func ruleOperand(reference referenceModel) string {
	goType := map[valueKind]string{
		kindBoolean: "bool",
		kindString:  "string",
		kindInteger: "int64",
		kindNumber:  "float64",
	}[reference.Kind]
	return goType + "(" + reference.GoExpression + ")"
}

func ruleDescription(rule ruleModel) string {
	symbol := map[string]string{"eq": "equal", "lte": "less than or equal to", "multiple_of": "a multiple of"}[rule.Op]
	return referenceDescription(rule.Left) + " must be " + symbol + " " + referenceDescription(rule.Right)
}

func referenceDescription(reference referenceModel) string {
	if reference.Path == "" {
		return reference.Root
	}
	return reference.Root + reference.Path
}
