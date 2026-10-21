package httpapi

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

func Register(api huma.API, service Automations) {
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation API definition schema: %w", err))
	}
	publishDefinitionSchema(api, codec.AutomationDefinitionSchema())
	publishReplacementSchema(api)
	publishOutputSchemas(api)
	handler := &handler{automations: service}
	registerCreateAutomation(api, handler)
	registerListAutomations(api, handler)
	registerGetAutomation(api, handler)
	registerReplaceAutomation(api, handler)
	registerDeleteAutomation(api, handler)
	registerStartAutomationRun(api, handler)
	registerListHistory(api, handler)
	registerGetHistoryEntry(api, handler)
}
