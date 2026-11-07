package agent

import (
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const automationContextKey = "hearth_automation_context_v1"

// AutomationContext carries an unsaved proposal, never permission to save it.
type AutomationContext struct {
	Mode             string `json:"mode"`
	AutomationID     string `json:"automation_id,omitempty"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	DefinitionJSON   string `json:"definition_json"`
}

//nolint:goconst // JSON Schema keywords are clearer as literals.
func (*AutomationContext) Schema(_ huma.Registry) *huma.Schema {
	create := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"mode", "definition_json"},
		"properties": map[string]any{
			"mode": map[string]any{"const": "create"}, "definition_json": map[string]any{"type": "string"},
		},
	}
	replace := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"mode", "automation_id", "expected_revision", "definition_json"},
		"properties": map[string]any{
			"mode": map[string]any{
				"const": "replace",
			},
			"automation_id":     map[string]any{"type": "string"},
			"expected_revision": map[string]any{"type": "integer", "minimum": 1},
			"definition_json":   map[string]any{"type": "string"},
		},
	}
	return &huma.Schema{Extensions: map[string]any{"type": "object", "oneOf": []any{create, replace}}}
}

func (context *AutomationContext) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var mode string
	if err := json.Unmarshal(fields["mode"], &mode); err != nil {
		return fmt.Errorf("invalid automation context mode")
	}
	var required []string
	switch mode {
	case "create":
		required = []string{"mode", "definition_json"}
	case "replace":
		required = []string{"mode", "automation_id", "expected_revision", "definition_json"}
	default:
		return fmt.Errorf("invalid automation context mode")
	}
	if len(fields) != len(required) {
		return fmt.Errorf("invalid automation context fields")
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("invalid automation context fields")
		}
	}
	type wire AutomationContext
	var value wire
	if err := automations.DecodeStrictJSONObject(raw, &value); err != nil {
		return err
	}
	*context = AutomationContext(value)
	return context.validate()
}

func (context *AutomationContext) validate() error {
	switch context.Mode {
	case "create":
		if context.AutomationID != "" || context.ExpectedRevision != 0 {
			return fmt.Errorf("invalid create context")
		}
	case "replace":
		if _, err := automations.ParseAutomationID(context.AutomationID); err != nil {
			return err
		}
		if context.ExpectedRevision < 1 {
			return fmt.Errorf("invalid revision")
		}
	default:
		return fmt.Errorf("invalid automation context mode")
	}
	_, err := automations.DecodeDefinition([]byte(context.DefinitionJSON))
	return err
}

func rawAutomationContext(messageJSON []byte) json.RawMessage {
	var envelope struct {
		Extra map[string]json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(messageJSON, &envelope) != nil {
		return nil
	}
	return envelope.Extra[automationContextKey]
}
