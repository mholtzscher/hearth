package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerPresentAutomation(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[presentationInput, presentationOutput]{
		Name:        "present_automation",
		Description: "Present a saved Automation or an unsaved create or replacement proposal. This never saves or runs it.",
		Handler:     handler.presentAutomation,
	})
}

type presentationInput struct{ value automations.Presentation }

func (input *presentationInput) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Mode             string           `json:"mode"`
		AutomationID     automationID     `json:"automation_id"`
		ExpectedRevision expectedRevision `json:"expected_revision"`
		Definition       json.RawMessage  `json:"definition"`
	}
	if err := automations.DecodeStrictJSONObject(raw, &wire); err != nil {
		return &mcptools.ToolError{Code: "invalid_arguments", Message: "invalid presentation arguments"}
	}
	switch wire.Mode {
	case "reference":
		input.value = automations.PresentReference{AutomationID: automations.AutomationID(wire.AutomationID)}
	case "create":
		input.value = automations.PresentCreate{DefinitionJSON: string(wire.Definition)}
	case "replace":
		input.value = automations.PresentReplace{
			AutomationID:     automations.AutomationID(wire.AutomationID),
			ExpectedRevision: int64(wire.ExpectedRevision),
			DefinitionJSON:   string(wire.Definition),
		}
	default:
		return &mcptools.ToolError{Code: "invalid_arguments", Message: "invalid presentation mode"}
	}
	return nil
}

//nolint:goconst // JSON Schema keywords are clearer as literals.
func (presentationInput) Schema(_ huma.Registry) *huma.Schema {
	definition := schemaDocument(automations.AutomationDefinitionSchema())
	delete(definition, "$id")
	relocateReferences(definition, "#/$defs/definition")
	return &huma.Schema{Extensions: map[string]any{
		"type":  "object",
		"$defs": map[string]any{"definition": definition},
		"oneOf": []any{
			presentationSchemaVariant("reference", false, true, false, false),
			presentationSchemaVariant("create", true, false, false, false),
			presentationSchemaVariant("replace", true, true, true, false),
		},
	}}
}

type presentationOutput struct{ artifact automations.Artifact }

func (output presentationOutput) MarshalJSON() ([]byte, error) { return json.Marshal(output.artifact) }
func (presentationOutput) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Extensions: map[string]any{"type": "object", "oneOf": []any{
		presentationSchemaVariant("reference", false, true, false, true),
		presentationSchemaVariant("create", true, false, false, true),
		presentationSchemaVariant("replace", true, true, true, true),
	}}}
}

//nolint:goconst // JSON Schema keywords are clearer as literals.
func presentationSchemaVariant(mode string, definition, id, revision, output bool) map[string]any {
	properties := map[string]any{"mode": map[string]any{"const": mode}}
	required := []string{"mode"}
	if output {
		properties["kind"] = map[string]any{"const": "hearth.automation"}
		properties["artifact_id"] = map[string]any{"type": "string"}
		properties["schema_version"] = map[string]any{"const": 1}
		required = append(required, "kind", "artifact_id", "schema_version")
	}
	if id {
		properties["automation_id"] = map[string]any{"type": "string"}
		required = append(required, "automation_id")
	}
	if revision {
		properties["expected_revision"] = map[string]any{"type": "integer", "minimum": 1}
		required = append(required, "expected_revision")
	}
	if definition {
		name := "definition"
		properties[name] = map[string]any{"$ref": "#/$defs/definition"}
		if output {
			delete(properties, name)
			name = "definition_json"
			properties[name] = map[string]any{"type": "string"}
		}
		required = append(required, name)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func (handler *handler) presentAutomation(ctx context.Context, input presentationInput) (presentationOutput, error) {
	artifact, err := handler.automations.PresentAutomation(ctx, input.value)
	if err != nil {
		return presentationOutput{}, automationFailure(err, failureAutomationNotFound)
	}
	return presentationOutput{artifact: artifact}, nil
}
