package speedv1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/speedv1"
)

// The canonical envelope is a hand-written oracle rather than a value read from
// state.schema.json, so a schema or generator regression cannot restate its own
// expectation. Speed is a finite JSON number of metres per second from 0
// through 200.
const (
	canonicalMinimum = 0.0
	canonicalMaximum = 200.0
)

func TestStateAcceptsFiniteFractionalMetresPerSecondWithinEnvelope(t *testing.T) {
	t.Parallel()
	codecs, err := speedv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "0.001", "0.44704", "3.58", "199.999", "200"} {
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
	codecs, err := speedv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"-0.001", "200.001", "2000", "-1"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection rather than clamping", raw)
		}
	}
}

func TestStateRejectsNonNumberAndNonFiniteJSON(t *testing.T) {
	t.Parallel()
	codecs, err := speedv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`"3.58"`, "true", "null", "1e400", "-1e400"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("decode %s unexpectedly succeeded, want rejection", raw)
		}
	}
}

func TestSupportIsClosedEmptyOperation(t *testing.T) {
	t.Parallel()
	codecs, err := speedv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, normalized, decodeErr := codecs.Support.Decode(json.RawMessage(`{"state":{},"operations":{}}`))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if validateErr := speedv1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
	if string(normalized) != `{"state":{},"operations":{}}` {
		t.Fatalf("normalized support = %s, want closed empty-operation shape", normalized)
	}
	for _, raw := range []string{
		`{"state":{},"operations":{"set":{}}}`,
		`{"state":{"minimum":0},"operations":{}}`,
		`{"state":{}}`,
	} {
		if _, _, unsupportedErr := codecs.Support.Decode(json.RawMessage(raw)); unsupportedErr == nil {
			t.Errorf("support %s unexpectedly decoded, want closed shape", raw)
		}
	}
}
