package mcpapi //nolint:testpackage // Validate private server-side success contracts independently of SDK metadata.

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Only contract tests reuse these fixture documents across their cases.
// Production prepares fresh contracts for each server registration.
//
//nolint:gochecknoglobals // Immutable test fixtures, not production preparation state.
var resultSchemaFixtures = sync.OnceValues(func() (map[string]map[string]any, error) {
	codec, err := automations.NewDefinitionCodec()
	if err != nil {
		return nil, err
	}
	definitions := canonicalResultDefinitions(codec.AutomationDefinitionSchema())
	detail, history := detailSchema(), historyDetailSchema()
	detail["$defs"], history["$defs"] = definitions, definitions
	return map[string]map[string]any{
		"receipt": receiptSchema(), "detail": detail,
		"summaries": pageSchema(summarySchema()), "runReceipt": runReceiptSchema(),
		"historySummaries": pageSchema(historySummarySchema()), "historyDetail": history,
	}, nil
})

func compileResultContract(t *testing.T, name, fragment string) *jsonschema.Schema {
	t.Helper()
	fixtures, err := resultSchemaFixtures()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(fixtures[name])
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/result", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://hearth.invalid/result" + fragment)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestReceiptAndSummaryContractsAreClosed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, document string
		valid          bool
	}{
		{"receipt", `{"automation_id":"aut_example","revision":1}`, true},
		{"receipt", `{"automation_id":"aut_example","revision":1,"definition":{}}`, false},
		{"receipt", `{"automation_id":"aut_example","revision":0}`, false},
		{"summaries", `{"items":[]}`, true},
		{"summaries", `{"items":null}`, false},
		{"summaries", `{"items":[{"automation_id":"aut_example","name":"Morning","enabled":true,"revision":1}]}`, true},
		{"summaries", `{"items":[{"automation_id":"aut_example","name":"Morning","enabled":true,"revision":1,"definition":{}}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, true},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule","reason":"automation_busy"}]}`, true},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule","reason":"automation_busy","status":"failed"}]}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running","completed_at":"2026-10-21T12:00:00Z"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"succeeded","completed_at":"2026-10-21T12:00:00Z"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00Z","failure_code":"executor_fault"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":null,"failure_code":"executor_fault"}`, false},
	} {
		t.Run(test.name+test.document, func(t *testing.T) {
			t.Parallel()
			value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(test.document))
			if err != nil {
				t.Fatal(err)
			}
			if validationErr := compileResultContract(
				t,
				test.name,
				"",
			).Validate(value); (validationErr == nil) != test.valid {
				t.Fatalf("valid=%v, validation=%v", test.valid, validationErr)
			}
		})
	}
}

func TestCanonicalNestedEvidenceRemainsConstrained(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, fields string
		valid        bool
	}{
		{"running", `"status":"running","completed_at":null`, true},
		{"accepted", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z","outcome":"accepted"`, true},
		{"pre-attempt failure", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","reason_code":"notification_admission_closed"`, true},
		{"interrupted", `"status":"interrupted","completed_at":"2026-10-11T10:00:01Z","reason_code":"core_restarted"`, true},
		{"running invents outcome", `"status":"running","completed_at":null,"outcome":"accepted"`, false},
		{"accepted without outcome", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z"`, false},
		{"accepted unknown", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"outcome_timeout"`, false},
		{"failed accepted", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","outcome":"accepted","reason_code":"provider_error"`, false},
		{"terminal null completion", `"status":"interrupted","completed_at":null,"reason_code":"core_restarted"`, false},
		{"interrupted invents outcome", `"status":"interrupted","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"core_restarted"`, false},
		{"provider failure lacks outcome", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","reason_code":"provider_error"`, false},
		{"unknown wrong reason", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"destination_unavailable"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := `{"step_id":"notify","position":0,"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","started_at":"2026-10-11T10:00:00Z",` + test.fields + `}`
			value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(document))
			if err != nil {
				t.Fatal(err)
			}
			schema := compileResultContract(t, "historyDetail", "#/$defs/evidence/$defs/notificationExecution")
			if validationErr := schema.Validate(value); (validationErr == nil) != test.valid {
				t.Fatalf("valid=%v, validation=%v", test.valid, validationErr)
			}
		})
	}
}
