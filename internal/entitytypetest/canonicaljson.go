package entitytypetest

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// CanonicalJSON decodes raw and returns a string that compares JSON values by
// structure rather than spelling: object keys are sorted, insignificant
// whitespace disappears, and numbers reduce to exact rationals so 1, 1.0, and
// 1e0 agree while integers beyond 2^53 stay distinct. String and number
// spellings stay distinct too. Generated conformance tests compare canonical
// strings instead of raw bytes, so numeric spelling and key order cannot cause
// false mismatches. Malformed raw fails the test.
func CanonicalJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	value, err := decodeCanonicalJSON(raw)
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return canonicalJSONValue(value)
}

// CanonicalValue marshals value and returns its canonical JSON string using the
// same normalization as CanonicalJSON, so a Go value can be compared against
// raw JSON text. Marshaling failures fail the test.
func CanonicalValue(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	return CanonicalJSON(t, raw)
}

// EqualJSON reports whether left and right carry the same JSON value under the
// same normalization as CanonicalJSON: object order and numeric spelling are
// ignored, array order and string/number distinctions are not. Malformed input
// compares unequal instead of failing the test, so a catalog assertion keeps
// reporting a mismatch rather than aborting.
func EqualJSON(t *testing.T, left, right []byte) bool {
	t.Helper()
	leftValue, leftErr := decodeCanonicalJSON(left)
	if leftErr != nil {
		return false
	}
	rightValue, rightErr := decodeCanonicalJSON(right)
	if rightErr != nil {
		return false
	}
	return canonicalJSONValue(leftValue) == canonicalJSONValue(rightValue)
}

// decodeCanonicalJSON decodes raw with [json.Number] preserved so numeric
// spelling reaches canonicalJSONValue unrounded.
func decodeCanonicalJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// canonicalJSONValue renders one decoded JSON value as a comparable string.
// Numbers reduce to exact rationals through [big.Rat], so 1, 1.0, and 1e0 all
// render identically while -0 and 0 do too. A number [big.Rat] cannot parse
// falls back to its spelling.
func canonicalJSONValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		if value {
			return "bool:true"
		}
		return "bool:false"
	case json.Number:
		rational, ok := new(big.Rat).SetString(value.String())
		if !ok {
			return "number:" + value.String()
		}
		return "number:" + rational.RatString()
	case string:
		return "string:" + strconv.Quote(value)
	case []any:
		var canonical strings.Builder
		canonical.WriteString("array:[")
		for index, item := range value {
			if index > 0 {
				canonical.WriteString(",")
			}
			canonical.WriteString(canonicalJSONValue(item))
		}
		canonical.WriteString("]")
		return canonical.String()
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var canonical strings.Builder
		canonical.WriteString("object:{")
		for index, key := range keys {
			if index > 0 {
				canonical.WriteString(",")
			}
			canonical.WriteString(strconv.Quote(key))
			canonical.WriteString("=")
			canonical.WriteString(canonicalJSONValue(value[key]))
		}
		canonical.WriteString("}")
		return canonical.String()
	default:
		return "unsupported"
	}
}
