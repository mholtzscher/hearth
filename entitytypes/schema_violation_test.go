package entitytypes_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
)

type flatFixture struct {
	Level int `json:"level"`
}

// TestSchemaViolationDiagnosticsFollowOnlyDeclaredNames keeps rejected input
// out of published diagnostics: paths name declared properties, and Allowed
// lists the schema's names.
func TestSchemaViolationDiagnosticsFollowOnlyDeclaredNames(t *testing.T) {
	t.Parallel()
	codec, err := entitytypes.CompileJSONCodec[flatFixture]("urn:test:flat", json.RawMessage(`{
		"type":"object","additionalProperties":false,"required":["level"],
		"properties":{"level":{"type":"integer","maximum":3}}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, value string
		want        []entitytypes.SchemaDiagnostic
	}{
		{"unknown key", `{"secret_key":{"x":1}}`, []entitytypes.SchemaDiagnostic{
			{Code: entitytypes.DiagnosticUnknownProperty, Path: "", Allowed: []string{"level"}},
			{Code: entitytypes.DiagnosticMissingProperty, Path: "/level"},
		}},
		{"constraint", `{"level":9}`, []entitytypes.SchemaDiagnostic{
			{Code: entitytypes.DiagnosticConstraint, Path: "/level"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, decodeErr := codec.Decode(json.RawMessage(test.value))
			violation, ok := errors.AsType[*entitytypes.SchemaViolationError](decodeErr)
			if !ok {
				t.Fatalf("Decode() error = %v, want SchemaViolationError", decodeErr)
			}
			if !reflect.DeepEqual(violation.Diagnostics, test.want) {
				t.Fatalf("diagnostics = %+v, want %+v", violation.Diagnostics, test.want)
			}
			published, _ := json.Marshal(violation.Diagnostics)
			if strings.Contains(string(published), "secret_key") || strings.Contains(string(published), "9") {
				t.Fatalf("diagnostics echo input: %s", published)
			}
		})
	}
}
