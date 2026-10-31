package mcpapi

import (
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const automationRequestMaxBytes = 196608
const invalidDefinitionCode = "invalid_definition"

type definitionValue struct{ value automations.Definition }

func (value *definitionValue) UnmarshalJSON(raw []byte) error {
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		return automationFailure(err, failureNone)
	}
	value.value = definition
	return nil
}

func (definitionValue) Schema(_ huma.Registry) *huma.Schema {
	codec, err := automations.NewDefinitionCodec()
	if err != nil {
		panic(err)
	}
	return &huma.Schema{Extensions: schemaDocument(codec.AutomationDefinitionSchema())}
}

func validateDefinitionEnvelope(raw json.RawMessage) error {
	if len(raw) > automationRequestMaxBytes {
		return &mcptools.ToolError{Code: invalidDefinitionCode, Message: "argument envelope exceeds limit"}
	}
	return nil
}
