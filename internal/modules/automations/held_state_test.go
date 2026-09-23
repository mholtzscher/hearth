package automations_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestHeldStateDefinitionRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	raw := json.RawMessage(
		`{"name":"Held light","enabled":true,"triggers":[{"id":"held","kind":"held_state","entity_id":"` +
			string(entity) + `","comparisons":[{"value_pointer":"","operator":"eq","operand":true}],` +
			`"for_seconds":60}],"steps":[{"id":"step","entity_id":"` +
			string(entity) + `","operation":"set","parameters":{}}]}`,
	)
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := definition.Triggers[0].HeldState; got == nil || got.ForSeconds != 60 ||
		got.Comparisons[0].Operand == nil {
		t.Fatalf("decoded held trigger = %#v", got)
	}
	encoded, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Triggers[0].Kind != automations.TriggerKindHeldState || decoded.Triggers[0].HeldState.ForSeconds != 60 {
		t.Fatalf("round-trip trigger = %#v", decoded.Triggers[0])
	}
	for _, invalid := range []string{
		`"for_seconds":1.5`, `"for_seconds":0`, `"for_seconds":2592001`,
		`"for_seconds":60,"event_name":"press"`, `"comparisons":[]`,
		`"unknown":true`, `"comparisons":[{"value_pointer":"bad","operator":"eq","operand":true}]`,
		"MISSING_COMPARISONS",
	} {
		bad := string(raw)
		switch invalid {
		case `"for_seconds":1.5`, `"for_seconds":0`, `"for_seconds":2592001`:
			bad = replaceHeldField(bad, `"for_seconds":60`, invalid)
		case `"for_seconds":60,"event_name":"press"`:
			bad = replaceHeldField(bad, `"for_seconds":60`, invalid)
		case `"comparisons":[]`:
			bad = replaceHeldField(bad, `"comparisons":[{"value_pointer":"","operator":"eq","operand":true}]`, invalid)
		case `"unknown":true`:
			bad = replaceHeldField(bad, `"for_seconds":60`, `"for_seconds":60,"unknown":true`)
		case `"comparisons":[{"value_pointer":"bad","operator":"eq","operand":true}]`:
			bad = replaceHeldField(bad, `"value_pointer":""`, `"value_pointer":"bad"`)
		case "MISSING_COMPARISONS":
			bad = replaceHeldField(bad, `,"comparisons":[{"value_pointer":"","operator":"eq","operand":true}]`, "")
		}
		if _, err = automations.DecodeDefinition(json.RawMessage(bad)); err == nil {
			t.Errorf("DecodeDefinition accepted invalid held trigger %s", bad)
		}
	}
}

func replaceHeldField(source, old, replacement string) string {
	for i := 0; i+len(old) <= len(source); i++ {
		if source[i:i+len(old)] == old {
			return source[:i] + replacement + source[i+len(old):]
		}
	}
	return source
}

func TestHeldStateMatchAndImmediateAdmissionAreDistinct(t *testing.T) {
	t.Parallel()
	comparison := automations.ObservationComparison{
		Pointer: "/on", Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
	}
	trigger := automations.HeldStateTrigger{
		EntityID: newEntityID(t), Comparisons: []automations.ObservationComparison{comparison}, ForSeconds: 10,
	}
	matched, err := automations.MatchHeldState(trigger, devices.Value(`{"on":true}`))
	if err != nil || !matched {
		t.Fatalf("MatchHeldState = %v, %v", matched, err)
	}
	matched, err = automations.MatchHeldState(trigger, devices.Value(`{"on":false}`))
	if err != nil || matched {
		t.Fatalf("nonmatching State = %v, %v", matched, err)
	}
	definition := automations.Definition{
		Triggers: []automations.Trigger{{
			ID: "held", Kind: automations.TriggerKindHeldState, HeldState: &trigger,
		}},
	}
	fact := automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID:        "dfc_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			ObservationID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityID:      trigger.EntityID,
			Disposition:   devices.DispositionApplied,
			Value:         devices.Value(`{"on":true}`),
			EmittedAt:     time.Now(),
		},
	}
	ids, err := automations.MatchTriggers(fact, definition)
	if err != nil || len(ids) != 0 {
		t.Fatalf("immediate matches = %v, %v", ids, err)
	}
	duration, durationErr := automations.HeldStateDuration(2_592_000)
	if durationErr != nil || duration != 30*24*time.Hour {
		t.Fatalf("maximum duration = %v, %v", duration, durationErr)
	}
}
