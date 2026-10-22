package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func Register(server *mcptools.Server, service Automations) {
	handler := &handler{automations: service}
	codec, err := automations.NewDefinitionCodec()
	if err != nil {
		panic(err)
	}
	definition := codec.AutomationDefinitionSchema()
	definitions := canonicalResultDefinitions(definition)
	registerCreateAutomation(server, handler, definition)
	registerListAutomations(server, handler)
	registerGetAutomation(server, handler, definitions)
	registerReplaceAutomation(server, handler, definition)
	registerDeleteAutomation(server, handler)
	registerStartAutomationRun(server, handler)
	registerListAutomationHistory(server, handler)
	registerGetAutomationHistoryEntry(server, handler, definitions)
}
