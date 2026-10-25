package mcpapi //nolint:testpackage // Validate MCP's independently owned canonical argument and history mappings.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestValueBindingsMCPV3OnlyAndEnvelopeLimit(t *testing.T) {
	t.Parallel()
	const prefix = `{"definition":{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[`
	for _, test := range []struct {
		step  string
		valid bool
	}{
		{`{"id":"set","kind":"set_variables","assignments":{"x":{"literal":9007199254740993}}}`, true},
		{`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`, true},
		{`{"id":"wait","kind":"delay","duration_ms":1}`, false},
		{`{"id":"wait","kind":"delay","duration_ms":{"expr":"1"}}`, false},
		{`{"id":"set","kind":"set_variables","assignments":{"x":{"expr":"let private_source = 'private_value'; private_source"}}}`, false},
	} {
		_, err := definitionArgument(json.RawMessage(prefix + test.step + `]}}`))
		if (err == nil) != test.valid {
			t.Fatalf("valid=%t error=%v", test.valid, err)
		}
		if err != nil &&
			(strings.Contains(err.Error(), "private_source") || strings.Contains(err.Error(), "private_value")) {
			t.Fatal("expression source leaked")
		}
	}
	if _, err := definitionArgument(json.RawMessage(strings.Repeat(" ", automationRequestMaxBytes+1))); err == nil {
		t.Fatal("oversized argument envelope accepted")
	}
}

func TestRunDataMCPDetailCopiesExactJournalOnlyOnDetail(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	definition, err := automations.DecodeDefinition(
		json.RawMessage(
			`{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[{"id":"wait","kind":"delay","duration_ms":{"literal":1}}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := automations.NewRunSnapshot(
		automations.Record{ID: "aut_01980000-0000-7000-8000-000000000001", Revision: 1, Definition: definition},
		"arn_01980000-0000-7000-8000-000000000001",
		automations.ManualCause{},
		nil,
		automations.NotConfiguredDecision(),
		at,
	)
	run.Data = []automations.RunDataRecord{
		{
			Sequence:   0,
			RecordedAt: at,
			Body:       automations.VariablesInitialized{Values: json.RawMessage(`{"n":9007199254740993}`)},
		},
	}
	detail, err := runDetail(run)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"data_evidence":"recorded"`) ||
		!strings.Contains(string(raw), `"n":9007199254740993`) {
		t.Fatalf("exact journal lost: %s", raw)
	}
	contract := compileResultContract(t, "historyDetail", "")
	var document any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err = decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	if err = contract.Validate(document); err != nil {
		t.Fatal(err)
	}
}
