package agent //nolint:testpackage // Exercises private context preparation.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

const testViewContext = `{"definition":{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Draft","spec":{"root":"n","elements":{"n":{"type":"Note","props":{"text":"Hello"}}}}}}`

func TestViewContextStrictAdmissionAndCopy(t *testing.T) {
	t.Parallel()
	var view ViewContext
	if err := json.Unmarshal([]byte(testViewContext), &view); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(testViewContext, `"title":"Draft"`, `"title":"Draft","unknown":true`, 1),
		strings.TrimSuffix(testViewContext, "}") + `,"source_revision":1}`,
		strings.TrimSuffix(testViewContext, "}") + `,"source_dashboard_id":null,"source_revision":null}`,
		strings.TrimSuffix(testViewContext, "}") + `,"source_dashboard_id":null}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &view); err == nil {
			t.Fatalf("accepted invalid context %s", invalid)
		}
	}
	message := schema.UserMessage("Prompt")
	message.Extra = map[string]any{viewContextKey: json.RawMessage(testViewContext), "other": "retained"}
	prepared := prepareModelMessage(message)
	if prepared == message || message.Content != "Prompt" || message.Extra[viewContextKey] == nil ||
		prepared.Extra[viewContextKey] != nil ||
		prepared.Extra["other"] != "retained" ||
		!strings.Contains(prepared.Content, "Draft") {
		t.Fatalf("prepared: %+v original: %+v", prepared, message)
	}
	if prepareModelMessage(prepared).Content != prepared.Content {
		t.Fatal("expanded twice")
	}
}

func TestModelPreparationPreservesSelectedRevisionPrecision(t *testing.T) {
	t.Parallel()
	contextJSON := strings.TrimSuffix(
		testViewContext,
		"}",
	) + `,"source_dashboard_id":"dash_018f0f4a-7b29-7000-8000-000000000001","source_revision":9007199254740993}`
	row := dbsqlc.ListRecentMessageJsonRow{
		Role:        "user",
		MessageJson: `{"role":"user","content":"Prompt","extra":{"hearth_view_context_v1":` + contextJSON + `}}`,
	}
	prepared, err := historyMessageFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.message.Content, `"source_revision":9007199254740993`) {
		t.Fatalf("revision changed: %s", prepared.message.Content)
	}
}

func TestUnsupportedViewContextIsRetainedButNotExpanded(t *testing.T) {
	t.Parallel()
	raw := strings.Replace(testViewContext, `"schema_version":1`, `"schema_version":2`, 1)
	message := schema.UserMessage("Prompt")
	message.Extra = map[string]any{viewContextKey: json.RawMessage(raw)}
	stored, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(retainedViewContext(stored)), `"schema_version":2`) {
		t.Fatal("unsupported context was lost")
	}
	if prepared := prepareModelMessage(message); prepared.Content != "Prompt" || prepared.Extra[viewContextKey] != nil {
		t.Fatalf("unsupported context sent to model: %+v", prepared)
	}
}
