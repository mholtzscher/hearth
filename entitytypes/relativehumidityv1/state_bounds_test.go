package relativehumidityv1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/relativehumidityv1"
)

// The canonical envelope and unit are hand-written oracles rather than values
// read from the schemas, so a schema or generator regression cannot restate its
// own expectation. Relative humidity is a finite JSON number of percent from 0
// through 100, with its unit declared by State.
const (
	canonicalMinimum = 0.0
	canonicalMaximum = 100.0
)

func TestStateAcceptsFiniteFractionalPercentWithinEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := relativehumidityv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "0.001", "45", "45.5", "99.999", "100"} {
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
	codecs, err := relativehumidityv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"-0.001", "100.001", "1000", "-1"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection rather than clamping", raw)
		}
	}
}

func TestStateRejectsNonNumberAndNonFiniteJSON(t *testing.T) {
	t.Parallel()
	codecs, err := relativehumidityv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`"45"`, "true", "null", "1e400", "-1e400"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection", raw)
		}
	}
}

// Fixed units belong to State, not Entity support.
func TestSupportRejectsDuplicatedFixedUnit(t *testing.T) {
	t.Parallel()
	codecs, err := relativehumidityv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, normalized, decodeErr := codecs.Support.Decode(
		json.RawMessage(`{"state":{},"operations":{}}`),
	)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if validateErr := relativehumidityv1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
	if string(normalized) != `{"state":{},"operations":{}}` {
		t.Fatalf("normalized support = %s, want the canonical unit shape", normalized)
	}
	for _, raw := range []string{
		`{"state":{"unit":"%"},"operations":{}}`,
		`{"state":{"unit":"hPa"},"operations":{}}`,
		`{"state":{"unit":"%","minimum":0},"operations":{}}`,
		`{"state":{"unit":"%"},"operations":{"set":{}}}`,
		`{"state":{"unit":"%"}}`,
	} {
		if _, _, unsupportedErr := codecs.Support.Decode(json.RawMessage(raw)); unsupportedErr == nil {
			t.Errorf("support %s unexpectedly decoded, want canonical unit enforcement", raw)
		}
	}
}
