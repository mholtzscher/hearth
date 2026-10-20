package entitytypes_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
)

type codecFixture struct {
	Value bool `json:"value"`
}

func TestJSONCodecNormalizesAndValidates(t *testing.T) {
	t.Parallel()
	codec, err := entitytypes.CompileJSONCodec[codecFixture](
		"urn:test:codec",
		json.RawMessage(
			`{"type":"object","required":["value"],"properties":{"value":{"type":"boolean"}},"additionalProperties":false}`,
		),
		func(value codecFixture) error {
			if !value.Value {
				return errors.New("value must be true")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	value, normalized, err := codec.Decode(json.RawMessage(" \n { \"value\" : true } \t"))
	if err != nil {
		t.Fatal(err)
	}
	if !value.Value || string(normalized) != `{"value":true}` {
		t.Fatalf("decoded = %#v, normalized = %s", value, normalized)
	}
	encoded, err := codec.Encode(codecFixture{Value: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"value":true}` {
		t.Fatalf("encoded = %s", encoded)
	}

	for _, test := range []struct {
		name string
		raw  string
	}{
		{"missing", ""},
		{"malformed", "{"},
		{"trailing", `{"value":true} false`},
		{"schema", `{"value":true,"extra":false}`},
		{"invariant", `{"value":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, decodeErr := codec.Decode(json.RawMessage(test.raw)); decodeErr == nil {
				t.Fatal("value unexpectedly accepted")
			}
		})
	}
}

func TestJSONCodecDecodesEquivalentIntegerRepresentations(t *testing.T) {
	t.Parallel()
	scalar, err := entitytypes.CompileJSONCodec[int64](
		"urn:test:integer",
		json.RawMessage(`{"type":"integer"}`),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"75", "75.0", "75e0"} {
		value, normalized, decodeErr := scalar.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			t.Errorf("Decode(%s): %v", raw, decodeErr)
			continue
		}
		if value != 75 || string(normalized) != "75" {
			t.Errorf("Decode(%s) = %d, %s", raw, value, normalized)
		}
	}

	type integerFixture struct {
		Value int64 `json:"value"`
	}
	object, err := entitytypes.CompileJSONCodec[integerFixture](
		"urn:test:integer-object",
		json.RawMessage(
			`{"type":"object","required":["value"],"properties":{"value":{"type":"integer"}},"additionalProperties":false}`,
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	value, normalized, err := object.Decode(json.RawMessage(`{"value":75e0}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Value != 75 || string(normalized) != `{"value":75}` {
		t.Fatalf("object Decode = %#v, %s", value, normalized)
	}
}

func TestJSONCodecRejectsBindingDrift(t *testing.T) {
	t.Parallel()
	type driftedBinding struct{}
	codec, err := entitytypes.CompileJSONCodec[driftedBinding](
		"urn:test:drift",
		json.RawMessage(
			`{"type":"object","required":["value"],"properties":{"value":{"type":"boolean"}},"additionalProperties":false}`,
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = codec.Decode(json.RawMessage(`{"value":true}`))
	if err == nil || !strings.Contains(err.Error(), "does not preserve its schema") {
		t.Fatalf("binding drift error = %v", err)
	}
}

type nestedIntegers struct {
	Values []int64 `json:"values"`
}

type integerDocument struct {
	Count  int64          `json:"count"`
	Nested nestedIntegers `json:"nested"`
}

func newIntegerDocumentCodec(t *testing.T) *entitytypes.JSONCodec[integerDocument] {
	t.Helper()
	codec, err := entitytypes.CompileJSONCodec[integerDocument](
		"urn:test:nested-integers",
		json.RawMessage(`{"type":"object","required":["count","nested"],"properties":{
			"count":{"type":"integer"},
			"nested":{"type":"object","required":["values"],"properties":{
				"values":{"type":"array","items":{"type":"integer"}}
			},"additionalProperties":false}
		},"additionalProperties":false}`),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func TestJSONCodecNormalizesNestedIntegers(t *testing.T) {
	t.Parallel()
	codec := newIntegerDocumentCodec(t)
	for _, test := range []struct {
		name       string
		raw        string
		want       integerDocument
		normalized string
	}{
		{
			"zero and empty array", ` { "count": 0, "nested": { "values": [] } } `,
			integerDocument{Count: 0, Nested: nestedIntegers{Values: []int64{}}},
			`{"count":0,"nested":{"values":[]}}`,
		},
		{
			"decimal and exponent integers", `{"count":75e0,"nested":{"values":[0.0,-2e0,3E+0]}}`,
			integerDocument{Count: 75, Nested: nestedIntegers{Values: []int64{0, -2, 3}}},
			`{"count":75,"nested":{"values":[0,-2,3]}}`,
		},
		{
			"int64 boundaries", `{"count":9223372036854775807,"nested":{"values":[-9223372036854775808]}}`,
			integerDocument{Count: 9223372036854775807, Nested: nestedIntegers{Values: []int64{-9223372036854775808}}},
			`{"count":9223372036854775807,"nested":{"values":[-9223372036854775808]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, normalized, decodeErr := codec.Decode(json.RawMessage(test.raw))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if !reflect.DeepEqual(value, test.want) || string(normalized) != test.normalized {
				t.Fatalf("Decode = %#v, %s; want %#v, %s", value, normalized, test.want, test.normalized)
			}
			again, renormalized, decodeErr := codec.Decode(normalized)
			if decodeErr != nil || !reflect.DeepEqual(again, test.want) || string(renormalized) != test.normalized {
				t.Fatalf(
					"second Decode = %#v, %s, %v; want %#v, %s",
					again,
					renormalized,
					decodeErr,
					test.want,
					test.normalized,
				)
			}
			encoded, encodeErr := codec.Encode(test.want)
			if encodeErr != nil || string(encoded) != test.normalized {
				t.Fatalf("Encode = %s, %v; want %s", encoded, encodeErr, test.normalized)
			}
		})
	}
}

func TestJSONCodecRejectsInvalidIntegerValues(t *testing.T) {
	t.Parallel()
	codec := newIntegerDocumentCodec(t)
	for name, raw := range map[string]string{
		"null":            `null`,
		"missing nested":  `{"count":1}`,
		"string count":    `{"count":"1","nested":{"values":[]}}`,
		"overflow":        `{"count":9223372036854775808,"nested":{"values":[]}}`,
		"underflow":       `{"count":-9223372036854775809,"nested":{"values":[]}}`,
		"fraction":        `{"count":1.5,"nested":{"values":[]}}`,
		"nested string":   `{"count":1,"nested":{"values":[1,"two"]}}`,
		"nested overflow": `{"count":1,"nested":{"values":[9223372036854775808]}}`,
		"trailing input":  `{"count":1,"nested":{"values":[]}} false`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, decodeErr := codec.Decode(json.RawMessage(raw)); decodeErr == nil {
				t.Fatalf("invalid integer document accepted: %s", raw)
			}
		})
	}
}

func TestCompileJSONCodecRejectsInvalidSchemas(t *testing.T) {
	t.Parallel()
	if _, err := entitytypes.CompileJSONCodec[bool]("", json.RawMessage(`{"type":"boolean"}`), nil); err == nil {
		t.Fatal("empty schema ID unexpectedly accepted")
	}
	if _, err := entitytypes.CompileJSONCodec[bool]("urn:test:bad", json.RawMessage(`{`), nil); err == nil {
		t.Fatal("malformed schema unexpectedly accepted")
	}
}
