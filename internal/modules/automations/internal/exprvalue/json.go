package exprvalue

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DecodeValue rejects duplicate keys and preserves integer tokens as exact int64.
// Decimal and exponent tokens use finite float64 semantics.
func DecodeValue(raw json.RawMessage) (Value, error) {
	return decodeValue(raw, ingressNumber)
}

// NormalizeEvidence validates already-resolved history values for comparison,
// retaining exact int64 tokens and accepting finite floats whose JSON spelling
// is integral outside int64. JSON cannot retain the live int/float distinction.
// This is not expression ingress: never use it to populate an Environment or
// replace Value.Data. Original State/Function JSON still requires DecodeValue.
func NormalizeEvidence(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(raw, evidenceNumber)
	return value.JSON, err
}

func evidenceNumber(token string) (any, error) {
	value, err := ingressNumber(token)
	if err == nil {
		return value, nil
	}
	// A normalized finite float can be outside int64 even though its encoded
	// decimal is integral. Do not expand exponents or accept overflow/underflow.
	return finiteNumber(token)
}

func decodeValue(raw json.RawMessage, number func(string) (any, error)) (Value, error) {
	if len(raw) > valueBytes {
		return Value{}, fault("expression_limit")
	}
	if !utf8.Valid(raw) {
		return Value{}, fault("expression_value_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	data, err := decodeJSON(decoder, 1, &nodes, number)
	if err != nil {
		return Value{}, err
	}
	if _, trailingErr := decoder.Token(); trailingErr != io.EOF {
		return Value{}, fault("expression_value_invalid")
	}
	return makeValue(data)
}

func decodeJSON(d *json.Decoder, depth int, nodes *int, number func(string) (any, error)) (any, error) {
	*nodes++
	if depth > valueDepth || *nodes > valueNodes {
		return nil, fault("expression_limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, fault("expression_value_invalid")
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '[':
			return decodeArray(d, depth, nodes, number)
		case '{':
			return decodeObject(d, depth, nodes, number)
		default:
			return nil, fault("expression_value_invalid")
		}
	case json.Number:
		return number(string(t))
	case nil, bool, string:
		return t, nil
	default:
		return nil, fault("expression_value_invalid")
	}
}

func decodeArray(d *json.Decoder, depth int, nodes *int, number func(string) (any, error)) (any, error) {
	array := []any{}
	for d.More() {
		if len(array) == containerSize {
			return nil, fault("expression_limit")
		}
		item, err := decodeJSON(d, depth+1, nodes, number)
		if err != nil {
			return nil, err
		}
		array = append(array, item)
	}
	if end, err := d.Token(); err != nil || end != json.Delim(']') {
		return nil, fault("expression_value_invalid")
	}
	return array, nil
}

func decodeObject(d *json.Decoder, depth int, nodes *int, number func(string) (any, error)) (any, error) {
	object := map[string]any{}
	for d.More() {
		if len(object) == containerSize {
			return nil, fault("expression_limit")
		}
		key, err := d.Token()
		if err != nil {
			return nil, fault("expression_value_invalid")
		}
		name, ok := key.(string)
		if !ok {
			return nil, fault("expression_value_invalid")
		}
		if _, exists := object[name]; exists {
			return nil, fault("expression_value_invalid")
		}
		item, err := decodeJSON(d, depth+1, nodes, number)
		if err != nil {
			return nil, err
		}
		object[name] = item
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, fault("expression_value_invalid")
	}
	return object, nil
}

func ingressNumber(token string) (any, error) {
	if strings.ContainsAny(token, ".eE") {
		return finiteNumber(token)
	}
	number, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return nil, fault("expression_value_invalid")
	}
	return number, nil
}

func finiteNumber(token string) (any, error) {
	number, err := strconv.ParseFloat(token, 64)
	// ParseFloat silently rounds sufficiently small nonzero inputs to zero.
	mantissa, _, _ := strings.Cut(strings.ToLower(token), "e")
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) ||
		number == 0 && strings.Trim(mantissa, "-0.") != "" {
		return nil, fault("expression_value_invalid")
	}
	return number, nil
}

type valueBudget struct{ nodes, stringBytes int }

func (b *valueBudget) addString(value string) error {
	if len(value) > valueBytes-b.stringBytes {
		return fault("expression_limit")
	}
	if !utf8.ValidString(value) {
		return fault("expression_value_invalid")
	}
	b.stringBytes += len(value)
	return nil
}

// copy bounds structure and aggregate raw strings before serialization. JSON
// escaping may allocate more temporarily; makeValue checks the actual result.
// Only the closed JSON domain is accepted, never reflection or host methods.
func (b *valueBudget) copy(data any, depth int) (any, error) {
	b.nodes++
	if depth > valueDepth || b.nodes > valueNodes {
		return nil, fault("expression_limit")
	}
	switch v := data.(type) {
	case nil, bool, int64:
		return v, nil
	case string:
		return v, b.addString(v)
	case int:
		return int64(v), nil
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, fault("expression_value_invalid")
		}
		return v, nil
	case []any:
		return b.copyArray(v, depth)
	case []int:
		return copyNativeArray(b, v, depth)
	case []string:
		return copyNativeArray(b, v, depth)
	case []float64:
		return copyNativeArray(b, v, depth)
	case map[string]any:
		return b.copyObject(v, depth)
	default:
		return nil, fault("expression_value_invalid")
	}
}

// Native ranges and builtins may return typed JSON arrays. Check the bound
// before adapting their representation; elements still pass the same copier.
func copyNativeArray[T int | string | float64](budget *valueBudget, input []T, depth int) (any, error) {
	if len(input) > containerSize {
		return nil, fault("expression_limit")
	}
	items := make([]any, len(input))
	for index, item := range input {
		items[index] = item
	}
	return budget.copyArray(items, depth)
}

func (b *valueBudget) copyArray(v []any, depth int) (any, error) {
	if len(v) > containerSize {
		return nil, fault("expression_limit")
	}
	result := make([]any, len(v))
	for i, item := range v {
		owned, err := b.copy(item, depth+1)
		if err != nil {
			return nil, err
		}
		result[i] = owned
	}
	return result, nil
}

func (b *valueBudget) copyObject(v map[string]any, depth int) (any, error) {
	if len(v) > containerSize {
		return nil, fault("expression_limit")
	}
	result := make(map[string]any, len(v))
	keys := objectKeys(v)
	for _, key := range keys {
		if err := b.addString(key); err != nil {
			return nil, err
		}
		owned, err := b.copy(v[key], depth+1)
		if err != nil {
			return nil, err
		}
		result[key] = owned
	}
	return result, nil
}

func objectKeys(v map[string]any) []string {
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func makeValue(data any) (Value, error) {
	state := valueBudget{}
	owned, err := state.copy(data, 1)
	if err != nil {
		return Value{}, err
	}
	encoded, err := json.Marshal(owned)
	if err != nil {
		return Value{}, fault("expression_value_invalid")
	}
	if len(encoded) > valueBytes {
		return Value{}, fault("expression_limit")
	}
	return Value{Data: owned, JSON: encoded}, nil
}
