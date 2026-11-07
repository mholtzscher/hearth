package agent

import (
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

// Artifact is the agent's narrow projection of the two product-owned formats.
type Artifact struct {
	dashboards.Artifact `json:"-"`

	Automation *automations.Artifact `json:"-"`
}

const firstUnsupportedAutomationArtifactVersion = 2

// Schema documents the two product formats without moving their ownership.
//
//nolint:goconst // JSON Schema keywords are clearer as literals.
func (Artifact) Schema(_ huma.Registry) *huma.Schema {
	view := map[string]any{
		"type":     "object",
		"required": []string{"kind", "artifact_id", "definition"},
		"properties": map[string]any{
			"kind": map[string]any{
				"const": "hearth.view",
			},
			"artifact_id": map[string]any{"type": "string"},
			"definition":  map[string]any{"type": "object"},
		},
	}
	automation := map[string]any{
		"type":     "object",
		"required": []string{"kind", "artifact_id", "schema_version"},
		"properties": map[string]any{
			"kind": map[string]any{
				"const": "hearth.automation",
			},
			"artifact_id":    map[string]any{"type": "string"},
			"schema_version": map[string]any{"type": "integer"},
			"mode":           map[string]any{"type": "string"},
			"automation_id": map[string]any{
				"type": "string",
			},
			"expected_revision": map[string]any{"type": "integer"},
			"definition_json":   map[string]any{"type": "string"},
		},
		"oneOf": []any{
			artifactModeSchema("reference", []string{"automation_id"}),
			artifactModeSchema("create", []string{"definition_json"}),
			artifactModeSchema("replace", []string{"automation_id", "expected_revision", "definition_json"}),
			map[string]any{
				"properties": map[string]any{
					"schema_version": map[string]any{
						"type":    "integer",
						"minimum": firstUnsupportedAutomationArtifactVersion,
					},
				},
			},
		},
	}
	return &huma.Schema{Extensions: map[string]any{"oneOf": []any{view, automation}}}
}

//nolint:goconst // JSON Schema keywords are clearer as literals.
func artifactModeSchema(mode string, fields []string) map[string]any {
	properties := map[string]any{
		"kind":           map[string]any{"const": "hearth.automation"},
		"artifact_id":    map[string]any{"type": "string"},
		"schema_version": map[string]any{"const": 1},
		"mode":           map[string]any{"const": mode},
	}
	for _, field := range fields {
		if field == "expected_revision" {
			properties[field] = map[string]any{"type": "integer", "minimum": 1}
		} else {
			properties[field] = map[string]any{"type": "string"}
		}
	}
	required := append([]string{"kind", "artifact_id", "schema_version", "mode"}, fields...)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func (artifact Artifact) MarshalJSON() ([]byte, error) {
	if artifact.Automation != nil {
		return json.Marshal(artifact.Automation)
	}
	return json.Marshal(artifact.Artifact)
}

func (artifact *Artifact) UnmarshalJSON(raw []byte) error {
	var envelope struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	switch envelope.Kind {
	case "hearth.view":
		value, err := dashboards.ReadArtifact(raw)
		if err != nil {
			return err
		}
		*artifact = viewArtifact(value)
	case "hearth.automation":
		value, err := automations.ReadArtifact(raw)
		if err != nil {
			return err
		}
		*artifact = automationArtifact(value)
	default:
		return fmt.Errorf("unsupported artifact kind")
	}
	return nil
}

func viewArtifact(value dashboards.Artifact) Artifact        { return Artifact{Artifact: value} }
func automationArtifact(value automations.Artifact) Artifact { return Artifact{Automation: &value} }
