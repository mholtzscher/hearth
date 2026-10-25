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

// DecodeValue rejects duplicate keys and converts exact decimal integers before
// any float conversion. The token and tree budgets precede numeric expansion.
func DecodeValue(raw json.RawMessage) (Value, error) {
	if len(raw) > valueBytes {
		return Value{}, fault("expression_limit")
	}
	if !utf8.Valid(raw) {
		return Value{}, fault("expression_value_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	data, err := decodeJSON(decoder, 1, &nodes)
	if err != nil {
		return Value{}, err
	}
	if _, trailingErr := decoder.Token(); trailingErr != io.EOF {
		return Value{}, fault("expression_value_invalid")
	}
	return makeValue(data)
}

func decodeJSON(d *json.Decoder, depth int, nodes *int) (any, error) {
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
			return decodeArray(d, depth, nodes)
		case '{':
			return decodeObject(d, depth, nodes)
		default:
			return nil, fault("expression_value_invalid")
		}
	case json.Number:
		return decimalNumber(string(t))
	case nil, bool, string:
		return t, nil
	default:
		return nil, fault("expression_value_invalid")
	}
}

func decodeArray(d *json.Decoder, depth int, nodes *int) (any, error) {
	array := []any{}
	for d.More() {
		if len(array) == containerSize {
			return nil, fault("expression_limit")
		}
		item, err := decodeJSON(d, depth+1, nodes)
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

func decodeObject(d *json.Decoder, depth int, nodes *int) (any, error) {
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
		item, err := decodeJSON(d, depth+1, nodes)
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

func decimalNumber(token string) (any, error) {
	mantissa, exponent, hasExponent := strings.Cut(token, "e")
	if !hasExponent {
		mantissa, exponent, hasExponent = strings.Cut(token, "E")
	}
	exp := int64(0)
	if hasExponent {
		var err error
		exp, err = strconv.ParseInt(exponent, 10, 32)
		if err != nil || exp > decimalExponentBound || exp < -decimalExponentBound {
			// Zero is exact even with a huge exponent; never expand a power of ten.
			if strings.Trim(mantissa, "-0.") == "" {
				return int64(0), nil
			}
			return nil, fault("expression_value_invalid")
		}
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return int64(0), nil
	}
	scale := exp - int64(len(fraction))
	trailing := len(digits) - len(strings.TrimRight(digits, "0"))
	if scale >= 0 || int64(trailing) >= -scale {
		return integralDecimal(digits, scale, negative)
	}
	number, err := strconv.ParseFloat(token, 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) || number == 0 {
		return nil, fault("expression_value_invalid")
	}
	return number, nil
}

func integralDecimal(digits string, scale int64, negative bool) (any, error) {
	if int64(len(digits))+scale > int64DecimalDigits {
		return nil, fault("expression_value_invalid")
	}
	if scale < 0 {
		digits = digits[:int64(len(digits))+scale]
	} else {
		digits += strings.Repeat("0", int(scale))
	}
	if negative {
		digits = "-" + digits
	}
	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return nil, fault("expression_value_invalid")
	}
	return number, nil
}

type valueBudget struct{ nodes, bytes, maxBytes int }

func (b *valueBudget) add(n int) error {
	b.bytes += n
	if b.bytes > b.maxBytes {
		return fault("expression_limit")
	}
	return nil
}

// copy accounts encoded bytes before allocating a container or serializing it.
// Only the closed JSON domain is accepted, never reflection or host methods.
func (b *valueBudget) copy(data any, depth int) (any, error) {
	b.nodes++
	if depth > valueDepth || b.nodes > valueNodes {
		return nil, fault("expression_limit")
	}
	switch v := data.(type) {
	case nil:
		return nil, b.add(len(nullToken))
	case bool:
		if v {
			return v, b.add(len("true"))
		}
		return v, b.add(len("false"))
	case string:
		size, err := quotedSize(v)
		if err != nil {
			return nil, err
		}
		return v, b.add(size)
	case int:
		return b.copyNumber(int64(v))
	case int64:
		return b.copyNumber(v)
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, fault("expression_value_invalid")
		}
		encoded, _ := json.Marshal(v)
		return v, b.add(len(encoded))
	case []any:
		return b.copyArray(v, depth)
	case map[string]any:
		return b.copyObject(v, depth)
	default:
		return nil, fault("expression_value_invalid")
	}
}

func (b *valueBudget) copyArray(v []any, depth int) (any, error) {
	if len(v) > containerSize {
		return nil, fault("expression_limit")
	}
	if err := b.add(len("[]") + max(0, len(v)-1)); err != nil {
		return nil, err
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
	if err := b.add(len("{}") + max(0, len(v)-1) + len(v)); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(v))
	keys := objectKeys(v)
	for _, key := range keys {
		size, err := quotedSize(key)
		if err != nil {
			return nil, err
		}
		err = b.add(size)
		if err != nil {
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

func (b *valueBudget) copyNumber(v int64) (any, error) {
	return v, b.add(len(strconv.FormatInt(v, 10)))
}

// quotedSize mirrors encoding/json's UTF-8 string escaping without allocation.
// It bounds the predicted representation before concatenation or serialization.
func quotedSize(s string) (int, error) {
	if !utf8.ValidString(s) {
		return 0, fault("expression_value_invalid")
	}
	size := quoteBytes
	for _, r := range s {
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case '<', '>', '&', '\u2028', '\u2029':
			size += 6
		default:
			if r < firstPrintable {
				size += 6
			} else {
				size += utf8.RuneLen(r)
			}
		}
		if size > valueBytes {
			return 0, fault("expression_limit")
		}
	}
	return size, nil
}

func makeValue(data any) (Value, error) {
	state := valueBudget{maxBytes: valueBytes}
	owned, err := state.copy(data, 1)
	if err != nil {
		return Value{}, err
	}
	encoded, err := json.Marshal(owned)
	if err != nil {
		return Value{}, fault("expression_value_invalid")
	}
	return Value{Data: owned, JSON: encoded}, nil
}
