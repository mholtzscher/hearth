package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const automationIDField = "automation_id"

const canonicalDefinitionsKeyword = "$defs"

func objectSchema(properties map[string]*huma.Schema, required ...string) *huma.Schema {
	return &huma.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Properties:           properties,
		Required:             required,
	}
}
func stringSchema() *huma.Schema   { return &huma.Schema{Type: "string", MinLength: new(1)} }
func timeSchema() *huma.Schema     { return &huma.Schema{Type: "string", Format: "date-time"} }
func revisionSchema() *huma.Schema { return &huma.Schema{Type: "integer", Minimum: new(float64(1))} }
func enumSchema(values ...string) *huma.Schema {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return &huma.Schema{Enum: enum}
}
func refSchema(ref string) *huma.Schema          { return &huma.Schema{Ref: ref} }
func arraySchema(item *huma.Schema) *huma.Schema { return &huma.Schema{Type: "array", Items: item} }
func pageSchema(item *huma.Schema) *huma.Schema {
	return objectSchema(map[string]*huma.Schema{"items": arraySchema(item), "next_cursor": stringSchema()}, "items")
}

func schemaDocument(raw json.RawMessage) map[string]any {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		panic(fmt.Errorf("canonical schema: %w", err))
	}
	return document
}
func relocateReferences(node any, root string) {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok {
			switch {
			case ref == "#":
				value["$ref"] = root
			case strings.HasPrefix(ref, "#/"):
				value["$ref"] = root + strings.TrimPrefix(ref, "#")
			case strings.HasPrefix(ref, "urn:hearth:schema:automation-definition:v2"):
				value["$ref"] = "#/$defs/definition" + strings.TrimPrefix(
					strings.TrimPrefix(ref, "urn:hearth:schema:automation-definition:v2"),
					"#",
				)
			}
		}
		for _, child := range value {
			relocateReferences(child, root)
		}
	case []any:
		for _, child := range value {
			relocateReferences(child, root)
		}
	}
}

func canonicalResultDefinitions(rawDefinition json.RawMessage) map[string]any {
	definition := schemaDocument(rawDefinition)
	history := schemaDocument(automations.AutomationHistorySchema())
	delete(definition, "$id")
	relocateReferences(definition, "#/$defs/definition")
	evidence, ok := history["$defs"].(map[string]any)
	if !ok {
		panic("canonical automation history schema has no definitions")
	}
	// Outer HTTP records are deliberately not authorities for MCP projections.
	for _, key := range []string{"run", "skip", "historyEntry", "historySummary"} {
		delete(evidence, key)
	}
	relocateReferences(evidence, "#/$defs/evidence")
	defs := map[string]any{"definition": definition, "evidence": map[string]any{"$defs": evidence}}
	return defs
}
