package mcpapi

import (
	"encoding/json"
	"fmt"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// definitionProperty is the tool argument carrying one strict definition.
const definitionProperty = "definition"

// definitionInputSchema builds the argument schema for one
// definition-bearing Automation tool, so tools/list advertises the canonical
// Trigger, Condition, and Step constraints. Registration marks the definition
// handler-validated: the canonical decoder, not SDK input validation, rejects
// it, so callers receive the same locatable diagnostics as HTTP. The remaining
// properties keep the schema derived from the tool's input struct, so this adds
// no hand-maintained copy of the canonical document.
//
// The canonical document is prepared by this module's Register call.
// A contract failure is a startup invariant and panics during registration.
func definitionInputSchema[I any](definition json.RawMessage) any {
	schema, err := mcptools.InputSchemaWithProperty[I](definitionProperty, definition)
	if err != nil {
		panic(fmt.Errorf("automation MCP definition schema: %w", err))
	}
	return schema
}
