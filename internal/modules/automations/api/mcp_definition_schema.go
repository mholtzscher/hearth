package api

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// mcpAutomationDefinitionInputSchema builds the argument schema for one
// definition-bearing Automation tool.
//
// The definition property carries the same canonical strict schema the Huma
// operations publish, so tools/list advertises the Trigger, Condition, and Step
// constraints and the SDK rejects a schema-invalid definition before the handler
// runs. The remaining properties keep the schema derived from the tool's input
// struct, so this adds no hand-maintained copy of the canonical document.
//
// The compiled codec is shared with Register, so the MCP and Huma surfaces read
// one schema authority; a codec failure is a startup invariant and panics, as it
// does for Register.
func mcpAutomationDefinitionInputSchema[I any]() any {
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation MCP definition schema: %w", err))
	}
	schema, err := mcpapi.InputSchemaWithProperty[I]("definition", codec.AutomationDefinitionSchema())
	if err != nil {
		panic(fmt.Errorf("automation MCP definition schema: %w", err))
	}
	return schema
}
