package entitytypes_test

import (
	"encoding/json"
	"errors"
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
			if _, _, err := codec.Decode(json.RawMessage(test.raw)); err == nil {
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
		value, normalized, err := scalar.Decode(json.RawMessage(raw))
		if err != nil {
			t.Errorf("Decode(%s): %v", raw, err)
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

func TestCompileJSONCodecRejectsInvalidSchemas(t *testing.T) {
	t.Parallel()
	if _, err := entitytypes.CompileJSONCodec[bool]("", json.RawMessage(`{"type":"boolean"}`), nil); err == nil {
		t.Fatal("empty schema ID unexpectedly accepted")
	}
	if _, err := entitytypes.CompileJSONCodec[bool]("urn:test:bad", json.RawMessage(`{`), nil); err == nil {
		t.Fatal("malformed schema unexpectedly accepted")
	}
}
