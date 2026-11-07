package agent //nolint:testpackage // Exercises the model-facing MCP schema conversion.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	automationmcp "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestAutomationModelSchemaExposesTypedArguments(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "automation-schema", Version: "1"})
	automationmcp.Register(server, nil)
	info, err := bridgeToolByName(t, mustBridgeTools(t, server), "present_automation").Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := toolParameters(info)
	if err != nil {
		t.Fatal(err)
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("presentation arguments have no top-level property guidance")
	}
	for name, want := range map[string]string{"mode": "string", "automation_id": "string", "expected_revision": "integer", "definition": "object"} {
		property, exists := properties[name].(map[string]any)
		if !exists || property["type"] != want {
			t.Errorf("%s: got %v, want explicit type %s", name, property, want)
		}
	}
	if variants, exists := parameters["oneOf"].([]any); !exists || len(variants) != 3 || parameters["$defs"] == nil {
		t.Fatal("closed presentation variants or canonical definition schema lost")
	}
	chatTools, err := goChatTools([]*schema.ToolInfo{info})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(chatTools)
	if err != nil {
		t.Fatal(err)
	}
	var sent []struct {
		Function struct {
			Parameters  map[string]any `json:"parameters"`
			Description string         `json:"description"`
		} `json:"function"`
	}
	if err = json.Unmarshal(raw, &sent); err != nil || len(sent) != 1 {
		t.Fatalf("chat tools: %s %v", raw, err)
	}
	if !reflect.DeepEqual(sent[0].Function.Parameters, parameters) {
		t.Fatal("Chat provider changed the parameter contract")
	}
	if !strings.Contains(sent[0].Function.Description, `"mode":"reference"`) {
		t.Fatal("model-facing presentation tool omits the saved-reference invocation")
	}
}
