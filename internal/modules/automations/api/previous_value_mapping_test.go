package api //nolint:testpackage // Tests exercise package-private HTTP and MCP mapping functions.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestObservationTransitionHTTPAndMCPMappingsPreserveNullAndOmission(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		previous devices.Value
		want     string
	}{
		{name: "absent"},
		{name: "JSON null", previous: devices.Value("null"), want: "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertTransitionValueMappings(t, test.previous, test.want)
		})
	}
}

func assertTransitionValueMappings(t *testing.T, previous devices.Value, want string) {
	t.Helper()
	summary := automations.DeviceFactSummary{
		FactID: "dfc_01890f47-7a6b-7c4d-8e9f-0123456789ab", Family: automations.DeviceFactObservation,
		EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Variant:  "applied", CausationID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		ObservationValue: devices.Value("9007199254740993"), PreviousStateValue: previous,
		EmittedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
	}
	for name, value := range map[string]any{
		"HTTP": deviceFactSummaryBody(summary),
		"MCP":  mcpFactOutput(deviceFactSummaryBody(summary)),
	} {
		assertTransitionMapping(t, name, value, want)
	}
}

func assertTransitionMapping(t *testing.T, name string, value any, want string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["observation_value"]); got != "9007199254740993" {
		t.Errorf("%s observation number = %s, want exact integer", name, got)
	}
	got, present := fields["previous_state_value"]
	if want == "" {
		if present {
			t.Errorf("%s emitted absent predecessor as %s", name, got)
		}
		return
	}
	if !present || string(got) != want {
		t.Errorf("%s previous_state_value = %s (present %v), want %s", name, got, present, want)
	}
}
