package mcpapi //nolint:testpackage // Exercises private input preparation and diagnostic decisions.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type exactInput struct {
	Payload JSONObject `json:"payload"`
	Limit   int        `json:"limit,omitempty" default:"50" minimum:"1" maximum:"200"`
}

func TestPreparedInputPreservesRawDocumentsAndDefaults(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[exactInput]()
	if err != nil {
		t.Fatal(err)
	}
	const payload = `{ "integer":9007199254740993, "decimal":1.0000000000000001, "huge":1e10000, "tiny":1e-10000, "nested":[null,{"value":null}] }`
	for _, suffix := range []string{"", `,"limit":3`} {
		value, decodeErr := prepared.Decode(json.RawMessage(`{"payload":` + payload + suffix + `}`))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		wantLimit := 50
		if suffix != "" {
			wantLimit = 3
		}
		if string(value.Payload) != payload || value.Limit != wantLimit {
			t.Fatalf("input = %+v payload=%s", value, value.Payload)
		}
	}
}

func TestPreparedInputRejectsInvalidEnvelopes(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[exactInput]()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"payload":null}`, `{"payload":[]}`, `{"payload":"bad"}`,
		`{"payload":{},"extra":true}`, `{"payload":{},"limit":0}`, `{"payload":{},"limit":201}`,
		`{"payload":{},"limit":null}`, `{"payload":{},"limit":"50"}`,
		`{"payload":{},"payload":{}}`, `{"payload":{"a":1,"a":2}}`, `{"payload":{}} {}`,
		`{"Payload":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := prepared.Decode(json.RawMessage(raw))
			failure, ok := errors.AsType[*ToolError](decodeErr)
			if !ok || failure.Code != CodeInvalidArguments {
				t.Fatalf("input=%s err=%v", raw, decodeErr)
			}
		})
	}
}

func TestPreparedInputTypeMismatchPaths(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[exactInput]()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, path string }{
		{`{"payload":{},"limit":"PRIVATE-value"}`, "/limit"},
		{`{"payload":"PRIVATE-value"}`, "/payload"},
		{`{"payload":null}`, "/payload"},
		{`{"payload":[]}`, "/payload"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := prepared.Decode(json.RawMessage(test.raw))
			failure, ok := errors.AsType[*ToolError](decodeErr)
			if !ok || failure.Code != CodeInvalidArguments {
				t.Fatalf("unexpected failure: %v", decodeErr)
			}
			diagnostics := failure.Details[argumentDiagnosticsField].([]argumentDiagnostic)
			if len(diagnostics) != 1 || diagnostics[0].Path != test.path || diagnostics[0].Code != "type_mismatch" {
				t.Fatalf("diagnostics=%+v, want type_mismatch at %s", diagnostics, test.path)
			}
			encoded, marshalErr := json.Marshal(failure.structuredContent())
			if marshalErr != nil || bytes.Contains(encoded, []byte("PRIVATE")) {
				t.Fatalf("unsafe failure=%s err=%v", encoded, marshalErr)
			}
		})
	}
}

type rejectedField string

func (*rejectedField) UnmarshalJSON([]byte) error {
	return &ToolError{Code: "invalid_owned_field", Message: "invalid owned field"}
}

type decoderPrecedenceInput struct {
	Count int           `json:"count"`
	Owned rejectedField `json:"owned"`
}

func TestModeledDecoderFailurePrecedesSharedSchemaValidation(t *testing.T) {
	t.Parallel()
	input, err := prepareInput[decoderPrecedenceInput]()
	if err != nil {
		t.Fatal(err)
	}
	_, err = input.Decode(json.RawMessage(`{"owned":[],"count":1}`))
	failure, ok := errors.AsType[*ToolError](err)
	if !ok || failure.Code != "invalid_owned_field" {
		t.Fatalf("modeled decoder failure was replaced: %v", err)
	}
}

type nestedDefaultInput struct {
	Optional map[string]any `json:"optional,omitempty"`
	Required map[string]any `json:"required"`
}

func (nestedDefaultInput) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Extensions: map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"required"},
		"properties": map[string]any{
			"optional": map[string]any{"type": []string{"object", "null"}, "properties": map[string]any{
				"n": map[string]any{"type": "integer", "default": json.Number("9007199254740993")},
			}},
			"required": map[string]any{"type": "object", "properties": map[string]any{
				"n": map[string]any{"type": "integer", "default": 7},
			}},
		},
	}}
}

func TestPreparedDefaultsRetainBoundedTraversalAndIsolation(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[nestedDefaultInput]()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		value, decodeErr := prepared.Decode(json.RawMessage(`{"required":{}}`))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if value.Optional["n"] != json.Number("9007199254740993") || len(value.Required) != 0 {
			t.Fatalf("default traversal = %+v", value)
		}
		value.Optional["n"] = "mutated"
	}
	value, err := prepared.Decode(json.RawMessage(`{"required":{},"optional":null}`))
	if err != nil || value.Optional != nil {
		t.Fatalf("explicit null changed: %+v %v", value, err)
	}
}

type invalidDefaultInput struct{}

func (invalidDefaultInput) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "object", Properties: map[string]*huma.Schema{
		"limit": {Type: "integer", Minimum: new(float64(1)), Default: 0},
	}}
}

func TestPreparedInputFailsOnInvalidDefaults(t *testing.T) {
	t.Parallel()
	if _, err := prepareInput[invalidDefaultInput](); err == nil {
		t.Fatal("invalid default did not fail registration preparation")
	}
}

type privateDecoderInput struct{}

func (*privateDecoderInput) UnmarshalJSON([]byte) error {
	return errors.New("PRIVATE-decoder-invariant")
}

func TestPreparedInputDoesNotClassifyUnknownDecoderFailuresAsInput(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[privateDecoderInput]()
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepared.Decode(json.RawMessage(`{}`))
	if _, modeled := errors.AsType[*ToolError](err); err == nil || modeled {
		t.Fatalf("unexpected decoder error lost its identity: %v", err)
	}
}

type boundedInput struct {
	Payload JSONObject `json:"payload"`
}

func (boundedInput) ValidateEnvelope(raw json.RawMessage) error {
	if len(raw) > len(`{"payload":{}}`) {
		return &ToolError{Code: "too_large", Message: "argument envelope exceeds limit"}
	}
	return nil
}

func TestEnvelopeBoundPrecedesParsingForValuesAndPointers(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[boundedInput]()
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := prepareInput[*boundedInput]()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"payload":{}}`, strings.Repeat(" ", 20)} {
		_, valueErr := prepared.Decode(json.RawMessage(raw))
		_, pointerErr := pointer.Decode(json.RawMessage(raw))
		if raw[0] == '{' {
			if valueErr != nil || pointerErr != nil {
				t.Fatalf("valid bound rejected: %v %v", valueErr, pointerErr)
			}
			continue
		}
		for _, failure := range []error{valueErr, pointerErr} {
			modeled, ok := errors.AsType[*ToolError](failure)
			if !ok || modeled.Code != "too_large" {
				t.Fatalf("envelope was not checked first: %v", failure)
			}
		}
	}
}

func TestDecodeIntegerIsExact(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]int64{
		`1`: 1, `1.0`: 1, `1e0`: 1, `-2.00`: -2, `9007199254740993`: 9007199254740993,
		`9223372036854775807`: 9223372036854775807, `-9223372036854775808`: -9223372036854775808,
	} {
		value, err := DecodeInteger(json.RawMessage(raw))
		if err != nil || value != want {
			t.Fatalf("DecodeInteger(%s)=%d,%v want=%d", raw, value, err, want)
		}
	}
	for _, raw := range []string{`null`, `"1"`, `1.0000000000000001`, `9223372036854775808`, `1e10000`, `true`, `{} `} {
		if _, err := DecodeInteger(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSharedSchemaDiagnosticsDoNotEchoUnknownNames(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[exactInput]()
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepared.Decode(json.RawMessage(`{"payload":{},"PRIVATE-property":"PRIVATE-value"}`))
	failure, ok := errors.AsType[*ToolError](err)
	if !ok {
		t.Fatal(err)
	}
	raw, err := json.Marshal(failure.structuredContent())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("PRIVATE")) || !bytes.Contains(raw, []byte(`"unknown_property"`)) {
		t.Fatalf("unsafe or incorrect failure: %s", raw)
	}
}

type diagnosticInput map[string]any

func (diagnosticInput) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Extensions: map[string]any{
		"type": "object", "properties": map[string]any{
			"known": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"},
				"items": map[string]any{"type": "string"}},
			"rows": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				"additionalProperties": false,
			}},
		},
	}}
}

func TestSchemaDiagnosticPathsDistinguishMapKeysAndArrayIndexes(t *testing.T) {
	t.Parallel()
	prepared, err := prepareInput[diagnosticInput]()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, path, code string }{
		{`{"known":{"123456":"PRIVATE-value"}}`, "/known", "type_mismatch"},
		{`{"known":{"PRIVATE-key":"PRIVATE-value"}}`, "/known", "type_mismatch"},
		{`{"rows":[{"PRIVATE-key":"PRIVATE-value"}]}`, "/rows/0", "unknown_property"},
	} {
		_, decodeErr := prepared.Decode(json.RawMessage(test.raw))
		failure, ok := errors.AsType[*ToolError](decodeErr)
		if !ok {
			t.Fatalf("input=%s error=%v", test.raw, decodeErr)
		}
		diagnostics := failure.Details[argumentDiagnosticsField].([]argumentDiagnostic)
		if len(diagnostics) != 1 || diagnostics[0].Path != test.path || diagnostics[0].Code != test.code {
			t.Fatalf("input=%s diagnostics=%+v", test.raw, diagnostics)
		}
		encoded, marshalErr := json.Marshal(failure.structuredContent())
		if marshalErr != nil || bytes.Contains(encoded, []byte("PRIVATE")) ||
			bytes.Contains(encoded, []byte("123456")) {
			t.Fatalf("unsafe failure=%s err=%v", encoded, marshalErr)
		}
	}
}

//nolint:gochecknoglobals // One fixture hook exposes its source only for the ownership test below.
var mutableContractSchema = &huma.Schema{Type: "object", Properties: map[string]*huma.Schema{
	"subject": {Type: "string", Default: "fallback"},
}}

type mutableContract struct {
	Subject string `json:"subject,omitempty"`
}

func (mutableContract) Schema(huma.Registry) *huma.Schema { return mutableContractSchema }

func TestPreparedContractsOwnMutableHookSources(t *testing.T) {
	t.Parallel()
	input, err := prepareInput[mutableContract]()
	if err != nil {
		t.Fatal(err)
	}
	output, err := compileSuccessSchema[mutableContract]()
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(input.advertised)
	property := mutableContractSchema.Properties["subject"]
	property.Type, property.Default = "boolean", true
	t.Cleanup(func() { property.Type, property.Default = "string", "fallback" })
	value, err := input.Decode(json.RawMessage(`{}`))
	if err != nil || value.Subject != "fallback" || !bytes.Equal(before, input.advertised) {
		t.Fatalf("source mutation changed prepared input=%+v err=%v schema=%s", value, err, input.advertised)
	}
	encoded, err := encodeValidatedOutput(value, output)
	if err != nil || string(encoded) != `{"subject":"fallback"}` {
		t.Fatalf("source mutation changed output contract: %s %v", encoded, err)
	}
}

type escapedDefaultInput map[string]any

func (escapedDefaultInput) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Extensions: map[string]any{
		"$id": "https://hearth.test/default-input.json", "type": "object",
		"properties": map[string]any{
			"rate%/~": map[string]any{"type": "integer", "minimum": 1, "default": 17},
		},
	}}
}

func TestDefaultLocationsEscapeURIFragmentsAndJSONPointers(t *testing.T) {
	t.Parallel()
	input, err := prepareInput[escapedDefaultInput]()
	if err != nil {
		t.Fatal(err)
	}
	value, err := input.Decode(json.RawMessage(`{}`))
	if err != nil || value["rate%/~"] != json.Number("17") {
		t.Fatalf("escaped default=%v err=%v", value, err)
	}
}
