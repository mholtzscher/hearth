package mcpapi

import (
	"bytes"
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const automationRequestMaxBytes = 196608

// The argument types below mirror the Devices MCP custom scalars: a dedicated
// type with an UnmarshalJSON decoder rejects a malformed scalar while the SDK
// decodes tool arguments, so no automation handler or service call ever sees one.
// The stable scalar failure code prefixes every message,
// because the SDK renders a decoding failure as the tool result text and that
// text is the only place the code crosses the MCP boundary.

// definitionArgument extracts the exact strict definition document from one
// raw tool request.
//
// The SDK's typed argument pipeline decodes every argument number through float64
// before a handler sees it, so it cannot carry a value above 2^53 unchanged. The
// untouched request bytes can: this reads the definition member as a
// [json.RawMessage] and hands the canonical strict decoder the same raw JSON the
// Huma request body carries. A literal outside the float64 range is still
// rejected by the SDK's own argument validation before any handler runs.
func definitionArgument(arguments json.RawMessage) (automations.Definition, error) {
	if len(arguments) > automationRequestMaxBytes {
		return automations.Definition{}, &mcptools.ToolError{
			Code:    "invalid_definition",
			Message: "argument envelope exceeds limit",
		}
	}
	var envelope struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(arguments, &envelope); err != nil {
		return automations.Definition{}, &mcptools.ToolError{
			Code:    "invalid_definition",
			Message: "definition must be a JSON object",
		}
	}
	return decodeDefinitionBody(envelope.Definition)
}

func decodeDefinitionBody(raw json.RawMessage) (automations.Definition, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return automations.Definition{}, &mcptools.ToolError{
			Code:    "invalid_definition",
			Message: "definition body is required",
		}
	}
	return automations.DecodeDefinition(raw)
}
