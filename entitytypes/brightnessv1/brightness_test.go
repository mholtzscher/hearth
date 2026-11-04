package brightnessv1_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/brightnessv1"
)

func TestCanonicalPercentageContract(t *testing.T) {
	t.Parallel()
	codecs, err := brightnessv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support, _, err := codecs.Support.Decode(json.RawMessage(`{"state":{},"operations":{"set":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for value := range int64(101) {
		state, _, stateErr := codecs.State.Decode(json.RawMessage(strconv.FormatInt(value, 10)))
		if stateErr != nil || brightnessv1.ValidateState(support, state) != nil {
			t.Fatalf("State %d rejected: %v", value, stateErr)
		}
		parameters, _, parameterErr := codecs.SetParameters.Decode(
			json.RawMessage(`{"value":` + strconv.FormatInt(value, 10) + `}`),
		)
		if parameterErr != nil ||
			brightnessv1.ValidateSetParameters(support, support.Operations.Set, parameters) != nil {
			t.Fatalf("set %d rejected: %v", value, parameterErr)
		}
	}
}

func TestCanonicalPercentageRejectsInvalidValuesAndRemovedSupport(t *testing.T) {
	t.Parallel()
	codecs, err := brightnessv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"-1", "101", "50.5", `"50"`, "null"} {
		if _, _, decodeErr := codecs.State.Decode(json.RawMessage(invalid)); decodeErr == nil {
			t.Fatalf("accepted State %s", invalid)
		}
		if _, _, decodeErr := codecs.SetParameters.Decode(
			json.RawMessage(`{"value":` + invalid + `}`),
		); decodeErr == nil {
			t.Fatalf("accepted set %s", invalid)
		}
	}
	for _, invalid := range []string{
		`{"state":{"maximum":100},"operations":{"set":{}}}`,
		`{"state":{},"operations":{"set":{"step":1}}}`,
	} {
		if _, _, decodeErr := codecs.Support.Decode(json.RawMessage(invalid)); decodeErr == nil {
			t.Fatalf("accepted removed support field: %s", invalid)
		}
	}
}
