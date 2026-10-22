package mcpapi

import (
	"encoding/json"
	"fmt"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// definitionInputSchema builds the argument schema for one
// definition-bearing Automation tool, so tools/list advertises the canonical
// Trigger, Condition, and Step constraints and the SDK rejects a schema-invalid
// definition before the handler runs. The remaining properties keep the schema
// derived from the tool's input struct, so this adds no hand-maintained copy of
// the canonical document.
//
// The canonical document is prepared by this module's Register call.
// A contract failure is a startup invariant and panics during registration.
func definitionInputSchema[I any](definition json.RawMessage) any {
	schema, err := mcptools.InputSchemaWithProperty[I]("definition", definition)
	if err != nil {
		panic(fmt.Errorf("automation MCP definition schema: %w", err))
	}
	return schema
}
