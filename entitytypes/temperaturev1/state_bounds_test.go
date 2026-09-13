package temperaturev1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/temperaturev1"
)

// The canonical envelope and unit are hand-written oracles rather than values
// read from the schemas, so a schema or generator regression cannot restate its
// own expectation. Temperature is an integer count of milli-Celsius from
// -273150 through 1000000, and support declares the one canonical unit.
const (
	canonicalMinimum = int64(-273150)
	canonicalMaximum = int64(1000000)
	canonicalUnit    = "mCel"
)

func TestStateAcceptsIntegerMilliCelsiusWithinEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := temperaturev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"-273150", "0", "21500", "1000000"} {
		state, normalized, decodeErr := codecs.State.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			t.Errorf("decode %s: %v", raw, decodeErr)
			continue
		}
		if int64(state) < canonicalMinimum || int64(state) > canonicalMaximum {
			t.Errorf("decode %s = %d, outside canonical envelope", raw, int64(state))
		}
		if string(normalized) != raw {
			t.Errorf("normalized %s = %s, want exact integer preservation", raw, normalized)
		}
	}
}

func TestStateRejectsValuesOutsideEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := temperaturev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"-273151", "1000001", "1000000000", "-1000000000"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection rather than clamping", raw)
		}
	}
}

func TestStateRejectsNonIntegerAndNonFiniteJSON(t *testing.T) {
	t.Parallel()
	codecs, err := temperaturev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"21.5", `"21500"`, "true", "null", "1e400"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection", raw)
		}
	}
}

// TestSupportRequiresCanonicalMilliCelsiusUnit protects the canonical
// temperature unit in Entity support: temperature State is bare milli-Celsius,
// so support must declare exactly the one unit the contract fixes and reject
// every other shape.
func TestSupportRequiresCanonicalMilliCelsiusUnit(t *testing.T) {
	t.Parallel()
	codecs, err := temperaturev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, normalized, decodeErr := codecs.Support.Decode(
		json.RawMessage(`{"state":{"unit":"mCel"},"operations":{}}`),
	)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if support.State.Unit != canonicalUnit {
		t.Fatalf("support unit = %q, want %q", support.State.Unit, canonicalUnit)
	}
	if validateErr := temperaturev1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
	if string(normalized) != `{"state":{"unit":"mCel"},"operations":{}}` {
		t.Fatalf("normalized support = %s, want the canonical unit shape", normalized)
	}
	for _, raw := range []string{
		`{"state":{},"operations":{}}`,
		`{"state":{"unit":"hPa"},"operations":{}}`,
		`{"state":{"unit":"mCel","minimum":0},"operations":{}}`,
		`{"state":{"unit":"mCel"},"operations":{"set":{}}}`,
		`{"state":{"unit":"mCel"}}`,
	} {
		if _, _, unsupportedErr := codecs.Support.Decode(json.RawMessage(raw)); unsupportedErr == nil {
			t.Errorf("support %s unexpectedly decoded, want canonical unit enforcement", raw)
		}
	}
}
