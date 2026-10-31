package mcpapi

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

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
	return &huma.Schema{Extensions: schemaDocument(automations.AutomationDefinitionSchema())}
}
