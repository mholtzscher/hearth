package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationPointerMaxBytes bounds one comparison pointer in UTF-8 bytes.
const automationPointerMaxBytes = 256

// ValidateJSONPointer reports whether pointer is a syntactically valid RFC 6901
// JSON Pointer of at most 256 UTF-8 bytes. The empty pointer selects the whole
// value. Only ~0 and ~1 are accepted as escapes, and no token is interpreted
// here: array index rules are applied when the pointer is read against a value.
func ValidateJSONPointer(pointer string) error {
	if len(pointer) > automationPointerMaxBytes {
		return fmt.Errorf("%w: pointer exceeds %d bytes", ErrInvalidAutomation, automationPointerMaxBytes)
	}
	if pointer == "" {
		return nil
	}
	if pointer[0] != '/' {
		return fmt.Errorf("%w: pointer must start with / or be empty", ErrInvalidAutomation)
	}
	for index := 1; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		index++
		if index >= len(pointer) || (pointer[index] != '0' && pointer[index] != '1') {
			return fmt.Errorf("%w: pointer uses an invalid ~ escape", ErrInvalidAutomation)
		}
	}
	return nil
}

// ValidateObservationComparison reports whether one comparison may be persisted:
// the pointer is valid, the operator is closed to eq, ne, lt, lte, gt and gte,
// the operand is exactly one JSON value, and an ordering operator has a numeric
// operand. Pointer existence cannot be proven against future values, so it is
// deliberately not checked here.
func ValidateObservationComparison(comparison ObservationComparison) error {
	if err := ValidateJSONPointer(comparison.Pointer); err != nil {
		return err
	}
	if !comparison.Operator.isKnown() {
		return fmt.Errorf("%w: comparison operator %q is not supported", ErrInvalidAutomation, comparison.Operator)
	}
	operand, err := decodeJSONValue(comparison.Operand)
	if err != nil {
		return fmt.Errorf("%w: comparison operand is not exactly one JSON value", ErrInvalidAutomation)
	}
	if comparison.Operator.isOrdering() {
		if _, numeric := jsonNumberValue(operand); !numeric {
			return fmt.Errorf("%w: ordering comparison requires a numeric operand", ErrInvalidAutomation)
		}
	}
	return nil
}

// MatchObservationComparison reports whether one comparison matches the given
// Observation value. A missing pointer, an array index that is invalid for the
// runtime array, or a value and operand that are not comparable returns false,
// including for ne; those are ordinary evidence, not admission errors. An error
// is returned only when the comparison or value itself is not one JSON value,
// which no validated comparison can produce at runtime.
func MatchObservationComparison(comparison ObservationComparison, value devices.Value) (bool, error) {
	operand, err := decodeJSONValue(comparison.Operand)
	if err != nil {
		return false, fmt.Errorf("%w: comparison operand is not exactly one JSON value", ErrInvalidAutomation)
	}
	tokens, err := parseJSONPointer(comparison.Pointer)
	if err != nil {
		return false, err
	}
	document, err := decodeJSONValue(json.RawMessage(value))
	if err != nil {
		return false, fmt.Errorf("%w: observation value is not exactly one JSON value", ErrInvalidAutomation)
	}
	selected, found := lookupJSONPointer(document, tokens)
	if !found {
		return false, nil
	}
	return compareJSONValues(comparison.Operator, selected, operand)
}

// isOrdering reports whether operator compares two finite JSON numbers.
func (operator ComparisonOperator) isOrdering() bool {
	switch operator {
	case ComparisonLessThan, ComparisonLessThanOrEqual, ComparisonGreaterThan, ComparisonGreaterThanOrEqual:
		return true
	case ComparisonEqual, ComparisonNotEqual:
		return false
	default:
		return false
	}
}

// isKnown reports whether operator is one of the six closed comparison operators.
func (operator ComparisonOperator) isKnown() bool {
	switch operator {
	case ComparisonEqual, ComparisonNotEqual, ComparisonLessThan,
		ComparisonLessThanOrEqual, ComparisonGreaterThan, ComparisonGreaterThanOrEqual:
		return true
	default:
		return false
	}
}

// compareOrdering reports the ordering verdict for one exact rational
// comparison. ok is false when operator is not an ordering operator.
func (operator ComparisonOperator) compareOrdering(compared int) (bool, bool) {
	switch operator {
	case ComparisonLessThan:
		return compared < 0, true
	case ComparisonLessThanOrEqual:
		return compared <= 0, true
	case ComparisonGreaterThan:
		return compared > 0, true
	case ComparisonGreaterThanOrEqual:
		return compared >= 0, true
	case ComparisonEqual, ComparisonNotEqual:
		return false, false
	default:
		return false, false
	}
}

func parseJSONPointer(pointer string) ([]string, error) {
	if err := ValidateJSONPointer(pointer); err != nil {
		return nil, err
	}
	if pointer == "" {
		return nil, nil
	}
	segments := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(segments))
	for index, segment := range segments {
		tokens[index] = unescapeJSONPointerToken(segment)
	}
	return tokens, nil
}

func unescapeJSONPointerToken(token string) string {
	if !strings.Contains(token, "~") {
		return token
	}
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

func lookupJSONPointer(document any, tokens []string) (any, bool) {
	current := document
	for _, token := range tokens {
		switch node := current.(type) {
		case map[string]any:
			selected, found := node[token]
			if !found {
				return nil, false
			}
			current = selected
		case []any:
			index, valid := canonicalArrayIndex(token)
			if !valid || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// canonicalArrayIndex parses one RFC 6901 array token: non-negative decimal
// without leading zeroes, with "-" invalid for reads.
func canonicalArrayIndex(token string) (int, bool) {
	if token == "" || token == "-" {
		return 0, false
	}
	if len(token) > 1 && token[0] == '0' {
		return 0, false
	}
	for index := range len(token) {
		if token[index] < '0' || token[index] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(token)
	if err != nil {
		return 0, false
	}
	return value, true
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing content after JSON value")
	}
	return value, nil
}

func compareJSONValues(operator ComparisonOperator, left, right any) (bool, error) {
	if !operator.isKnown() {
		return false, fmt.Errorf("%w: comparison operator %q is not supported", ErrInvalidAutomation, operator)
	}
	if operator.isOrdering() {
		leftNumber, leftNumeric := jsonNumberValue(left)
		rightNumber, rightNumeric := jsonNumberValue(right)
		if !leftNumeric || !rightNumeric {
			return false, nil
		}
		result, ok := operator.compareOrdering(leftNumber.Cmp(rightNumber))
		if !ok {
			return false, nil
		}
		return result, nil
	}
	// An incompatible top-level JSON type is false for eq and ne alike, unlike an
	// unequal container whose contents simply differ, so it is rejected before
	// equality is inverted for ne.
	if jsonValueKind(left) != jsonValueKind(right) {
		return false, nil
	}
	equal, err := jsonValuesEqual(left, right)
	if err != nil {
		return false, err
	}
	if operator == ComparisonNotEqual {
		return !equal, nil
	}
	return equal, nil
}

// jsonValueKind classifies one decoded JSON value so equality and inequality
// require the same JSON type. [json.Number] and float64 are both numbers because
// an operand decoded without UseNumber would otherwise look like a third type.
func jsonValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// jsonValuesEqual reports whether two decoded JSON values are equal. It first
// requires the same JSON type, including at every nested array element and
// object member, so a nested null never equals a value of another type.
func jsonValuesEqual(left, right any) (bool, error) {
	if jsonValueKind(left) != jsonValueKind(right) {
		return false, nil
	}
	switch leftValue := left.(type) {
	case nil:
		return true, nil
	case bool:
		rightValue, ok := right.(bool)
		return ok && leftValue == rightValue, nil
	case string:
		rightValue, ok := right.(string)
		return ok && leftValue == rightValue, nil
	case json.Number, float64:
		leftNumber, leftOK := jsonNumberValue(left)
		rightNumber, rightOK := jsonNumberValue(right)
		if !leftOK || !rightOK {
			return false, nil
		}
		return leftNumber.Cmp(rightNumber) == 0, nil
	case []any:
		rightValue, ok := right.([]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false, nil
		}
		return jsonArraysEqual(leftValue, rightValue)
	case map[string]any:
		rightValue, ok := right.(map[string]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false, nil
		}
		return jsonObjectsEqual(leftValue, rightValue)
	default:
		return false, nil
	}
}

func jsonArraysEqual(left, right []any) (bool, error) {
	for index := range left {
		equal, err := jsonValuesEqual(left[index], right[index])
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}

func jsonObjectsEqual(left, right map[string]any) (bool, error) {
	for key, leftValue := range left {
		rightValue, found := right[key]
		if !found {
			return false, nil
		}
		equal, err := jsonValuesEqual(leftValue, rightValue)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}

// jsonNumberValue converts one decoded JSON number to an exact rational, so
// ordering and equality never lose precision through binary floating point.
func jsonNumberValue(value any) (*big.Rat, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, ok := new(big.Rat).SetString(number.String())
		return parsed, ok
	case float64:
		parsed := new(big.Rat).SetFloat64(number)
		return parsed, parsed != nil
	default:
		return nil, false
	}
}
