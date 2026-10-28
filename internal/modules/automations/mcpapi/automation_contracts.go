package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const automationIDField = "automation_id"

const canonicalDefinitionsKeyword = "$defs"

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
			case strings.HasPrefix(ref, "urn:hearth:schema:automation-definition:v1"):
				value["$ref"] = "#/$defs/definition" + strings.TrimPrefix(
					strings.TrimPrefix(ref, "urn:hearth:schema:automation-definition:v1"),
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

//nolint:goconst // Canonical document keys stay literal at the composition boundary.
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
