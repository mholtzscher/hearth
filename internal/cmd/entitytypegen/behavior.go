package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type valueKind string

const (
	kindBoolean valueKind = "boolean"
	kindString  valueKind = "string"
	kindInteger valueKind = "integer"
	kindNumber  valueKind = "number"

	schemaTypeArray  = "array"
	schemaTypeObject = "object"

	ruleOperatorGTE          = "gte"
	ruleOperatorLTE          = "lte"
	ruleOperatorMultipleOf   = "multiple_of"
	ruleOperatorIsTrue       = "is_true"
	ruleOperatorNear         = "near"
	ruleOperatorCircularNear = "circular_near"
	ruleOperatorIn           = "in"
	ruleOperatorInIfPresent  = "in_if_present"
	ruleOperatorEqOptional   = "eq_optional"
	ruleOperatorGTEIfPresent = "gte_if_present"
	ruleOperatorLTEIfPresent = "lte_if_present"

	outcomeObserved   = "observed"
	outcomeDispatched = "dispatched"

	referenceRootParameters = "parameters"
	referenceRootState      = "state"
	referenceRootSupport    = "support"

	int64MagnitudeBits       = 63
	builtinSchemaCount       = 2
	maximumOperationDeadline = 9_223_372_036_854
)

type ruleModel struct {
	Op        string
	Left      referenceModel
	Right     referenceModel
	HasRight  bool
	Tolerance int64
	Modulus   int64
}

type referenceModel struct {
	Root         string
	Path         string
	Kind         valueKind
	GoExpression string
	Optional     bool
}

type referenceRoot struct {
	Schema       schemaNode
	GoExpression string
}

func compileRules(rules []ruleManifest, roots map[string]referenceRoot, context string) ([]ruleModel, error) {
	compiled := make([]ruleModel, 0, len(rules))
	for index, rule := range rules {
		value, compileErr := compileRule(rule, roots)
		if compileErr != nil {
			return nil, fmt.Errorf("%s rule %d: %w", context, index+1, compileErr)
		}
		compiled = append(compiled, value)
	}
	return compiled, nil
}

func compileRule(rule ruleManifest, roots map[string]referenceRoot) (ruleModel, error) {
	// Optional-leaf operators resolve their own references: the shared
	// strict left compilation below rejects optional leaves.
	switch rule.Op {
	case ruleOperatorInIfPresent:
		return compileInIfPresentRule(rule, roots)
	case ruleOperatorEqOptional:
		return compileEqOptionalRule(rule, roots)
	case ruleOperatorGTEIfPresent, ruleOperatorLTEIfPresent:
		return compileIfPresentOrderedRule(rule, roots)
	}
	left, leftErr := compileReference(rule.Left, roots)
	if leftErr != nil {
		return ruleModel{}, fmt.Errorf("left reference: %w", leftErr)
	}
	switch rule.Op {
	case "eq":
		return compileEqualityRule(rule, roots, left)
	case ruleOperatorGTE, ruleOperatorLTE:
		return compileOrderedRule(rule, roots, left)
	case ruleOperatorMultipleOf:
		return compileMultipleOfRule(rule, roots, left)
	case ruleOperatorIn:
		return compileInRule(rule, roots, left)
	case ruleOperatorIsTrue:
		return compileIsTrueRule(rule, left)
	case ruleOperatorNear:
		return compileNearRule(rule, roots, left)
	case ruleOperatorCircularNear:
		return compileCircularNearRule(rule, roots, left)
	default:
		return ruleModel{}, fmt.Errorf("unsupported operator %q", rule.Op)
	}
}

func compileRightReference(rule ruleManifest, roots map[string]referenceRoot) (referenceModel, error) {
	if rule.Right == nil {
		return referenceModel{}, fmt.Errorf("operator %q requires right", rule.Op)
	}
	right, rightErr := compileReference(*rule.Right, roots)
	if rightErr != nil {
		return referenceModel{}, fmt.Errorf("right reference: %w", rightErr)
	}
	return right, nil
}

func compileEqualityRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

func compileOrderedRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	if left.Kind != kindInteger && left.Kind != kindNumber {
		return ruleModel{}, fmt.Errorf("operator %q requires numeric operands, got %s", rule.Op, left.Kind)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

func compileMultipleOfRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	if left.Kind != kindInteger {
		return ruleModel{}, fmt.Errorf("operator %q requires integer operands, got %s", rule.Op, left.Kind)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

func compileInRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileArrayRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != kindString {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires a string left operand, got %s",
			rule.Op,
			left.Kind,
		)
	}
	if _, schemaErr := stringArraySchema(rule.Op, rule.Right, roots, 1); schemaErr != nil {
		return ruleModel{}, schemaErr
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

func compileInIfPresentRule(rule ruleManifest, roots map[string]referenceRoot) (ruleModel, error) {
	left, leftErr := compileOptionalLeafReference(rule.Left, roots)
	if leftErr != nil {
		return ruleModel{}, fmt.Errorf("left reference: %w", leftErr)
	}
	right, rightErr := compileArrayRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if !left.Optional {
		return ruleModel{}, fmt.Errorf("operator %q requires an optional left leaf", rule.Op)
	}
	if left.Kind != kindString {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires a string left operand, got %s",
			rule.Op,
			left.Kind,
		)
	}
	items, schemaErr := stringArraySchema(rule.Op, rule.Right, roots, 0)
	if schemaErr != nil {
		return ruleModel{}, schemaErr
	}
	leftSchema, schemaErr := optionalLeafSchemaValue(rule.Left, roots)
	if schemaErr != nil {
		return ruleModel{}, schemaErr
	}
	if !equalLengthBounds(items, leftSchema) {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires right items matching the left string bounds",
			rule.Op,
		)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

// compileArrayRightReference resolves a membership right operand: a
// required array path. Unlike scalar references it skips the scalar-kind
// check; the membership schema checks in stringArraySchema apply instead.
func compileArrayRightReference(
	rule ruleManifest,
	roots map[string]referenceRoot,
) (referenceModel, error) {
	if rule.Right == nil {
		return referenceModel{}, fmt.Errorf("operator %q requires right", rule.Op)
	}
	root, exists := roots[rule.Right.Root]
	if !exists {
		return referenceModel{}, fmt.Errorf(
			"right reference: root %q is not available in this context",
			rule.Right.Root,
		)
	}
	_, expression, _, pathErr := compileReferencePathAllowingOptionalLeaf(root, rule.Right.Path, false)
	if pathErr != nil {
		return referenceModel{}, fmt.Errorf("right reference: %w", pathErr)
	}
	return referenceModel{Root: rule.Right.Root, Path: rule.Right.Path, GoExpression: expression}, nil
}

func compileEqOptionalRule(rule ruleManifest, roots map[string]referenceRoot) (ruleModel, error) {
	if rule.Right == nil {
		return ruleModel{}, fmt.Errorf("operator %q requires right", rule.Op)
	}
	left, leftErr := compileOptionalLeafReference(rule.Left, roots)
	if leftErr != nil {
		return ruleModel{}, fmt.Errorf("left reference: %w", leftErr)
	}
	right, rightErr := compileOptionalLeafReference(*rule.Right, roots)
	if rightErr != nil {
		return ruleModel{}, fmt.Errorf("right reference: %w", rightErr)
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

func compileIfPresentOrderedRule(rule ruleManifest, roots map[string]referenceRoot) (ruleModel, error) {
	left, leftErr := compileOptionalLeafReference(rule.Left, roots)
	if leftErr != nil {
		return ruleModel{}, fmt.Errorf("left reference: %w", leftErr)
	}
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if !left.Optional {
		return ruleModel{}, fmt.Errorf("operator %q requires an optional left leaf", rule.Op)
	}
	if left.Kind != right.Kind {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires matching operand types, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	if left.Kind != kindInteger && left.Kind != kindNumber {
		return ruleModel{}, fmt.Errorf("operator %q requires numeric operands, got %s", rule.Op, left.Kind)
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true}, nil
}

// stringArraySchema checks a membership right operand: a required string
// array with unique items and at least minimumItems entries. It returns the
// item schema for further bounds checks.
func stringArraySchema(
	operator string,
	reference *referenceManifest,
	roots map[string]referenceRoot,
	minimumItems int,
) (schemaNode, error) {
	schema, err := referenceSchema(*reference, roots)
	if err != nil {
		return schemaNode{}, err
	}
	if schema.Type != schemaTypeArray {
		return schemaNode{}, fmt.Errorf(
			"operator %q requires a string array right operand, got %s",
			operator,
			schema.Type,
		)
	}
	if schema.Items == nil || schema.Items.Type != string(kindString) {
		return schemaNode{}, fmt.Errorf("operator %q requires a string array right operand", operator)
	}
	if schema.UniqueItems == nil || !*schema.UniqueItems {
		return schemaNode{}, fmt.Errorf("operator %q requires a right array with uniqueItems", operator)
	}
	if minimumItems > 0 && (schema.MinItems == nil || *schema.MinItems < minimumItems) {
		return schemaNode{}, fmt.Errorf(
			"operator %q requires a right array with minItems >= %d",
			operator,
			minimumItems,
		)
	}
	return *schema.Items, nil
}

func equalLengthBounds(left, right schemaNode) bool {
	return equalIntPointer(left.MinLength, right.MinLength) &&
		equalIntPointer(left.MaxLength, right.MaxLength)
}

func equalIntPointer(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func compileIsTrueRule(rule ruleManifest, left referenceModel) (ruleModel, error) {
	if rule.Right != nil {
		return ruleModel{}, fmt.Errorf("operator %q does not support right", rule.Op)
	}
	if constantsErr := rejectRuleConstants(rule); constantsErr != nil {
		return ruleModel{}, constantsErr
	}
	if left.Kind != kindBoolean {
		return ruleModel{}, fmt.Errorf("operator %q requires a boolean operand, got %s", rule.Op, left.Kind)
	}
	return ruleModel{Op: rule.Op, Left: left}, nil
}

func compileNearRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if rule.Modulus != nil {
		return ruleModel{}, fmt.Errorf("operator %q does not support modulus", rule.Op)
	}
	if rule.Tolerance == nil {
		return ruleModel{}, fmt.Errorf("operator %q requires tolerance", rule.Op)
	}
	if left.Kind != kindInteger || right.Kind != kindInteger {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires integer operands, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	tolerance, toleranceErr := parseIntegerConstant("tolerance", *rule.Tolerance)
	if toleranceErr != nil {
		return ruleModel{}, toleranceErr
	}
	if tolerance < 0 {
		return ruleModel{}, fmt.Errorf("operator %q requires tolerance >= 0, got %d", rule.Op, tolerance)
	}
	if boundsErr := requireNonnegativeBoundedIntegers(rule.Op, rule.Left, rule.Right, roots); boundsErr != nil {
		return ruleModel{}, boundsErr
	}
	return ruleModel{Op: rule.Op, Left: left, Right: right, HasRight: true, Tolerance: tolerance}, nil
}

func compileCircularNearRule(
	rule ruleManifest,
	roots map[string]referenceRoot,
	left referenceModel,
) (ruleModel, error) {
	right, rightErr := compileRightReference(rule, roots)
	if rightErr != nil {
		return ruleModel{}, rightErr
	}
	if rule.Tolerance == nil {
		return ruleModel{}, fmt.Errorf("operator %q requires tolerance", rule.Op)
	}
	if rule.Modulus == nil {
		return ruleModel{}, fmt.Errorf("operator %q requires modulus", rule.Op)
	}
	if left.Kind != kindInteger || right.Kind != kindInteger {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires integer operands, got %s and %s",
			rule.Op,
			left.Kind,
			right.Kind,
		)
	}
	tolerance, toleranceErr := parseIntegerConstant("tolerance", *rule.Tolerance)
	if toleranceErr != nil {
		return ruleModel{}, toleranceErr
	}
	modulus, modulusErr := parseIntegerConstant("modulus", *rule.Modulus)
	if modulusErr != nil {
		return ruleModel{}, modulusErr
	}
	if modulus <= 0 {
		return ruleModel{}, fmt.Errorf("operator %q requires positive modulus, got %d", rule.Op, modulus)
	}
	// Overflow-safe domain check for 2*tolerance >= modulus: tolerance and
	// modulus are int64, so tolerance*2 can overflow before the comparison.
	// tolerance >= modulus-tolerance is equivalent over the integers without
	// any intermediate overflow (modulus-tolerance cannot overflow for
	// modulus > 0 and tolerance >= 0 within int64 range).
	if tolerance < 0 || tolerance >= modulus-tolerance {
		return ruleModel{}, fmt.Errorf(
			"operator %q requires 0 <= tolerance < modulus/2, got tolerance %d modulus %d",
			rule.Op,
			tolerance,
			modulus,
		)
	}
	if boundsErr := requireNonnegativeBoundedIntegers(rule.Op, rule.Left, rule.Right, roots); boundsErr != nil {
		return ruleModel{}, boundsErr
	}
	if belowErr := requireOperandsBelowModulus(rule.Op, rule.Left, rule.Right, roots, modulus); belowErr != nil {
		return ruleModel{}, belowErr
	}
	return ruleModel{
		Op: rule.Op, Left: left, Right: right, HasRight: true,
		Tolerance: tolerance, Modulus: modulus,
	}, nil
}

func rejectRuleConstants(rule ruleManifest) error {
	if rule.Tolerance != nil {
		return fmt.Errorf("operator %q does not support tolerance", rule.Op)
	}
	if rule.Modulus != nil {
		return fmt.Errorf("operator %q does not support modulus", rule.Op)
	}
	return nil
}

func parseIntegerConstant(name string, raw json.Number) (int64, error) {
	rational, ok := new(big.Rat).SetString(raw.String())
	if !ok {
		return 0, fmt.Errorf("%s %q is not a number", name, raw.String())
	}
	if !rational.IsInt() {
		return 0, fmt.Errorf("%s %q must be an integer", name, raw.String())
	}
	numerator := rational.Num()
	if !numerator.IsInt64() {
		return 0, fmt.Errorf("%s %q is outside int64", name, raw.String())
	}
	return numerator.Int64(), nil
}

func requireNonnegativeBoundedIntegers(
	operator string,
	left referenceManifest,
	right *referenceManifest,
	roots map[string]referenceRoot,
) error {
	for _, reference := range []referenceManifest{left, *right} {
		schema, schemaErr := referenceSchema(reference, roots)
		if schemaErr != nil {
			return schemaErr
		}
		minimum, _, boundsErr := integerBounds(schema)
		if boundsErr != nil {
			return fmt.Errorf("operator %q: %w", operator, boundsErr)
		}
		if minimum < 0 {
			return fmt.Errorf("operator %q requires nonnegative integer operands", operator)
		}
	}
	return nil
}

func requireOperandsBelowModulus(
	operator string,
	left referenceManifest,
	right *referenceManifest,
	roots map[string]referenceRoot,
	modulus int64,
) error {
	for _, reference := range []referenceManifest{left, *right} {
		schema, schemaErr := referenceSchema(reference, roots)
		if schemaErr != nil {
			return schemaErr
		}
		_, maximum, boundsErr := integerBounds(schema)
		if boundsErr != nil {
			return fmt.Errorf("operator %q: %w", operator, boundsErr)
		}
		if maximum >= modulus {
			return fmt.Errorf(
				"operator %q requires operands below modulus %d",
				operator,
				modulus,
			)
		}
	}
	return nil
}

func integerBounds(schema schemaNode) (int64, int64, error) {
	if schema.Type != string(kindInteger) {
		return 0, 0, fmt.Errorf("requires integer operands, got %s", schema.Type)
	}
	if schema.Minimum == nil || schema.Maximum == nil {
		return 0, 0, errors.New("distance operands must be bounded integers with minimum and maximum")
	}
	minimum, minimumErr := parseIntegerConstant("minimum", *schema.Minimum)
	if minimumErr != nil {
		return 0, 0, minimumErr
	}
	maximum, maximumErr := parseIntegerConstant("maximum", *schema.Maximum)
	if maximumErr != nil {
		return 0, 0, maximumErr
	}
	return minimum, maximum, nil
}

func referenceSchema(reference referenceManifest, roots map[string]referenceRoot) (schemaNode, error) {
	root, exists := roots[reference.Root]
	if !exists {
		return schemaNode{}, fmt.Errorf("root %q is not available in this context", reference.Root)
	}
	schema, _, pathErr := compileReferencePath(root, reference.Path)
	if pathErr != nil {
		return schemaNode{}, pathErr
	}
	return schema, nil
}

func compileReference(reference referenceManifest, roots map[string]referenceRoot) (referenceModel, error) {
	root, exists := roots[reference.Root]
	if !exists {
		return referenceModel{}, fmt.Errorf("root %q is not available in this context", reference.Root)
	}
	schema, expression, pathErr := compileReferencePath(root, reference.Path)
	if pathErr != nil {
		return referenceModel{}, pathErr
	}
	kind, kindErr := scalarKind(schema)
	if kindErr != nil {
		return referenceModel{}, fmt.Errorf("path %q: %w", reference.Path, kindErr)
	}
	return referenceModel{Root: reference.Root, Path: reference.Path, Kind: kind, GoExpression: expression}, nil
}

func compileOptionalLeafReference(
	reference referenceManifest,
	roots map[string]referenceRoot,
) (referenceModel, error) {
	root, exists := roots[reference.Root]
	if !exists {
		return referenceModel{}, fmt.Errorf("root %q is not available in this context", reference.Root)
	}
	schema, expression, optional, pathErr := compileReferencePathAllowingOptionalLeaf(
		root,
		reference.Path,
		true,
	)
	if pathErr != nil {
		return referenceModel{}, pathErr
	}
	kind, kindErr := scalarKind(schema)
	if kindErr != nil {
		return referenceModel{}, fmt.Errorf("path %q: %w", reference.Path, kindErr)
	}
	return referenceModel{
		Root: reference.Root, Path: reference.Path, Kind: kind,
		GoExpression: expression, Optional: optional,
	}, nil
}

func optionalLeafSchemaValue(
	reference referenceManifest,
	roots map[string]referenceRoot,
) (schemaNode, error) {
	root, exists := roots[reference.Root]
	if !exists {
		return schemaNode{}, fmt.Errorf("root %q is not available in this context", reference.Root)
	}
	schema, _, _, pathErr := compileReferencePathAllowingOptionalLeaf(root, reference.Path, true)
	if pathErr != nil {
		return schemaNode{}, pathErr
	}
	return schema, nil
}

func compileReferencePath(root referenceRoot, path string) (schemaNode, string, error) {
	schema, expression, _, err := compileReferencePathAllowingOptionalLeaf(root, path, false)
	return schema, expression, err
}

func compileReferencePathAllowingOptionalLeaf(
	root referenceRoot,
	path string,
	allowOptionalLeaf bool,
) (schemaNode, string, bool, error) {
	schema := root.Schema
	expression := root.GoExpression
	if path == "" {
		return schema, expression, false, nil
	}
	segments, pointerErr := parseJSONPointer(path)
	if pointerErr != nil {
		return schemaNode{}, "", false, pointerErr
	}
	var suffix strings.Builder
	optional := false
	for index, segment := range segments {
		if schema.Type != schemaTypeObject {
			return schemaNode{}, "", false, fmt.Errorf("path %q traverses non-object type %q", path, schema.Type)
		}
		property, exists := schema.Properties[segment]
		if !exists {
			return schemaNode{}, "", false, fmt.Errorf("path %q selects unknown property %q", path, segment)
		}
		if !required(schema, segment) {
			if !allowOptionalLeaf || index != len(segments)-1 {
				return schemaNode{}, "", false, fmt.Errorf("path %q traverses optional property %q", path, segment)
			}
			optional = true
		}
		field, nameErr := exportedName(segment)
		if nameErr != nil {
			return schemaNode{}, "", false, nameErr
		}
		suffix.WriteString("." + field)
		schema = property
	}
	return schema, expression + suffix.String(), optional, nil
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
	case schemaTypeObject, schemaTypeArray:
		return "", errors.New("behavior references must select scalar values")
	default:
		return "", fmt.Errorf("unsupported scalar type %q", schema.Type)
	}
}

func satisfactionCondition(rules []ruleModel) string {
	conditions := make([]string, 0, len(rules))
	for _, rule := range rules {
		conditions = append(conditions, "("+ruleCondition(rule)+")")
	}
	return strings.Join(conditions, " && ")
}

func ruleCondition(rule ruleModel) string {
	switch rule.Op {
	case "eq":
		return ruleOperand(rule.Left) + " == " + ruleOperand(rule.Right)
	case ruleOperatorGTE:
		return ruleOperand(rule.Left) + " >= " + ruleOperand(rule.Right)
	case ruleOperatorLTE:
		return ruleOperand(rule.Left) + " <= " + ruleOperand(rule.Right)
	case ruleOperatorMultipleOf:
		return ruleOperand(rule.Right) + " != 0 && " + ruleOperand(rule.Left) + "%" + ruleOperand(rule.Right) + " == 0"
	case ruleOperatorIsTrue:
		return ruleOperand(rule.Left)
	case ruleOperatorIn:
		return membershipCondition(ruleOperand(rule.Left), rule.Right.GoExpression)
	case ruleOperatorInIfPresent:
		return "(" + rule.Left.GoExpression + " == nil || " +
			membershipCondition(optionalRuleOperand(rule.Left), rule.Right.GoExpression) + ")"
	case ruleOperatorEqOptional:
		return eqOptionalCondition(rule)
	case ruleOperatorGTEIfPresent:
		return "(" + rule.Left.GoExpression + " == nil || " +
			optionalRuleOperand(rule.Left) + " >= " + ruleOperand(rule.Right) + ")"
	case ruleOperatorLTEIfPresent:
		return "(" + rule.Left.GoExpression + " == nil || " +
			optionalRuleOperand(rule.Left) + " <= " + ruleOperand(rule.Right) + ")"
	case ruleOperatorNear:
		left := ruleOperand(rule.Left)
		right := ruleOperand(rule.Right)
		tolerance := strconv.FormatInt(rule.Tolerance, 10)
		return "(" + left + " >= " + right + " && " + left + "-" + right + " <= " + tolerance + ") || " +
			"(" + right + " > " + left + " && " + right + "-" + left + " <= " + tolerance + ")"
	case ruleOperatorCircularNear:
		left := ruleOperand(rule.Left)
		right := ruleOperand(rule.Right)
		return fmt.Sprintf(
			"(func() bool { l := %s; r := %s; var d int64; if l >= r { d = l - r } else { d = r - l }; return d <= %d || %d-d <= %d }())",
			left,
			right,
			rule.Tolerance,
			rule.Modulus,
			rule.Tolerance,
		)
	default:
		panic("render unsupported rule operator " + rule.Op)
	}
}

func ruleOperand(reference referenceModel) string {
	return goTypeName(reference.Kind) + "(" + reference.GoExpression + ")"
}

func optionalRuleOperand(reference referenceModel) string {
	return goTypeName(reference.Kind) + "(*" + reference.GoExpression + ")"
}

func goTypeName(kind valueKind) string {
	return map[valueKind]string{
		kindBoolean: "bool",
		kindString:  string(kindString),
		kindInteger: "int64",
		kindNumber:  "float64",
	}[kind]
}

// membershipCondition emits a string-membership loop over a support choice
// array. Both operators using it are string-only by compile-time checks.
func membershipCondition(leftOperand, rightExpression string) string {
	return "(func() bool { for _, candidate := range " + rightExpression +
		" { if " + leftOperand + " == string(candidate) { return true } }; return false }())"
}

// eqOptionalCondition compares two discriminated payload leaves where
// absence participates: both absent is true, one-sided absence is false,
// and present values must be equal.
func eqOptionalCondition(rule ruleModel) string {
	left, right := rule.Left, rule.Right
	switch {
	case left.Optional && right.Optional:
		return "((" + left.GoExpression + " == nil && " + right.GoExpression + " == nil) || (" +
			left.GoExpression + " != nil && " + right.GoExpression + " != nil && " +
			optionalRuleOperand(left) + " == " + optionalRuleOperand(right) + "))"
	case left.Optional:
		return "(" + left.GoExpression + " != nil && " +
			optionalRuleOperand(left) + " == " + ruleOperand(right) + ")"
	case right.Optional:
		return "(" + right.GoExpression + " != nil && " +
			ruleOperand(left) + " == " + optionalRuleOperand(right) + ")"
	default:
		return ruleOperand(left) + " == " + ruleOperand(right)
	}
}

func ruleDescription(rule ruleModel) string {
	switch rule.Op {
	case ruleOperatorIsTrue:
		return referenceDescription(rule.Left) + " must be true"
	case ruleOperatorNear:
		return fmt.Sprintf(
			"%s must be within %d of %s",
			referenceDescription(rule.Left),
			rule.Tolerance,
			referenceDescription(rule.Right),
		)
	case ruleOperatorCircularNear:
		return fmt.Sprintf(
			"%s must be within %d of %s modulo %d",
			referenceDescription(rule.Left),
			rule.Tolerance,
			referenceDescription(rule.Right),
			rule.Modulus,
		)
	case ruleOperatorIn:
		return referenceDescription(rule.Left) + " must be one of " + referenceDescription(rule.Right)
	case ruleOperatorInIfPresent:
		return referenceDescription(rule.Left) + " must be absent or one of " + referenceDescription(rule.Right)
	case ruleOperatorEqOptional:
		return referenceDescription(rule.Left) + " must be equal to " + referenceDescription(rule.Right) +
			" (absent matches only absent)"
	case ruleOperatorGTEIfPresent:
		return referenceDescription(rule.Left) + " must be absent or greater than or equal to " +
			referenceDescription(rule.Right)
	case ruleOperatorLTEIfPresent:
		return referenceDescription(rule.Left) + " must be absent or less than or equal to " +
			referenceDescription(rule.Right)
	}
	symbol := map[string]string{
		"eq": "equal", ruleOperatorGTE: "greater than or equal to",
		ruleOperatorLTE: "less than or equal to", ruleOperatorMultipleOf: "a multiple of",
	}[rule.Op]
	return referenceDescription(rule.Left) + " must be " + symbol + " " + referenceDescription(rule.Right)
}

func referenceDescription(reference referenceModel) string {
	if reference.Path == "" {
		return reference.Root
	}
	return reference.Root + reference.Path
}
