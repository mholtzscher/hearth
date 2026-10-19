package functionschema_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

func TestNumericProfileChecksRangeWithoutRoundingLiterals(t *testing.T) {
	t.Parallel()
	validator, err := functionschema.Compile(
		json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","minimum":0}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	for literal, accepted := range map[string]bool{
		"9007199254740993":       true,
		"1.0":                    true,
		"1.7976931348623157e308": true,
		"1e-10000":               true,
		"0e10000":                true,
		"0e+00010000":            true,
		"0e-10000":               true,
		"1.7976931348623159e308": false,
		"1e400":                  false,
		"-1e400":                 false,
		"1e-10001":               false,
		"0e10001":                false,
		"0e-10001":               false,
		"1e999999999999":         false,
		"1e-999999999999":        false,
	} {
		t.Run(literal, func(t *testing.T) {
			t.Parallel()
			value := json.RawMessage(`{"n":` + literal + `}`)
			for _, normalize := range []func(json.RawMessage) (json.RawMessage, error){functionschema.NormalizeObject, validator.Normalize} {
				normalized, normalizeErr := normalize(value)
				assertNumericValue(t, value, normalized, normalizeErr, accepted)
			}
			assertNumericAssertion(t, literal, value, accepted)
		})
	}
}

func assertNumericValue(t *testing.T, value, normalized json.RawMessage, err error, accepted bool) {
	t.Helper()
	if !accepted {
		if !errors.Is(err, functionschema.ErrInvalidValue) {
			t.Fatalf("out-of-profile value accepted: %s, %v", normalized, err)
		}
		return
	}
	if err != nil || string(normalized) != string(value) {
		t.Fatalf("literal changed or rejected: %s, %v", normalized, err)
	}
}

func assertNumericAssertion(t *testing.T, literal string, value json.RawMessage, accepted bool) {
	t.Helper()
	// A numeric assertion must reject at compilation, never disappear.
	document := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","minimum":` + literal + `}}}`)
	schema, err := functionschema.Compile(document)
	if !accepted {
		if !errors.Is(err, functionschema.ErrInvalidSchema) {
			t.Fatalf("out-of-profile assertion accepted: %v", err)
		}
		return
	}
	if err != nil || !strings.Contains(string(schema.Document()), literal) {
		t.Fatalf("schema literal changed or rejected: %v", err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatalf("value did not meet its exact minimum: %v", err)
	}
}

func TestLargeCardinalityConstraintsKeepTheirMeaning(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{"minItems", "maxItems", "minLength", "maxLength"} {
		for _, literal := range []string{"18446744073709551616", "1e308"} {
			t.Run(keyword+"/"+literal, func(t *testing.T) {
				t.Parallel()
				assertLargeCardinality(t, keyword, literal)
			})
		}
	}
}

func assertLargeCardinality(t *testing.T, keyword, literal string) {
	t.Helper()
	valueType, value := "array", `[1]`
	if strings.HasSuffix(keyword, "Length") {
		valueType, value = "string", `"x"`
	}
	schema, err := functionschema.Compile(
		json.RawMessage(
			`{"type":"object","properties":{"n":{"type":"` + valueType + `","` + keyword + `":` + literal + `}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema.Document()), literal) {
		t.Fatalf("advertised assertion changed: %s", schema.Document())
	}
	err = schema.Validate(json.RawMessage(`{"n":` + value + `}`))
	if strings.HasPrefix(keyword, "min") {
		if !errors.Is(err, functionschema.ErrInvalidValue) {
			t.Fatalf("huge minimum accepted a single item: %v", err)
		}
	} else if err != nil {
		t.Fatalf("huge maximum rejected a single item: %v", err)
	}
}
