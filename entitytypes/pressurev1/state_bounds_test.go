package pressurev1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/pressurev1"
)

// The canonical envelope and unit are hand-written oracles rather than values
// read from the schemas, so a schema or generator regression cannot restate its
// own expectation. Pressure is a finite JSON number of hectopascals from 0
// through 2000, and support declares the one canonical unit.
const (
	canonicalMinimum = 0.0
	canonicalMaximum = 2000.0
	canonicalUnit    = "hPa"
)

func TestStateAcceptsFiniteFractionalHectopascalsWithinEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := pressurev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "0.001", "1013", "1013.25", "1999.999", "2000"} {
		state, normalized, decodeErr := codecs.State.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			t.Errorf("decode %s: %v", raw, decodeErr)
			continue
		}
		if float64(state) < canonicalMinimum || float64(state) > canonicalMaximum {
			t.Errorf("decode %s = %v, outside canonical envelope", raw, float64(state))
		}
		if string(normalized) != raw {
			t.Errorf("normalized %s = %s, want exact fractional preservation", raw, normalized)
		}
	}
}

func TestStateRejectsValuesOutsideEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := pressurev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"-0.001", "2000.001", "20000", "-1"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection rather than clamping", raw)
		}
	}
}

func TestStateRejectsNonNumberAndNonFiniteJSON(t *testing.T) {
	t.Parallel()
	codecs, err := pressurev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`"1013.25"`, "true", "null", "1e400", "-1e400"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection", raw)
		}
	}
}

// TestSupportRequiresCanonicalHectopascalUnit protects the canonical pressure
// unit in Entity support: pressure State is bare hectopascals, so support must
// declare exactly the one unit the contract fixes and reject every other shape.
func TestSupportRequiresCanonicalHectopascalUnit(t *testing.T) {
	t.Parallel()
	codecs, err := pressurev1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, normalized, decodeErr := codecs.Support.Decode(
		json.RawMessage(`{"state":{"unit":"hPa"},"operations":{}}`),
	)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if support.State.Unit != canonicalUnit {
		t.Fatalf("support unit = %q, want %q", support.State.Unit, canonicalUnit)
	}
	if validateErr := pressurev1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
	if string(normalized) != `{"state":{"unit":"hPa"},"operations":{}}` {
		t.Fatalf("normalized support = %s, want the canonical unit shape", normalized)
	}
	for _, raw := range []string{
		`{"state":{},"operations":{}}`,
		`{"state":{"unit":"mCel"},"operations":{}}`,
		`{"state":{"unit":"hPa","minimum":0},"operations":{}}`,
		`{"state":{"unit":"hPa"},"operations":{"set":{}}}`,
		`{"state":{"unit":"hPa"}}`,
	} {
		if _, _, unsupportedErr := codecs.Support.Decode(json.RawMessage(raw)); unsupportedErr == nil {
			t.Errorf("support %s unexpectedly decoded, want canonical unit enforcement", raw)
		}
	}
}
