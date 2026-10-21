//nolint:goconst // Literal schema keywords and wire fields keep these contracts directly reviewable.
package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Fixed independent result roots reference canonical nested documents only.
//
//nolint:gochecknoglobals // Immutable schemas initialized once, never mutated by handlers.
var resultSchemas = sync.OnceValue(buildResultSchemas)

const automationIDField = "automation_id"

func resultSchema(name string) map[string]any { return resultSchemas()[name] }

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}
func stringSchema() map[string]any               { return map[string]any{"type": "string", "minLength": 1} }
func timeSchema() map[string]any                 { return map[string]any{"type": "string", "format": "date-time"} }
func revisionSchema() map[string]any             { return map[string]any{"type": "integer", "minimum": 1} }
func enumSchema(values ...string) map[string]any { return map[string]any{"enum": values} }
func refSchema(ref string) map[string]any        { return map[string]any{"$ref": ref} }
func arraySchema(item any) map[string]any        { return map[string]any{"type": "array", "items": item} }
func pageSchema(item any) map[string]any {
	return objectSchema(map[string]any{"items": arraySchema(item), "next_cursor": stringSchema()}, "items")
}

func buildResultSchemas() map[string]map[string]any {
	defs := canonicalResultDefinitions()
	results := map[string]map[string]any{
		"receipt":          receiptSchema(),
		"detail":           detailSchema(),
		"summaries":        pageSchema(summarySchema()),
		"runReceipt":       runReceiptSchema(),
		"historySummaries": pageSchema(historySummarySchema()),
		"historyDetail":    historyDetailSchema(),
	}
	results["detail"]["$defs"] = defs
	results["historyDetail"]["$defs"] = defs
	return results
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

func canonicalResultDefinitions() map[string]any {
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation definition contract: %w", err))
	}
	definition := schemaDocument(codec.AutomationDefinitionSchema())
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

func receiptSchema() map[string]any {
	receipt := objectSchema(
		map[string]any{automationIDField: stringSchema(), "revision": revisionSchema()},
		automationIDField,
		"revision",
	)
	return receipt
}
