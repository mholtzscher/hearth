package mcpapi //nolint:testpackage // Exercise the actual private success contract, not SDK discovery metadata.

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Limits are independent expectations from the approved result contract. Every
// case validates the complete direct Run or Skip root with canonical items.
func TestHistoryDetailArrayBoundsAndCanonicalItems(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		field, kind, item string
		maximum           int
	}{
		{"executions", "run", `{"step_id":"wait","sequence":0,"kind":"delay","phase":"running","started_at":"2026-10-21T12:00:00Z","evidence":{}}`, 64},
		{"matched_triggers", "skip", `{"id":"tick","kind":"cron","expression":"* * * * *"}`, 32},
	} {
		t.Run(test.field, func(t *testing.T) {
			t.Parallel()
			schema := compileResultContract(t, "historyDetail", "")
			item, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(test.item))
			if err != nil {
				t.Fatal(err)
			}
			items := make([]any, test.maximum)
			for index := range items {
				items[index] = item
			}
			root := historyArrayContractRoot(t, test.kind)
			root[test.field] = items
			if validationErr := schema.Validate(root); validationErr != nil {
				t.Fatalf("maximum %d must be accepted: %v", test.maximum, validationErr)
			}
			overLimit := make([]any, test.maximum+1)
			copy(overLimit, items)
			overLimit[test.maximum] = item
			root[test.field] = overLimit
			if validationErr := schema.Validate(root); validationErr == nil {
				t.Fatalf("maximum+1 (%d) must be rejected", test.maximum+1)
			}
			// The length restriction must not replace the canonical item schema.
			items[test.maximum-1] = map[string]any{}
			root[test.field] = items
			if validationErr := schema.Validate(root); validationErr == nil {
				t.Fatal("malformed canonical item at the maximum length must be rejected")
			}
		})
	}
}

func historyArrayContractRoot(t *testing.T, kind string) map[string]any {
	t.Helper()
	const common = `"entry_id":"entry","automation_id":"automation","automation_name":"Example","revision":1,"recorded_at":"2026-10-21T12:00:00Z","cause":{"kind":"manual"},"condition_decision":{"mode":"not_configured","bypass_requested":false}`
	document := `{` + common + `,"kind":"run","status":"running","started_at":"2026-10-21T12:00:00Z","snapshot":{"name":"Example","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]},"matched_trigger_ids":[],"executions":[],"data_evidence":"legacy_unavailable","value_initialization":null}`
	if kind == "skip" {
		document = `{` + common + `,"kind":"skip","reason":"automation_busy","skipped_at":"2026-10-21T12:00:00Z","matched_triggers":[]}`
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(document))
	if err != nil {
		t.Fatal(err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		t.Fatal("history fixture must be a JSON object")
	}
	return root
}
