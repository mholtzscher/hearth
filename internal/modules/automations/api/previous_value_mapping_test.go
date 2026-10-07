package api //nolint:testpackage // Tests exercise the shared public mapping.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestObservationFactMappingPreservesNullAndOmission(t *testing.T) {
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
	fact := automations.ObservationFact{
		FactID:      "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:    "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Disposition: devices.DispositionApplied, ObservationID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Value: devices.Value("9007199254740993"), PreviousValue: previous,
		EmittedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
	}
	assertTransitionMapping(t, "shared DTO", struct {
		Cause AdmissionCauseBody `json:"cause"`
	}{Cause: admissionCauseBody(automations.DeviceFactCause{Fact: fact})}, want)
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
	for _, removed := range []string{"source", "fact", "held_state"} {
		if _, present := fields[removed]; present {
			t.Errorf("%s emitted removed provenance field %s", name, removed)
		}
	}
	var cause map[string]json.RawMessage
	if err = json.Unmarshal(fields["cause"], &cause); err != nil {
		t.Fatal(err)
	}
	if string(cause["kind"]) != `"device_fact"` {
		t.Fatalf("%s cause = %s", name, fields["cause"])
	}
	if err = json.Unmarshal(cause["fact"], &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["family"]) != `"observation"` ||
		string(fields["observation_id"]) != `"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"` {
		t.Errorf("%s Observation identity = %s", name, cause["fact"])
	}
	if _, present := fields["variant"]; present {
		t.Errorf("%s emitted overloaded variant", name)
	}
	if _, present := fields["causation_id"]; present {
		t.Errorf("%s emitted overloaded causation_id", name)
	}
	if got := string(fields["value"]); got != "9007199254740993" {
		t.Errorf("%s observation number = %s, want exact integer", name, got)
	}
	got, present := fields["previous_value"]
	if want == "" {
		if present {
			t.Errorf("%s emitted absent predecessor as %s", name, got)
		}
		return
	}
	if !present || string(got) != want {
		t.Errorf("%s previous_value = %s (present %v), want %s", name, got, present, want)
	}
}
