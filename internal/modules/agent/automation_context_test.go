package agent //nolint:testpackage // Tests inspect private model preparation.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

const automationDraftDefinition = `{"name":"Precise","enabled":false,"triggers":[],"variables":{"n":9007199254740993},"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`

func TestAutomationContextStrictAndExactModelPreparation(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(
		map[string]any{
			"mode":              "replace",
			"automation_id":     "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"expected_revision": 3,
			"definition_json":   automationDraftDefinition,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var draft AutomationContext
	if err = json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	message := &schema.Message{
		Role:    schema.User,
		Content: "change it",
		Extra:   map[string]any{automationContextKey: json.RawMessage(raw)},
	}
	prepared := prepareModelMessage(message)
	if !strings.Contains(prepared.Content, "9007199254740993") || !strings.Contains(prepared.Content, "user data") ||
		!strings.Contains(prepared.Content, `"expected_revision":3`) {
		t.Fatalf("prepared=%s", prepared.Content)
	}
	if _, ok := prepared.Extra[automationContextKey]; ok {
		t.Fatal("private context leaked")
	}
	if validationErr := validateMessageInput(
		MessageInput{Text: "edit", AutomationContext: &draft, ViewContext: &ViewContext{}},
	); validationErr == nil {
		t.Fatal("accepted two contexts")
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), `"mode":"replace"`, `"mode":"create"`, 1),
		strings.Replace(string(raw), `"expected_revision":3`, `"expected_revision":0`, 1),
		strings.TrimSuffix(string(raw), "}") + `,"unknown":true}`,
		strings.Replace(string(raw), `aut_01890f47`, `bad_01890f47`, 1),
	} {
		var value AutomationContext
		if json.Unmarshal([]byte(invalid), &value) == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	oversized := AutomationContext{Mode: "create", DefinitionJSON: strings.Repeat(" ", 128<<10+1)}
	if oversized.validate() == nil {
		t.Fatal("accepted an oversized definition")
	}
}
