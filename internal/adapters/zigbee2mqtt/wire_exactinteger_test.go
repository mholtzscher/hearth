package zigbee2mqtt //nolint:testpackage // Tests exercise package-private exact-integer wire parsing.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// This test protects exact-integer JSON number acceptance and fails on
// rounding, numeric-string coercion, trailing-data acceptance, or int64
// overflow. Expected values are protocol literals, never the parser output.
func TestParseExactIntegerJSONTable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    int64
		valid   bool
	}{
		{payload: `0`, want: 0, valid: true},
		{payload: `154`, want: 154, valid: true},
		{payload: `370.0`, want: 370, valid: true},
		{payload: `-5`, want: -5, valid: true},
		{payload: `1e3`, want: 1000, valid: true},
		{payload: `1E3`, want: 1000, valid: true},
		{payload: `1.5e1`, want: 15, valid: true},
		{payload: `65535`, want: 65535, valid: true},
		{payload: `9223372036854775807`, want: 9223372036854775807, valid: true},
		{payload: `-9223372036854775808`, want: -9223372036854775808, valid: true},
		{payload: ``},
		{payload: `370.5`},
		{payload: `1.5`},
		{payload: `1.5e0`},
		{payload: `2.15e1`},
		{payload: `15e-1`},
		{payload: `"370"`},
		{payload: `null`},
		{payload: `true`},
		{payload: `false`},
		{payload: `{}`},
		{payload: `[]`},
		{payload: `370 trailing`},
		{payload: `154 155`},
		{payload: `{`},
		{payload: `9223372036854775808`},
		{payload: `-9223372036854775809`},
		{payload: `1e10000`},
	} {
		got, err := parseExactIntegerJSON(json.RawMessage(test.payload))
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"parseExactIntegerJSON(%q) = %d, %v; want %d, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects error-category separation and fails if malformed JSON,
// non-numbers, and non-integers collapse into one error. Categories let
// domain wrappers keep their existing property/domain prefixes.
func TestParseExactIntegerJSONErrorCategories(t *testing.T) {
	t.Parallel()
	if _, err := parseExactIntegerJSON(json.RawMessage(`"370"`)); !errors.Is(err, errExactIntegerNotNumber) {
		t.Fatalf("string payload error = %v, want errExactIntegerNotNumber", err)
	}
	if _, err := parseExactIntegerJSON(json.RawMessage(`null`)); !errors.Is(err, errExactIntegerNotNumber) {
		t.Fatalf("null payload error = %v, want errExactIntegerNotNumber", err)
	}
	if _, err := parseExactIntegerJSON(json.RawMessage(`370.5`)); !errors.Is(
		err,
		errExactIntegerNotInteger,
	) {
		t.Fatalf("fraction payload error = %v, want errExactIntegerNotInteger", err)
	}
	if _, err := parseExactIntegerJSON(
		json.RawMessage(`9223372036854775808`),
	); !errors.Is(err, errExactIntegerNotInteger) {
		t.Fatalf("overflow payload error = %v, want errExactIntegerNotInteger", err)
	}
	if _, err := parseExactIntegerJSON(json.RawMessage(`{`)); err == nil ||
		errors.Is(err, errExactIntegerNotNumber) || errors.Is(err, errExactIntegerNotInteger) {
		t.Fatalf("malformed payload error = %v, want plain syntax error", err)
	}
}

// This test protects domain diagnostic prefixes after consolidation and
// fails if linkquality, color temperature, or startup temperature wrappers
// lose their property/domain error text or bool-path parity.
func TestExactIntegerWrappersPreserveDomainDiagnostics(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		message string
		payload string
	}{
		{name: "linkquality syntax", message: "decode linkquality number", payload: `{`},
		{name: "linkquality type", message: "linkquality value must be a JSON number", payload: `"156"`},
		{name: "linkquality integer", message: "linkquality value must be a finite integer", payload: `156.5`},
		{
			name:    "color temperature syntax",
			message: "decode color temperature number",
			payload: `{`,
		},
		{
			name:    "color temperature type",
			message: "color temperature value must be a JSON number",
			payload: `null`,
		},
		{
			name:    "color temperature integer",
			message: "color temperature value must be a finite integer",
			payload: `370.5`,
		},
		{
			name:    "startup syntax",
			message: "decode startup color temperature number",
			payload: `{`,
		},
		{
			name:    "startup type",
			message: "startup color temperature value must be a JSON number",
			payload: `true`,
		},
		{
			name:    "startup integer",
			message: "startup color temperature value must be a finite integer",
			payload: `300.5`,
		},
	} {
		var err error
		switch {
		case strings.HasPrefix(test.name, "linkquality"):
			_, err = normalizeLinkquality(json.RawMessage(test.payload))
		case strings.HasPrefix(test.name, "color temperature"):
			_, err = normalizeColorTemp(json.RawMessage(test.payload))
		default:
			_, err = normalizeStartupColorTemp(json.RawMessage(test.payload))
		}
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s(%s) = %v, want error containing %q", test.name, test.payload, err, test.message)
		}
	}
	// Bool paths keep exact-decimal/scientific acceptance and trailing rejection.
	for _, test := range []struct {
		payload string
		want    int64
		valid   bool
	}{
		{payload: `65535`, want: 65535, valid: true},
		{payload: `65535.0`, want: 65535, valid: true},
		{payload: `6.5535e4`, want: 65535, valid: true},
		{payload: `65535.5`},
		{payload: `"65535"`},
		{payload: `65535 trailing`},
	} {
		got, ok := exactPresetValue(json.RawMessage(test.payload))
		if ok != test.valid || got != test.want {
			t.Errorf("exactPresetValue(%q) = %d, %t; want %d, %t", test.payload, got, ok, test.want, test.valid)
		}
		raw, rawOK := colorTempBound(json.RawMessage(test.payload), nil)
		if rawOK != test.valid || raw != test.want {
			t.Errorf("colorTempBound(%q) = %d, %t; want %d, %t", test.payload, raw, rawOK, test.want, test.valid)
		}
	}
}
