package automations_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
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
		`{"sequence":1,"step_id":"value","recorded_at":"2026-10-25T12:00:00Z","kind":"function_output","output":9223372036854775808}`,
	} {
		if _, err = automations.DecodeRunDataRecord(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid body %s", raw)
		}
	}
}
