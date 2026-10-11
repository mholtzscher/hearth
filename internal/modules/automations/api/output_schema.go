package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const (
	outputSchemaType       = "type"
	outputSchemaRef        = "$ref"
	outputSchemaObject     = "object"
	outputSchemaString     = "string"
	outputSchemaItems      = "items"
	outputDefinitionSchema = "definition"
	outputRunSchema        = "run"
	outputSkipSchema       = "skip"
)

// automationOutputSchema bundles both contracts under local definitions so MCP
// clients can resolve recursive output without fetching a schema URN.
func automationOutputSchema(name string) map[string]any {
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation output definition schema: %w", err))
	}
	definition := outputSchemaDocument(codec.AutomationDefinitionSchema())
	history := outputSchemaDocument(automations.AutomationHistorySchema())
	delete(definition, "$id")
	delete(history, "$id")
	relocateOutputReferences(definition, "#/$defs/definition", "#/$defs/definition")
	relocateOutputReferences(history, "#/$defs/history", "#/$defs/definition")
	definitions := map[string]any{outputDefinitionSchema: definition, "history": history}
	definitions["automation"] = automationRecordSchema("#/$defs/definition")
	definitions["automationCollection"] = outputPageSchema("#/$defs/automation")
	definitions["historyCollection"] = outputPageSchema("#/$defs/history/$defs/historySummary")
	ref := "#/$defs/history/$defs/" + name
	switch name {
	case "automation", "automationCollection", "historyCollection", outputDefinitionSchema:
		ref = "#/$defs/" + name
	case "trigger":
		ref = "#/$defs/definition/properties/triggers/items"
	case "step", "condition", "branchCondition":
		ref = "#/$defs/definition/$defs/" + name
	}
	return map[string]any{outputSchemaType: outputSchemaObject, outputSchemaRef: ref, "$defs": definitions}
}

func automationRecordSchema(definitionRef string) map[string]any {
	return map[string]any{
		outputSchemaType: outputSchemaObject, "additionalProperties": false,
		"required": []string{"id", "revision", "created_at", "updated_at", outputDefinitionSchema},
		"properties": map[string]any{
			"id":                   map[string]any{outputSchemaType: outputSchemaString, "minLength": 1},
			"revision":             map[string]any{outputSchemaType: "integer", "minimum": 1},
			"created_at":           map[string]any{outputSchemaType: outputSchemaString, "format": "date-time"},
			"updated_at":           map[string]any{outputSchemaType: outputSchemaString, "format": "date-time"},
			outputDefinitionSchema: map[string]any{outputSchemaRef: definitionRef},
		},
	}
}

func outputPageSchema(itemRef string) map[string]any {
	return map[string]any{
		outputSchemaType: outputSchemaObject, "additionalProperties": false, "required": []string{outputSchemaItems},
		"properties": map[string]any{
			outputSchemaItems: map[string]any{
				outputSchemaType:  "array",
				outputSchemaItems: map[string]any{outputSchemaRef: itemRef},
			},
			"next_cursor": map[string]any{outputSchemaType: outputSchemaString},
		},
	}
}

func outputSchemaDocument(raw json.RawMessage) map[string]any {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		panic(fmt.Errorf("decode automation output schema: %w", err))
	}
	return document
}

// These canonical documents contain no instance-valued $ref members. Their
// references target either their own root or the definition document.
func relocateOutputReferences(node any, localRoot, definitionRoot string) {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value[outputSchemaRef].(string); ok {
			switch {
			case ref == "#":
				value[outputSchemaRef] = localRoot
			case strings.HasPrefix(ref, "#/"):
				value[outputSchemaRef] = localRoot + strings.TrimPrefix(ref, "#")
			case strings.HasPrefix(ref, "urn:hearth:schema:automation-definition:v2"):
				value[outputSchemaRef] = definitionRoot + strings.TrimPrefix(strings.TrimPrefix(ref,
					"urn:hearth:schema:automation-definition:v2"), "#")
			}
		}
		for _, child := range value {
			relocateOutputReferences(child, localRoot, definitionRoot)
		}
	case []any:
		for _, child := range value {
			relocateOutputReferences(child, localRoot, definitionRoot)
		}
	}
}

// publishOutputSchemas supplies canonical components before Huma registers
// handlers, so reflection and response transforms cannot replace their unions.
func publishOutputSchemas(api huma.API) {
	components := api.OpenAPI().Components.Schemas.Map()
	for bodyType, name := range map[reflect.Type]string{
		reflect.TypeFor[AutomationBody](): "automation", reflect.TypeFor[AutomationCollectionBody](): "automationCollection",
		reflect.TypeFor[AutomationRunBody](): outputRunSchema, reflect.TypeFor[AutomationSkipBody](): outputSkipSchema,
		reflect.TypeFor[AutomationHistoryEntryBody](): "historyEntry", reflect.TypeFor[AutomationHistorySummaryBody](): "historySummary",
		reflect.TypeFor[AutomationHistoryCollectionBody](): "historyCollection",
	} {
		component := bodyType.Name()
		api.OpenAPI().Components.Schemas.Schema(bodyType, true, component)
		// Give each bundle a resource base: local references then work both in
		// OpenAPI and when Huma serves the component at /schemas/{name}. Huma's
		// standalone rewrite cannot preserve nested component-pointer fragments.
		document := automationOutputSchema(name)
		document["$id"] = "urn:hearth:schema:automation-response:" + name + ":v2"
		components[component] = &huma.Schema{Extensions: document}
	}
}
