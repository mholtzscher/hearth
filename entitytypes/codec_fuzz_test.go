package entitytypes_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
)

const fuzzJSONCodecMaxInputSize = 64 << 10

type fuzzJSONCodecFixture struct {
	Name   string                     `json:"name"`
	Count  int64                      `json:"count"`
	Nested fuzzJSONCodecNestedFixture `json:"nested"`
}

type fuzzJSONCodecNestedFixture struct {
	Enabled bool    `json:"enabled"`
	Values  []int64 `json:"values"`
}

// FuzzJSONCodecDecodeStability protects schema-backed normalization and typed binding.
// Any accepted input must have one stable normalized representation shared by Decode and Encode.
func FuzzJSONCodecDecodeStability(f *testing.F) {
	codec, err := entitytypes.CompileJSONCodec[fuzzJSONCodecFixture](
		"urn:test:fuzz-json-codec",
		json.RawMessage(`{
			"type":"object",
			"required":["name","count","nested"],
			"properties":{
				"name":{"type":"string"},
				"count":{"type":"integer"},
				"nested":{
					"type":"object",
					"required":["enabled","values"],
					"properties":{
						"enabled":{"type":"boolean"},
						"values":{"type":"array","items":{"type":"integer"}}
					},
					"additionalProperties":false
				}
			},
			"additionalProperties":false
		}`),
		nil,
	)
	if err != nil {
		f.Fatal(err)
	}

	seeds := [][]byte{
		[]byte(`{"name":"lamp","count":0,"nested":{"enabled":true,"values":[]}}`),
		[]byte(
			" \n\t{ \"name\" : \"lamp\", \"count\" : 1, " +
				"\"nested\" : { \"enabled\" : false, \"values\" : [ 2, 3 ] } } \r\n",
		),
		[]byte(`{"name":"numeric","count":75e0,"nested":{"enabled":true,"values":[0.0,-2e0,3E+0]}}`),
		[]byte(`{"name":"nested","count":-1,"nested":{"enabled":false,"values":[1,2,3,4]}}`),
		[]byte(
			`{"name":"large","count":9223372036854775807,` +
				`"nested":{"enabled":true,"values":[-9223372036854775808]}}`,
		),
		[]byte(``),
		[]byte(`{`),
		[]byte(`null`),
		[]byte(`{"name":"missing","count":1}`),
		[]byte(`{"name":"wrong-type","count":"1","nested":{"enabled":true,"values":[]}}`),
		[]byte(`{"name":"extra","count":1,"nested":{"enabled":true,"values":[]},"extra":true}`),
		[]byte(`{"name":"trailing","count":1,"nested":{"enabled":true,"values":[]}} false`),
		[]byte(`{"name":"overflow","count":9223372036854775808,"nested":{"enabled":true,"values":[]}}`),
		[]byte(`{"name":"fraction","count":1.5,"nested":{"enabled":true,"values":[]}}`),
		[]byte(`{"name":"bad-nested","count":1,"nested":{"enabled":true,"values":[1,"two"]}}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > fuzzJSONCodecMaxInputSize {
			return
		}

		value, normalized, decodeErr := codec.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			return
		}

		redecoded, renormalized, stabilityErr := codec.Decode(normalized)
		if stabilityErr != nil {
			t.Fatalf(
				"Decode accepted %q but rejected its normalized output %q: %v",
				raw,
				normalized,
				stabilityErr,
			)
		}
		if !reflect.DeepEqual(redecoded, value) {
			t.Fatalf("typed value changed after normalized Decode: first %#v, second %#v", value, redecoded)
		}
		if !bytes.Equal(renormalized, normalized) {
			t.Fatalf("normalization is not idempotent: first %q, second %q", normalized, renormalized)
		}

		encoded, encodeErr := codec.Encode(value)
		if encodeErr != nil {
			t.Fatalf("Decode accepted %q as %#v but Encode rejected the value: %v", raw, value, encodeErr)
		}
		if !bytes.Equal(encoded, normalized) {
			t.Fatalf("Encode and Decode normalization differ for %#v: Decode %q, Encode %q", value, normalized, encoded)
		}
	})
}
