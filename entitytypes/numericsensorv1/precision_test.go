package numericsensorv1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
)

// Binary64 precision contract: integers through 2^53 decode exactly;
// overflow fails codec decode; underflow may round to zero; wire values
// that differ beyond binary64 precision compare equal after rounding.
func TestStateBindsIntegersExactlyThrough2To53(t *testing.T) {
	t.Parallel()
	codecs, err := numericsensorv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "18", "255", "9007199254740992"} {
		state, _, decodeErr := codecs.State.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", raw, decodeErr)
		}
		var want float64
		if unmarshalErr := json.Unmarshal(json.RawMessage(raw), &want); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		if float64(state) != want {
			t.Fatalf("decode %s = %v, want %v", raw, float64(state), want)
		}
	}
}

func TestStateRoundsWireValuesBeyond2To53(t *testing.T) {
	t.Parallel()
	codecs, err := numericsensorv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	state, normalized, decodeErr := codecs.State.Decode(json.RawMessage("9007199254740993"))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if float64(state) != 9007199254740992 {
		t.Fatalf("decode 9007199254740993 = %v, want 9007199254740992", float64(state))
	}
	if string(normalized) != "9007199254740992" {
		t.Fatalf("normalized = %s, want 9007199254740992", normalized)
	}
}

func TestStateRejectsOverflow(t *testing.T) {
	t.Parallel()
	codecs, err := numericsensorv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"1e400", "-1e400"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Fatalf("decode %s unexpectedly succeeded", raw)
		}
	}
}

func TestStateUnderflowsToZero(t *testing.T) {
	t.Parallel()
	codecs, err := numericsensorv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	state, _, decodeErr := codecs.State.Decode(json.RawMessage("1e-9999"))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if float64(state) != 0 {
		t.Fatalf("decode 1e-9999 = %v, want 0", float64(state))
	}
}

func TestSupportAcceptsEqualDecodedBounds(t *testing.T) {
	t.Parallel()
	codecs, err := numericsensorv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	// Wire minimum 2^53+1 and maximum 2^53 differ beyond binary64
	// precision, so both decode to 2^53 and the equal bounds validate.
	support, _, decodeErr := codecs.Support.Decode(json.RawMessage(
		`{"state":{"minimum":9007199254740993,"maximum":9007199254740992,"unit":"lqi"},"operations":{}}`,
	))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if support.State.Minimum != support.State.Maximum {
		t.Fatalf(
			"decoded bounds = %v, %v, want equal",
			support.State.Minimum,
			support.State.Maximum,
		)
	}
	if validateErr := numericsensorv1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
}
