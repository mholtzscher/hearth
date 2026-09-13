package measurementv1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/measurementv1"
)

// Binary64 precision contract. Generated Go binds measurement/v1 State and
// support bounds to float64, so the contract intentionally inherits binary64
// behavior: integers through 2^53 decode exactly, distinct higher-precision
// decimals may normalize to the same value, overflow is rejected, underflow
// may normalize to zero, and equality and range validation operate on
// normalized binary64 values.
func TestStateBindsIntegersExactlyThrough2To53(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "21", "255", "9007199254740992"} {
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

func TestStatePreservesFractionalReadingsExactly(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	// Adapters must not clamp, truncate, or round, so fractional readings
	// round-trip unchanged and normalize without an integral rewrite.
	for _, raw := range []string{"21.5", "-273.15", "0.1", "1234.5"} {
		state, normalized, decodeErr := codecs.State.Decode(json.RawMessage(raw))
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
		var normalizedValue float64
		if unmarshalErr := json.Unmarshal(normalized, &normalizedValue); unmarshalErr != nil {
			t.Fatalf("decode normalized %s: %v", normalized, unmarshalErr)
		}
		if normalizedValue != want {
			t.Fatalf("normalized %s = %v, want %v", raw, normalizedValue, want)
		}
	}
}

func TestStateRoundsWireValuesBeyond2To53(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
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

func TestStateEqualityUsesNormalizedBinary64(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	rounded, _, decodeErr := codecs.State.Decode(json.RawMessage("9007199254740993"))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	exact, _, decodeErr := codecs.State.Decode(json.RawMessage("9007199254740992"))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !measurementv1.EqualState(rounded, exact) {
		t.Fatalf("normalized equal decimals compared unequal: %v, %v", rounded, exact)
	}
}

func TestStateRejectsOverflow(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
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
	codecs, err := measurementv1.Compile()
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

// TestRangeValidationUsesNormalizedBinary64Bounds pins the documented
// consequence of binding bounds to float64: a wire value that is exactly
// outside the Entity range but rounds to the boundary compares as in range.
func TestRangeValidationUsesNormalizedBinary64Bounds(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, _, decodeErr := codecs.Support.Decode(json.RawMessage(
		`{"state":{"measurement_kind":"relative_humidity","unit":"%","minimum":0,"maximum":100},"operations":{}}`,
	))
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	// 100.0000000000000000001 exceeds 100 as an exact rational, but 100 is the
	// nearest binary64 value, so the generated range check accepts it.
	state, _, stateErr := codecs.State.Decode(json.RawMessage("100.0000000000000000001"))
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if validateErr := measurementv1.ValidateState(support, state); validateErr != nil {
		t.Fatalf("normalized boundary State rejected: %v", validateErr)
	}
	// A wire value that normalizes strictly above the bound is still rejected.
	over, _, overErr := codecs.State.Decode(json.RawMessage("100.5"))
	if overErr != nil {
		t.Fatal(overErr)
	}
	if validateErr := measurementv1.ValidateState(support, over); validateErr == nil {
		t.Fatal("out-of-range State unexpectedly accepted")
	}
}

// TestSupportAcceptsEqualDecodedBounds pins that relational bound validation
// runs on decoded binary64 values: two distinct wire spellings that normalize
// to the same in-envelope bound produce an equal, valid support.
func TestSupportAcceptsEqualDecodedBounds(t *testing.T) {
	t.Parallel()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, _, decodeErr := codecs.Support.Decode(json.RawMessage(
		`{"state":{"measurement_kind":"relative_humidity","unit":"%",` +
			`"minimum":99.999999999999999999,"maximum":100},"operations":{}}`,
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
	if validateErr := measurementv1.ValidateSupport(support); validateErr != nil {
		t.Fatalf("ValidateSupport = %v, want nil", validateErr)
	}
}
