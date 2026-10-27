package mcpapi //nolint:testpackage // Validate private server-side success contracts independently of SDK metadata.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Only contract tests reuse these fixture documents across their cases.
// Production prepares fresh contracts for each server registration.
//
//nolint:gochecknoglobals // Immutable test fixtures, not production preparation state.
var resultSchemaFixtures = sync.OnceValues(func() (map[string]*huma.Schema, error) {
	codec, err := automations.NewDefinitionCodec()
	if err != nil {
		return nil, err
	}
	definitions := canonicalResultDefinitions(codec.AutomationDefinitionSchema())
	detail, history := detailSchema(), historyDetailSchema()
	detail.Extensions = map[string]any{"$defs": definitions}
	history.Extensions = map[string]any{"$defs": definitions}
	return map[string]*huma.Schema{
		"receipt": generatedResultFixture[AutomationReceipt](), "detail": detail,
		"deleteReceipt":    generatedResultFixture[deleteAutomationToolOutput](),
		"summaries":        generatedResultFixture[AutomationPage](),
		"runReceipt":       generatedResultFixture[RunReceipt](),
		"historySummaries": generatedResultFixture[HistoryPage](), "historyDetail": history,
	}, nil
})

func generatedResultFixture[T any]() *huma.Schema {
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(registry, reflect.TypeFor[T]())
	if len(registry.Map()) != 0 {
		schema.Extensions = map[string]any{"$defs": registry.Map()}
	}
	return schema
}

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
		{"receipt", `{"automation_id":"","revision":1}`, false},
		{"deleteReceipt", `{"automation_id":"aut_example","deleted_revision":1,"deleted":true}`, true},
		{"deleteReceipt", `{"automation_id":"aut_example","deleted_revision":1,"deleted":false}`, false},
		{"deleteReceipt", `{"automation_id":"aut_example","deleted_revision":1}`, false},
		{"deleteReceipt", `{"automation_id":"aut_example","deleted_revision":0,"deleted":true}`, false},
		{"deleteReceipt", `{"automation_id":"","deleted_revision":1,"deleted":true}`, false},
		{"deleteReceipt", `{"automation_id":"aut_example","deleted_revision":1,"deleted":true,"extra":true}`, false},
		{"summaries", `{"items":[]}`, true},
		{"summaries", `{"items":null}`, false},
		{"summaries", `{"items":[{"automation_id":"aut_example","name":"Morning","enabled":true,"revision":1}]}`, true},
		{"summaries", `{"items":[{"automation_id":"aut_example","name":"Morning","enabled":true,"revision":1,"definition":{}}]}`, false},
		{"summaries", `{"items":[{"automation_id":"aut_example","name":"","enabled":true,"revision":1}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, true},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule","reason":"automation_busy"}]}`, true},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule","reason":"automation_busy","status":"failed"}]}`, false},
		{"historySummaries", `{"items":[]}`, true},
		{"historySummaries", `{"items":null}`, false},
		{"historySummaries", `{"items":[],"next_cursor":""}`, false},
		{"historySummaries", `{"items":[],"extra":true}`, false},
		{"historySummaries", `{"items":[{"entry_id":"","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":0,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"unknown","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"unknown","status":"running"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"unknown"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule","reason":"unknown"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"skip","automation_id":"aut","kind":"skip","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"schedule"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running","reason":"automation_busy"}]}`, false},
		{"historySummaries", `{"items":[{"entry_id":"run","automation_id":"aut","kind":"run","revision":1,"recorded_at":"2026-10-21T12:00:00Z","source":"manual","status":"running","extra":true}]}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running"}`, true},
		{"runReceipt", `{"automation_id":"","run_id":"run","status":"running"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"","status":"running"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"unknown"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running","failure_code":"executor_fault"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running","completed_at":"2026-10-21T12:00:00Z"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"succeeded","completed_at":"2026-10-21T12:00:00Z"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"succeeded"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"succeeded","completed_at":"2026-10-21T12:00:00Z","failure_code":"executor_fault"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00Z","failure_code":"executor_fault"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00Z"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","failure_code":"executor_fault"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00Z","failure_code":""}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":null,"failure_code":"executor_fault"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"interrupted","completed_at":"2026-10-21T12:00:00Z","failure_code":"core_restarted"}`, true},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"interrupted","completed_at":"2026-10-21T12:00:00Z"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"interrupted","failure_code":"core_restarted"}`, false},
		{"runReceipt", `{"automation_id":"aut","run_id":"run","status":"running","extra":true}`, false},
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
		{"running", `"phase":"running","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001"}`, true},
		{"accepted", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"accepted"}`, true},
		{"pre-attempt failure", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","reason_code":"notification_admission_closed"}`, true},
		{"interrupted", `"phase":"interrupted","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","reason_code":"core_restarted"}`, true},
		{"running invents outcome", `"phase":"running","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"accepted"}`, false},
		{"accepted without outcome", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001"}`, false},
		{"accepted unknown", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"unknown","reason_code":"outcome_timeout"}`, false},
		{"failed accepted", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"accepted","reason_code":"provider_error"}`, false},
		{"terminal null completion", `"phase":"interrupted","completed_at":null,"evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","reason_code":"core_restarted"}`, false},
		{"interrupted invents outcome", `"phase":"interrupted","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"unknown","reason_code":"core_restarted"}`, false},
		{"provider failure lacks outcome", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","reason_code":"provider_error"}`, false},
		{"unknown outcome with wrong reason", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"unknown","reason_code":"destination_unavailable"}`, false},
		{"unknown outcome with transport reason", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","outcome":"unknown","reason_code":"transport_error"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := `{"step_id":"notify","sequence":0,"kind":"notification","started_at":"2026-10-11T10:00:00Z",` + test.fields + `}`
			value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(document))
			if err != nil {
				t.Fatal(err)
			}
			schema := compileResultContract(t, "historyDetail", "#/$defs/evidence/$defs/notificationStepExecution")
			if validationErr := schema.Validate(value); (validationErr == nil) != test.valid {
				t.Fatalf("valid=%v, validation=%v", test.valid, validationErr)
			}
		})
	}
}
