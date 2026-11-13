package esphome //nolint:testpackage // Text routes are deliberately non-commandable.

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestProjectTextSensorPreservesEmptyAndMissingState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		text, want       string
		missing, present bool
	}{
		{"Improved", `"Improved"`, false, true}, {"", `""`, false, true}, {"", "", true, false},
	} {
		observation, ok, err := project(entityRoute{Kind: routeText}, "ent_text", nativeapi.StateUpdate{
			Type: nativeapi.EntityTextSensor, Text: test.text, Missing: test.missing,
		}, projectionTime())
		if err != nil || ok != test.present || string(observation.Value) != test.want {
			t.Errorf("text=%q missing=%t: %s,%t,%v", test.text, test.missing, observation.Value, ok, err)
		}
	}
	if _, _, err := prepareSet(
		boundRoute{Kind: routeText},
		[]byte(`{"value":"Improved"}`),
	); err == nil {
		t.Fatal("text sensor accepted a write")
	}
}
