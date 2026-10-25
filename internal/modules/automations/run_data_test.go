package automations_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // All six wire bodies share the same exact-value, schema and invalid-envelope checks.
func TestRunDataCanonicalVariantsAndStrictRejection(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	id := automations.StepID("value")
	reason := automations.ConditionUnknownEntityMissing
	bodies := []automations.RunDataBody{
		automations.VariablesInitialized{Values: json.RawMessage(`{"target":9007199254740993}`)},
		automations.InputsResolved{
			Values: json.RawMessage(`{"parameters":{"vendor":18446744073709551615,"expr":"not code"}}`),
		},
		automations.VariablesAssigned{
			Values: json.RawMessage(`{"minimum":-9223372036854775808,"maximum":9223372036854775807}`),
		},
		automations.StateCaptured{
			EntityID:       "ent_01980000-0000-7000-8000-000000000001",
			Pointer:        "",
			ReadAt:         at,
			UnusableReason: &reason,
			Output:         json.RawMessage(`null`),
		},
		automations.FunctionOutputCaptured{Output: json.RawMessage(`{"nested":[9007199254740993]}`)},
		automations.RunDataFailed{Path: "/steps/0/parameters", FailureCode: "expression_missing"},
	}
	schema, err := historySchemaCompiler(t).Compile("urn:hearth:schema:automation-history:v3#/$defs/runDataRecord")
	if err != nil {
		t.Fatal(err)
	}
	for index, body := range bodies {
		record := automations.RunDataRecord{Sequence: index, StepID: &id, RecordedAt: at, Body: body}
		if index == 0 {
			record.StepID = nil
		}
		raw, encodeErr := automations.EncodeRunDataRecord(record)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		decoded, decodeErr := automations.DecodeRunDataRecord(raw)
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", raw, decodeErr)
		}
		again, encodeErr := automations.EncodeRunDataRecord(decoded)
		if encodeErr != nil || !bytes.Equal(raw, again) {
			t.Fatalf("round trip changed exact evidence: %s -> %s, %v", raw, again, encodeErr)
		}
		var document any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&document); err != nil {
			t.Fatal(err)
		}
		if err = schema.Validate(document); err != nil {
			t.Fatalf("canonical schema rejects %s: %v", raw, err)
		}
		for _, bad := range []string{
			strings.TrimSuffix(string(raw), "}") + `,"extra":true}`,
			strings.TrimSuffix(string(raw), "}") + `,"source_start":null}`,
			strings.Replace(string(raw), `"sequence":`+string(rune('0'+index)), `"sequence":256`, 1),
			strings.Replace(string(raw), `"recorded_at":"2026-10-25T12:00:00Z"`, `"recorded_at":"2026-10-25T13:00:00+01:00"`, 1),
		} {
			if _, err = automations.DecodeRunDataRecord(json.RawMessage(bad)); err == nil {
				t.Fatalf("accepted invalid data %s", bad)
			}
		}
	}
	for _, raw := range []string{
		`{"sequence":0,"recorded_at":"2026-10-25T12:00:00Z","kind":"variables_initialized","values":null}`,
		`{"sequence":1,"step_id":"value","recorded_at":"2026-10-25T12:00:00Z","kind":"inputs_resolved","values":{"parameters":null}}`,
		`{"sequence":1,"step_id":"value","recorded_at":"2026-10-25T12:00:00Z","kind":"inputs_resolved","values":{"arguments":{},"title":"x"}}`,
		`{"sequence":1,"step_id":"value","recorded_at":"2026-10-25T12:00:00Z","kind":"function_output","output":1e309}`,
	} {
		if _, err = automations.DecodeRunDataRecord(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid body %s", raw)
		}
	}
}

func TestRunDataResolvedFloatEvidenceRoundTrips(t *testing.T) {
	t.Parallel()
	const envelope = `"sequence":1,"step_id":"value","recorded_at":"2026-10-25T12:00:00Z",`
	for _, body := range []string{
		`"kind":"variables_assigned","values":{"n":9223372036854776000}`,
		`"kind":"function_output","output":{"n":9223372036854776000}`,
		`"kind":"state_captured","entity_id":"ent_01980000-0000-7000-8000-000000000001","value_pointer":"","read_at":"2026-10-25T12:00:00Z","observation":{"observation_id":"obs_01980000-0000-7000-8000-000000000001","observed_at":"2026-10-25T12:00:00Z"},"output":9223372036854776000`,
	} {
		raw := json.RawMessage(`{` + envelope + body + `}`)
		record, err := automations.DecodeRunDataRecord(raw)
		if err != nil {
			t.Errorf("valid resolved float evidence rejected: %s: %v", raw, err)
			continue
		}
		encoded, err := automations.EncodeRunDataRecord(record)
		if err != nil || !bytes.Contains(encoded, []byte(`9223372036854776000`)) {
			t.Errorf("numeric evidence changed: %s -> %s: %v", raw, encoded, err)
		}
		again, err := automations.DecodeRunDataRecord(encoded)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := automations.EncodeRunDataRecord(again)
		if err != nil || !bytes.Equal(encoded, roundTrip) {
			t.Errorf("canonical evidence changed: %s -> %s: %v", encoded, roundTrip, err)
		}
	}
}

func TestRunDataRejectsExplicitEmptyStateUnusableReason(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	id := automations.StepID("read")
	entity := devices.EntityID("ent_01980000-0000-7000-8000-000000000001")
	observation := &automations.ObservationEvidence{
		ObservationID: "obs_01980000-0000-7000-8000-000000000001", ObservedAt: at,
	}
	empty := automations.ConditionUnknownReason("")
	for _, body := range []automations.RunDataBody{
		automations.StateCaptured{EntityID: entity, ReadAt: at, Observation: observation, Output: json.RawMessage(`null`), UnusableReason: &empty},
		automations.RunDataFailed{Path: "/steps/0", FailureCode: "read_state_unusable", State: &automations.StateReadFailure{
			EntityID: entity, ReadAt: at, Observation: observation, UnusableReason: empty,
		}},
	} {
		record := automations.RunDataRecord{Sequence: 1, StepID: &id, RecordedAt: at, Body: body}
		if err := automations.ValidateRunDataRecord(record); err == nil {
			t.Errorf("accepted empty unusable reason in %T", body)
		}
		if _, err := automations.EncodeRunDataRecord(record); err == nil {
			t.Errorf("encoded empty unusable reason in %T", body)
		}
	}
	const prefix = `{"sequence":1,"step_id":"read","recorded_at":"2026-10-25T12:00:00Z",`
	const state = `"entity_id":"ent_01980000-0000-7000-8000-000000000001","value_pointer":"","read_at":"2026-10-25T12:00:00Z","observation":{"observation_id":"obs_01980000-0000-7000-8000-000000000001","observed_at":"2026-10-25T12:00:00Z"},"unusable_reason":""`
	for _, raw := range []string{
		prefix + `"kind":"state_captured",` + state + `,"output":null}`,
		prefix + `"kind":"failed","path":"/steps/0","failure_code":"read_state_unusable","state":{` + state + `}}`,
	} {
		if _, err := automations.DecodeRunDataRecord(json.RawMessage(raw)); err == nil {
			t.Errorf("decoded explicit empty unusable reason: %s", raw)
		}
	}
}

func TestRunDataLiteralOwnershipUsesNormalizedEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		literal  string
		retained string
		valid    bool
	}{
		{`0.9999999999999999999`, `1`, true},
		{`1e3`, `1000.0`, true},
		{`9007199254740993`, `9007199254740993`, true},
		{`9007199254740993`, `9007199254740992`, false},
		{`9223372036854775807`, `9223372036854775806`, false},
		{`9223372036854775808`, `9223372036854776000`, false},
	} {
		snapshot, err := automations.NormalizeDefinition(automations.Definition{
			Name:     "Evidence",
			Triggers: []automations.Trigger{{ID: "tick", Body: automations.CronTrigger{Expression: "* * * * *"}}},
			Variables: map[automations.VariableName]automations.ValueBinding{
				"n": {Body: automations.LiteralValue{Value: json.RawMessage(test.literal)}},
			},
			Steps: []automations.Step{{ID: "wait", Body: automations.DelayStep{DurationMS: 1}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		record := automations.RunDataRecord{
			RecordedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
			Body:       automations.VariablesInitialized{Values: json.RawMessage(`{"n":` + test.retained + `}`)},
		}
		err = automations.ValidateRunDataWithPreparedSnapshot(record, snapshot)
		if (err == nil) != test.valid {
			t.Errorf("literal %s against evidence %s: %v; want valid=%t", test.literal, test.retained, err, test.valid)
		}
	}
}
